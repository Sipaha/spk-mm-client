//go:build wails

package desktop

import (
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func setupTray(app *application.App, icon []byte, show func(), toggle func(), n *notifier) {
	l := trayLabelsFor(os.Getenv)
	menu := app.NewMenu()
	menu.Add(l.Open).OnClick(func(*application.Context) { show() })
	if devMenu {
		menu.Add(l.TestNotification).OnClick(func(*application.Context) { n.test() })
	}
	menu.AddSeparator()
	menu.Add(l.Quit).OnClick(func(*application.Context) { app.Quit() })

	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("spk-mattermost")
	tray.SetMenu(menu)
	tray.OnClick(toggle)
}
