package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatLocale(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	assert.Equal(t, "ru-RU", formatLocale(env(map[string]string{"LANG": "en_US.UTF-8", "LC_TIME": "ru_RU.UTF-8"})))
	assert.Equal(t, "de-DE", formatLocale(env(map[string]string{"LC_ALL": "de_DE.UTF-8@euro", "LC_TIME": "ru_RU.UTF-8"})))
	assert.Equal(t, "en-US", formatLocale(env(map[string]string{"LANG": "en_US"})))
	assert.Equal(t, "", formatLocale(env(map[string]string{"LANG": "C.UTF-8"})))
	assert.Equal(t, "", formatLocale(env(map[string]string{"LC_ALL": "POSIX"})))
	assert.Equal(t, "", formatLocale(env(nil)))
}
