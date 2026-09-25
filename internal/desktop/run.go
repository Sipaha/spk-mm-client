//go:build wails

package desktop

import (
	"context"
	"io/fs"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/spk/spk-mm-client/internal/api"
	"github.com/spk/spk-mm-client/internal/api/transport"
	mmevents "github.com/spk/spk-mm-client/internal/events"
)

type Options struct {
	FrontendFS fs.FS
	Service    *api.Service
	Emitter    *mmevents.Emitter
	IconPNG    []byte

	IconUnreadPNG  []byte
	IconMentionPNG []byte
	DevActions     []DevAction // dev builds: extra tray items (fake server controls)
}

// Run starts the Wails loop. Closing the window hides it (tray brings it
// back); quitting is via the tray or ctx cancellation. When the D-Bus session
// bus is unusable on Linux there is no tray, and closing the window quits.
func Run(ctx context.Context, o Options) error {
	// Everything this function starts (tray badge, notification sender)
	// follows ctx, which also ends when Wails shuts down — so nothing calls
	// into Wails after shutdown, before the caller closes the service.
	var shutDown atomic.Bool
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	onShutdown := func() {
		shutDown.Store(true)
		cancel()
	}
	// winMu guards both the window pointer and the "show was requested
	// before the window existed" flag *together*, plus serializes every
	// call into WebviewWindow.Show/Hide/Focus, under one mutex. That is
	// deliberate, not just tidiness: win is written exactly once, right
	// after app.Window.NewWithOptions returns below, but show/deliver/
	// toggle can run before that — the SingleInstance.OnSecondInstanceLaunch
	// and ApplicationLaunchedWithUrl callbacks are wired in during
	// application.New()/app.Event... below, and Wails may invoke either
	// from its own listener goroutine before this function reaches window
	// creation (e.g. a second instance racing the first one's startup).
	// Splitting "read win" / "set pending" / "store win" / "read pending"
	// across independent atomics (an earlier version of this fix did that
	// with atomic.Pointer + atomic.Bool) leaves a TOCTOU gap: a show()
	// call can observe win==nil and be preempted *before* it sets pending,
	// while the window-creation code already read pending as false and
	// moved on — silently dropping the show request. Doing "check win,
	// maybe set pending" and "store win, maybe clear+honor pending" as two
	// atomic critical sections under the same lock removes that gap: they
	// can never interleave.
	//
	// The mutex also serializes Show()/Hide()/Focus() themselves: Wails v3
	// beta.25's Show() and Hide() (pkg/application/webview_window.go) both
	// write w.options.Hidden with no locking of their own, and our usage
	// pattern — SingleInstance's OnSecondInstanceLaunch calling Show() from
	// one goroutine while the WindowClosing hook calls Hide() from another
	// — hits that unsynchronized field concurrently (confirmed with `go
	// build -race`: a real data race inside the pinned, vendored library,
	// not something we can patch). We are the only caller of
	// Show/Hide/Focus/IsVisible for this window, so holding the same lock
	// around every call site removes the concurrency at its source.
	var (
		winMu   sync.Mutex
		win     *application.WebviewWindow
		pending bool
	)
	show := func() {
		winMu.Lock()
		defer winMu.Unlock()
		if win == nil {
			pending = true
			return
		}
		win.Show()
		win.Focus()
	}
	deliver := func(raw string) {
		// HandleDeepLink reports failures to the UI via login_failed.
		go func() { _ = o.Service.HandleDeepLink(context.Background(), raw) }()
		show()
	}

	// Probe D-Bus once; every D-Bus-backed feature follows this one result
	// (see integrationsFor).
	feat := integrationsFor(runtime.GOOS, sessionBusState())
	if feat.cutOffBus {
		cutOffSessionBus() // before application.New() initializes GTK
	}

	n := newNotifier(ctx, func(data map[string]any) {
		if id, ch, ok := clickTarget(data); ok {
			o.Service.NotificationClicked(id, ch)
		}
		show()
	})
	if !feat.notifications {
		n.disable()
	}
	o.Service.SetNotifier(n)

	var single *application.SingleInstanceOptions
	if feat.singleInstance {
		single = &application.SingleInstanceOptions{
			UniqueID: instanceID(os.Getenv("SPK_MM_CLIENT_HOME")),
			// Linux/Windows: the OS starts a second process with the
			// mmauth:// URL; Wails forwards its args here and exits it.
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				if u, ok := deepLinkFromArgs(d.Args); ok {
					deliver(u)
					return
				}
				show()
			},
		}
	}

	app := application.New(application.Options{
		Name:        "spk-mm-client",
		Description: "Lightweight Mattermost client",
		Icon:        o.IconPNG,
		Services: []application.Service{
			application.NewService(transport.NewAPI(o.Service)),
			// n, not n.svc: notifier.ServiceStartup bounds the raw
			// notification service's startup so a broken/absent OS
			// notification backend can never abort app.Run() (see notify.go).
			application.NewService(n),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(o.FrontendFS)},
		// nil on Linux when the D-Bus probe failed or timed out: Wails'
		// SingleInstance there dials the bus with no timeout and os.Exit(1)s
		// inside application.New() on failure (see busprobe_linux.go).
		SingleInstance: single,
		OnShutdown:     onShutdown,
	})

	started := make(chan struct{})
	var startedOnce sync.Once
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		startedOnce.Do(func() { close(started) })
	})

	// Cold start with the URL (Linux/Windows argv) and macOS open-URL events.
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) {
		if u := e.Context().URL(); u != "" {
			deliver(u)
		}
	})

	go func() {
		ch, unsub := o.Emitter.Subscribe()
		defer unsub()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				app.Event.Emit(ev.Type, ev.Payload)
			}
		}
	}()

	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "spk-mm-client",
		Width:  1200,
		Height: 800,
		// Matches --color-app (#1f1f23) in frontend/src/index.css: the app is
		// dark by default, so the window background must not flash white/light
		// before the webview paints.
		BackgroundColour: application.NewRGBA(31, 31, 35, 255),
		URL:              "/",
		DevToolsEnabled:  devToolsEnabled,
		Linux:            application.LinuxWindow{WebviewGpuPolicy: webviewGPUPolicy(os.Getenv("SPK_MM_CLIENT_GPU"))},
	})
	if feat.closeHides() {
		w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			e.Cancel()
			winMu.Lock()
			w.Hide()
			winMu.Unlock()
		})
	}
	// Without a tray nothing could bring a hidden window back, so the close
	// proceeds and Wails quits on the last window closed.

	// Publish the window and, in the same critical section, capture
	// whether a show() arrived while win was still nil. Honoring it via a
	// direct call (not a recursive show()) avoids re-locking winMu.
	winMu.Lock()
	win = w
	wasPending := pending
	pending = false
	winMu.Unlock()
	if wasPending {
		show()
	}

	toggle := func() {
		winMu.Lock()
		defer winMu.Unlock()
		if win == nil {
			return
		}
		if win.IsVisible() {
			win.Hide()
		} else {
			win.Show()
			win.Focus()
		}
	}
	if feat.tray {
		icons := trayIcons{plain: o.IconPNG, unread: o.IconUnreadPNG, mention: o.IconMentionPNG}
		tray := setupTray(app, icons, show, toggle, n, o.DevActions)
		o.Service.OnBadge(trayBadge(ctx, tray, icons, messagesLanguage(os.Getenv), started))
	}

	go func() {
		<-ctx.Done()
		if !shutDown.Load() { // the caller's ctx ended; not our own shutdown
			app.Quit()
		}
	}()
	return app.Run()
}

func webviewGPUPolicy(env string) application.WebviewGpuPolicy {
	switch parseGPUPolicy(env) {
	case gpuAlways:
		return application.WebviewGpuPolicyAlways
	case gpuOnDemand:
		return application.WebviewGpuPolicyOnDemand
	case gpuNever:
		return application.WebviewGpuPolicyNever
	}
	return application.WebviewGpuPolicyAlways // Wails' zero value
}
