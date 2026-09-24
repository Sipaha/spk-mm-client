package auth

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cb(token, srv string) string {
	q := url.Values{"MMAUTHTOKEN": {token}, "MMCSRF": {"csrf"}}
	if srv != "" {
		q.Set("srv", srv)
	}
	return CallbackURL + "?" + q.Encode()
}

func TestGitLabLoginURL(t *testing.T) {
	assert.Equal(t,
		"https://mm.example.com/oauth/gitlab/mobile_login?redirect_to=mmauth%3A%2F%2Fcallback",
		GitLabLoginURL("https://mm.example.com"))
}

func TestCompleteMatchesPendingBySrv(t *testing.T) {
	s := NewSSO()
	s.Begin(1, "https://a.example.com")
	s.Begin(2, "https://b.example.com")
	res, err := s.Complete(cb("tok", "https://b.example.com/"))
	require.NoError(t, err)
	assert.Equal(t, Result{ServerID: 2, Token: "tok"}, res)
}

func TestCompleteMatchesSiteURLAlias(t *testing.T) {
	s := NewSSO()
	// user typed "mm.example.com"; server reports a different SiteURL
	s.Begin(5, "https://mm.example.com", "https://chat.example.com")
	res, err := s.Complete(cb("tok", "HTTPS://CHAT.example.com"))
	require.NoError(t, err)
	assert.Equal(t, int64(5), res.ServerID)
}

func TestCompleteWithoutSrvNeedsExactlyOnePending(t *testing.T) {
	s := NewSSO()
	_, err := s.Complete(cb("t", ""))
	assert.ErrorIs(t, err, ErrNoPendingLogin)

	s.Begin(1, "https://a")
	s.Begin(2, "https://b")
	_, err = s.Complete(cb("t", ""))
	assert.ErrorIs(t, err, ErrServerMismatch)

	s.Cancel(2)
	res, err := s.Complete(cb("t", ""))
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.ServerID)
}

func TestCompleteRejectsUnknownServer(t *testing.T) {
	s := NewSSO()
	s.Begin(1, "https://a.example.com")
	_, err := s.Complete(cb("tok", "https://evil.example.com"))
	assert.ErrorIs(t, err, ErrServerMismatch)
	// the legit pending login survives the forged callback
	_, err = s.Complete(cb("tok2", "https://a.example.com"))
	assert.NoError(t, err)
}

func TestCompleteRejectsMalformed(t *testing.T) {
	s := NewSSO()
	s.Begin(1, "https://a")
	for _, raw := range []string{"https://a/?MMAUTHTOKEN=x", "mmauth://callback", "mmauth://callback?MMAUTHTOKEN=", "%%%"} {
		_, err := s.Complete(raw)
		assert.ErrorIs(t, err, ErrMalformedCallback, raw)
	}
}

func TestCompleteTwiceIsAlreadyCompleted(t *testing.T) {
	s := NewSSO()
	s.Begin(1, "https://a")
	raw := cb("tok", "https://a")
	_, err := s.Complete(raw)
	require.NoError(t, err)
	_, err = s.Complete(raw)
	assert.ErrorIs(t, err, ErrAlreadyCompleted)
}

func TestPendingExpires(t *testing.T) {
	s := NewSSO()
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	s.Begin(1, "https://a")
	now = now.Add(pendingTTL + time.Second)
	_, err := s.Complete(cb("tok", "https://a"))
	assert.ErrorIs(t, err, ErrNoPendingLogin)
}

func TestFindCallbackArg(t *testing.T) {
	u, ok := FindCallbackArg([]string{"/usr/bin/spk-mattermost", "MMAUTH://callback?MMAUTHTOKEN=x"})
	assert.True(t, ok)
	assert.Equal(t, "MMAUTH://callback?MMAUTHTOKEN=x", u)
	_, ok = FindCallbackArg([]string{"/usr/bin/spk-mattermost", "--browser"})
	assert.False(t, ok)
}

// Two server entries can share a URL alias (same SiteURL). The most recently
// started login wins, whatever the map iteration order.
func TestCompleteAmbiguousSrvPicksMostRecent(t *testing.T) {
	for i := 0; i < 50; i++ {
		s := NewSSO()
		clock := time.Unix(1000, 0)
		s.now = func() time.Time { return clock }
		s.Begin(1, "https://a.example.com", "https://chat.example.com")
		clock = clock.Add(time.Second)
		s.Begin(2, "https://b.example.com", "https://chat.example.com")
		clock = clock.Add(time.Second)
		s.Begin(3, "https://c.example.com")
		res, err := s.Complete(cb("tok", "https://chat.example.com"))
		require.NoError(t, err)
		require.Equal(t, int64(2), res.ServerID)
	}
}
