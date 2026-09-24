//go:build wails

package desktop

import (
	"context"
	"io/fs"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/api/transport"
	mmevents "github.com/spk/spk-mattermost/internal/events"
)

type Options struct {
	FrontendFS fs.FS
	Service    *api.Service
	Emitter    *mmevents.Emitter
	IconPNG    []byte
}

// Run starts the Wails loop. Closing the window hides it (tray brings it
// back); quitting is via the tray or ctx cancellation.
func Run(ctx context.Context, o Options) error {
	var win *application.WebviewWindow
	show := func() {
		if win == nil {
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

	var n *notifier
	n = newNotifier(func(data map[string]any) {
		slog.Info("notification clicked", "data", data)
		show()
	})

	app := application.New(application.Options{
		Name:        "spk-mattermost",
		Description: "Lightweight Mattermost client",
		Icon:        o.IconPNG,
		Services: []application.Service{
			application.NewService(transport.NewAPI(o.Service)),
			application.NewService(n.svc),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(o.FrontendFS)},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: UniqueID,
			// Linux/Windows: the OS starts a second process with the
			// mmauth:// URL; Wails forwards its args here and exits it.
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				if u, ok := deepLinkFromArgs(d.Args); ok {
					deliver(u)
					return
				}
				show()
			},
		},
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
		for ev := range ch {
			app.Event.Emit(ev.Type, ev.Payload)
		}
	}()

	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "spk-mattermost",
		Width:            1200,
		Height:           800,
		BackgroundColour: application.NewRGBA(250, 250, 250, 255),
		URL:              "/",
		DevToolsEnabled:  devToolsEnabled,
	})
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		win.Hide()
	})

	toggle := func() {
		if win.IsVisible() {
			win.Hide()
		} else {
			show()
		}
	}
	setupTray(app, o.IconPNG, show, toggle, n)

	go func() {
		<-ctx.Done()
		app.Quit()
	}()
	return app.Run()
}
