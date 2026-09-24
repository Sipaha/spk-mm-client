package desktop

import "strings"

type trayLabels struct {
	Open             string
	TestNotification string
	Quit             string
}

// trayLabelsFor picks ru or en tray menu labels the way gettext (and
// WebKitGTK, which sets the UI's navigator.language) picks the messages
// language: the locale is the first set of LC_ALL, LC_MESSAGES, LANG; unless
// it is C/POSIX, the colon-separated LANGUAGE list is tried first. The first
// entry that is ru or en wins; otherwise English.
func trayLabelsFor(getenv func(string) string) trayLabels {
	if messagesLanguage(getenv) == "ru" {
		return trayLabels{Open: "Открыть", TestNotification: "Тестовое уведомление", Quit: "Выход"}
	}
	return trayLabels{Open: "Open", TestNotification: "Test notification", Quit: "Quit"}
}

func messagesLanguage(getenv func(string) string) string {
	var loc string
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if loc = getenv(k); loc != "" {
			break
		}
	}
	var cands []string
	if loc != "" && loc != "C" && !strings.HasPrefix(loc, "C.") && loc != "POSIX" {
		cands = strings.Split(getenv("LANGUAGE"), ":")
	}
	for _, c := range append(cands, loc) {
		c = strings.ToLower(c)
		switch {
		case strings.HasPrefix(c, "ru"):
			return "ru"
		case strings.HasPrefix(c, "en"):
			return "en"
		}
	}
	return "en"
}
