package desktop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeepLinkFromArgs(t *testing.T) {
	u, ok := deepLinkFromArgs([]string{"mmauth://callback?MMAUTHTOKEN=x"})
	assert.True(t, ok)
	assert.Equal(t, "mmauth://callback?MMAUTHTOKEN=x", u)

	// Windows passes the URL quoted in some launchers
	u, ok = deepLinkFromArgs([]string{`"mmauth://callback?MMAUTHTOKEN=y"`})
	assert.True(t, ok)
	assert.Equal(t, "mmauth://callback?MMAUTHTOKEN=y", u)

	_, ok = deepLinkFromArgs([]string{"--browser"})
	assert.False(t, ok)
}
