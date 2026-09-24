package desktop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTrayLabelsFollowLocale(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	ru := trayLabels{Open: "Открыть", TestNotification: "Тестовое уведомление", Quit: "Выход"}
	en := trayLabels{Open: "Open", TestNotification: "Test notification", Quit: "Quit"}

	assert.Equal(t, ru, trayLabelsFor(env(map[string]string{"LANG": "ru_RU.UTF-8"})))
	assert.Equal(t, en, trayLabelsFor(env(map[string]string{"LANG": "en_US.UTF-8"})))
	assert.Equal(t, en, trayLabelsFor(env(nil)), "no locale → English")
	// POSIX precedence: LC_ALL > LC_MESSAGES > LANG.
	assert.Equal(t, en, trayLabelsFor(env(map[string]string{"LC_ALL": "C", "LANG": "ru_RU.UTF-8"})))
	assert.Equal(t, ru, trayLabelsFor(env(map[string]string{"LC_MESSAGES": "ru_RU", "LANG": "en_US.UTF-8"})))
	assert.Equal(t, en, trayLabelsFor(env(map[string]string{"LC_ALL": "en_GB", "LC_MESSAGES": "ru_RU"})))
	// gettext: LANGUAGE (first ru/en entry) overrides the locale unless it is C.
	assert.Equal(t, ru, trayLabelsFor(env(map[string]string{"LANGUAGE": "de:ru", "LANG": "en_US.UTF-8"})))
	assert.Equal(t, en, trayLabelsFor(env(map[string]string{"LANGUAGE": "en_US", "LANG": "ru_RU.UTF-8"})))
	assert.Equal(t, en, trayLabelsFor(env(map[string]string{"LANGUAGE": "ru", "LC_ALL": "C.UTF-8"})))
	assert.Equal(t, ru, trayLabelsFor(env(map[string]string{"LANGUAGE": "de", "LANG": "ru_RU.UTF-8"})))
	// This machine: English messages, Russian formats only.
	assert.Equal(t, en, trayLabelsFor(env(map[string]string{"LANGUAGE": "en_US", "LANG": "en_US.UTF-8", "LC_TIME": "ru_RU.UTF-8"})))
}

func TestTrayTooltip(t *testing.T) {
	assert.Equal(t, "spk-mattermost", trayTooltip("ru", false, 0))
	assert.Equal(t, "spk-mattermost — есть непрочитанные", trayTooltip("ru", true, 0))
	assert.Equal(t, "spk-mattermost — 1 упоминание", trayTooltip("ru", true, 1))
	assert.Equal(t, "spk-mattermost — 3 упоминания", trayTooltip("ru", true, 3))
	assert.Equal(t, "spk-mattermost — 11 упоминаний", trayTooltip("ru", false, 11))
	assert.Equal(t, "spk-mattermost — 21 упоминание", trayTooltip("ru", false, 21))
	assert.Equal(t, "spk-mattermost — 25 упоминаний", trayTooltip("ru", false, 25))
	assert.Equal(t, "spk-mattermost — unread messages", trayTooltip("en", true, 0))
	assert.Equal(t, "spk-mattermost — 1 mention", trayTooltip("en", false, 1))
	assert.Equal(t, "spk-mattermost — 2 mentions", trayTooltip("en", false, 2))
}

func TestTrayIconFor(t *testing.T) {
	assert.Equal(t, iconPlain, trayIconFor(false, 0))
	assert.Equal(t, iconUnread, trayIconFor(true, 0))
	assert.Equal(t, iconMention, trayIconFor(true, 2))
	assert.Equal(t, iconMention, trayIconFor(false, 1))
}
