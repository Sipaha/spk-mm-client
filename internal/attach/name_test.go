package attach

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanNameKeepsWhatAFileNameShows(t *testing.T) {
	for raw, want := range map[string]string{
		"report.pdf":                      "report.pdf",
		"../../etc/passwd":                "passwd",
		`C:\Users\me\report.pdf`:          "report.pdf",
		"pa\x07ss\u202ewd\u200f.txt\n":    "passwd.txt",
		"in\u200bvisible\ufeff.txt":       "invisible.txt",
		"  \x00 ":                         "attachment",
		"":                                "attachment",
		".":                               "attachment",
		"..":                              "attachment",
		".\u200b.":                        "attachment",
		"имя с пробелом.png":              "имя с пробелом.png",
		strings.Repeat("я", 300) + ".png": strings.Repeat("я", 196) + ".png",
		strings.Repeat("a", 250) + "." + strings.Repeat("b", 30): strings.Repeat("a", 200),
	} {
		assert.Equal(t, want, cleanName(raw), "%q", raw)
	}
}

// Names of files on disk (drop, dialog, clipboard) are cleaned like names
// from the page.
func TestAddPathCleansTheName(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	a, err := e.s.AddPath(1, "c1", "", e.file("gpj\u202e.exe", "x"))
	require.NoError(t, err)
	assert.Equal(t, "gpj.exe", a.Name)
}
