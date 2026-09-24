package desktop

import (
	"fmt"
	"strings"
)

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

type trayIcon int

const (
	iconPlain trayIcon = iota
	iconUnread
	iconMention
)

func trayIconFor(unread bool, mentions int) trayIcon {
	switch {
	case mentions > 0:
		return iconMention
	case unread:
		return iconUnread
	}
	return iconPlain
}

// ruPlural picks the Russian plural form for n (1 упоминание, 3 упоминания, 5 упоминаний).
func ruPlural(n int, one, few, many string) string {
	n10, n100 := n%10, n%100
	switch {
	case n10 == 1 && n100 != 11:
		return one
	case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
		return few
	}
	return many
}

func trayTooltip(lang string, unread bool, mentions int) string {
	const app = "spk-mattermost"
	switch {
	case mentions > 0 && lang == "ru":
		return fmt.Sprintf("%s — %d %s", app, mentions, ruPlural(mentions, "упоминание", "упоминания", "упоминаний"))
	case mentions > 0:
		word := "mentions"
		if mentions == 1 {
			word = "mention"
		}
		return fmt.Sprintf("%s — %d %s", app, mentions, word)
	case unread && lang == "ru":
		return app + " — есть непрочитанные"
	case unread:
		return app + " — unread messages"
	}
	return app
}
