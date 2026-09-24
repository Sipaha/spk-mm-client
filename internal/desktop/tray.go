//go:build wails

package desktop

import (
	"context"
	"os"
	"runtime"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mattermost/internal/api"
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
	setTrayText(tray, "spk-mattermost")
	tray.SetMenu(menu)
	tray.OnClick(toggle)
	return tray
}

// trayBadge returns the Service.OnBadge callback. Tray updates go through
// InvokeSync on the GTK main thread, so they run on their own goroutine,
// start only after ApplicationStarted + 200 ms (spike S3), and apply just
// the latest badge — the service's goroutine never waits for GTK.
func trayBadge(ctx context.Context, tray *application.SystemTray, icons trayIcons, lang string, started <-chan struct{}) func(api.Badge) {
	ch := make(chan api.Badge, 1)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-started:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-ch:
				setTrayText(tray, trayTooltip(lang, b.Unread, b.Mentions))
				tray.SetIcon(icons.pick(trayIconFor(b.Unread, b.Mentions)))
			}
		}
	}()
	return func(b api.Badge) { offerLatest(ch, b) }
}

// setTrayText shows text on hover over the tray icon. Wails beta.25's Linux
// tray ignores SetTooltip (a "TBD" no-op) and fixes the StatusNotifierItem
// Id/ToolTip to the label when the tray starts (default "Wails"), so on Linux
// the text also goes to the label — the SNI Title, which hosts fall back to.
// Not on macOS: there the label is drawn next to the menu-bar icon.
func setTrayText(tray *application.SystemTray, text string) {
	tray.SetTooltip(text)
	if runtime.GOOS == "linux" {
		tray.SetLabel(text)
	}
}
