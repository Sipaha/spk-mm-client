package rest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeURL(t *testing.T) {
	ok := map[string]string{
		"https://mm.example.com":          "https://mm.example.com",
		"https://mm.example.com/":         "https://mm.example.com",
		"  HTTPS://MM.Example.COM//  ":    "https://mm.example.com",
		"mm.example.com":                  "https://mm.example.com",
		"http://127.0.0.1:8065":           "http://127.0.0.1:8065",
		"https://example.com/mattermost/": "https://example.com/mattermost",
		"https://mm.example.com/?x=1#f":   "https://mm.example.com",
	}
	for in, want := range ok {
		got, err := NormalizeURL(in)
		if assert.NoError(t, err, in) {
			assert.Equal(t, want, got, in)
		}
	}
	for _, bad := range []string{"", "   ", "ftp://mm.example.com", "https://", "://nohost"} {
		_, err := NormalizeURL(bad)
		assert.Error(t, err, bad)
	}
}
