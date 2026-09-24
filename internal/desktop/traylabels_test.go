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
}
