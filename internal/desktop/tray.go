//go:build wails

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

func setupTray(app *application.App, icon []byte, show func(), toggle func(), n *notifier) {
	menu := app.NewMenu()
	menu.Add("Открыть").OnClick(func(*application.Context) { show() })
	if devMenu {
		menu.Add("Тестовое уведомление").OnClick(func(*application.Context) { n.test() })
	}
	menu.AddSeparator()
	menu.Add("Выход").OnClick(func(*application.Context) { app.Quit() })

	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("spk-mattermost")
	tray.SetMenu(menu)
	tray.OnClick(toggle)
}
