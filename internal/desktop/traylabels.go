package desktop

import "strings"

type trayLabels struct {
	Open             string
	TestNotification string
	Quit             string
}

// trayLabelsFor picks ru or en tray menu labels from the system locale:
// the first set of LC_ALL, LC_MESSAGES, LANG (POSIX precedence) starting
// with "ru" means Russian, anything else English.
func trayLabelsFor(getenv func(string) string) trayLabels {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := getenv(k); v != "" {
			if strings.HasPrefix(strings.ToLower(v), "ru") {
				return trayLabels{Open: "Открыть", TestNotification: "Тестовое уведомление", Quit: "Выход"}
			}
			break
		}
	}
	return trayLabels{Open: "Open", TestNotification: "Test notification", Quit: "Quit"}
}
