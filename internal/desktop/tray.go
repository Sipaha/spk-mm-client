//go:build wails

package desktop

import (
	"context"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mm-client/internal/api"
)

type trayIcons struct{ plain, unread, mention []byte }

func (t trayIcons) pick(i trayIcon) []byte {
	switch i {
	case iconMention:
		return t.mention
	case iconUnread:
		return t.unread
	}
	return t.plain
}

// DevAction is an extra tray menu item of dev builds (fake server controls).
type DevAction struct {
	Label string
	Run   func()
}

func setupTray(app *application.App, icons trayIcons, show, toggle func(), n *notifier, dev []DevAction) *application.SystemTray {
	l := trayLabelsFor(os.Getenv)
	menu := app.NewMenu()
	menu.Add(l.Open).OnClick(func(*application.Context) { show() })
	if devMenu {
		menu.Add(l.TestNotification).OnClick(func(*application.Context) { n.test() })
		for _, a := range dev {
			menu.Add(a.Label).OnClick(func(*application.Context) { go a.Run() })
		}
	}
	menu.AddSeparator()
	menu.Add(l.Quit).OnClick(func(*application.Context) { app.Quit() })

	tray := app.SystemTray.New()
	tray.SetIcon(icons.plain)
	setTrayText(tray, nil, "spk-mm-client")
	tray.SetMenu(menu)
	tray.OnClick(toggle)
	return tray
}

// trayBadge returns the Service.OnBadge callback. Tray updates go through
// InvokeSync on the GTK main thread, so they run on their own goroutine,
// start only once the native tray has finished starting up, and apply the
// latest badge — the service's goroutine never waits for GTK or D-Bus.
func trayBadge(ctx context.Context, tray *application.SystemTray, icons trayIcons, lang string, started <-chan struct{}) func(api.Badge) {
	ch := make(chan api.Badge, 1)
	pt := newPlatformTray(ctx)
	go runTrayBadge(ctx, ch, lang, trayApplier{
		waitReady: func(c context.Context) bool { return pt.waitReady(c, started) },
		setText:   func(text string) { setTrayText(tray, pt, text) },
		setIcon:   func(ic trayIcon) { tray.SetIcon(icons.pick(ic)) },
	})
	return func(b api.Badge) { offerLatest(ch, b) }
}

// setTrayText shows text on hover over the tray icon. On Linux, Wails 3.1.0's
// SetLabel updates both SNI Title and ToolTip.V2 (title), whereas SetTooltip
// sets ToolTip.V3 (description) under V2 — calling both with the same string
// makes xapp-sn-watcher and SNI hosts render duplicate lines ("text\ntext").
// Linux therefore uses SetLabel (keeping V3 empty) plus platformTray.syncTooltip
// to emit org.kde.StatusNotifierItem.NewToolTip. Off Linux, SetTooltip sets the
// hover text directly (macOS must not call SetLabel, which draws menu-bar text).
func setTrayText(tray *application.SystemTray, pt *platformTray, text string) {
	if runtime.GOOS == "linux" {
		tray.SetLabel(text)
	} else {
		tray.SetTooltip(text)
	}
	if pt != nil {
		pt.syncTooltip(text)
	}
}
