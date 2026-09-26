package media

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLoopbackEnv(t *testing.T) (*Loopback, *streamUpstream) {
	t.Helper()
	e, u := newStreamEnv(t)
	l := NewLoopback(NewStreamer(e.origin))
	t.Cleanup(func() { _ = l.Close() })
	return l, u
}

func lbDo(t *testing.T, method, rawURL string, hdr http.Header, host string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header[k] = v
	}
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestLoopbackStartsLazilyOnItsOwnLoopbackPort(t *testing.T) {
	l, _ := newLoopbackEnv(t)
	assert.Nil(t, l.srv, "nothing listens before the UI asks for the address")

	base, err := l.Base()
	require.NoError(t, err)
	u, err := url.Parse(base)
	require.NoError(t, err)
	assert.Equal(t, "http", u.Scheme)
	host, port, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	assert.NotEqual(t, "0", port)
	token := strings.TrimPrefix(u.Path, "/")
	assert.Len(t, token, 43, "32 random bytes, base64url without padding")
	assert.NotContains(t, token, "/")

	again, err := l.Base()
	require.NoError(t, err)
	assert.Equal(t, base, again, "one server per run")

	other := NewLoopback(NewStreamer(&testOrigin{}))
	defer other.Close()
	otherBase, err := other.Base()
	require.NoError(t, err)
	assert.NotEqual(t, token, otherBase[strings.LastIndex(otherBase, "/")+1:], "a new token every run")
}

func TestLoopbackServesStreamsWithItsHeaders(t *testing.T) {
	l, _ := newLoopbackEnv(t)
	base, err := l.Base()
	require.NoError(t, err)

	resp, body := lbDo(t, http.MethodGet, base+"/stream/1/clip", http.Header{"Range": {"bytes=10-19"}, "Origin": {"https://evil.example"}}, "")
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Equal(t, "bytes 10-19/1000", resp.Header.Get("Content-Range"))
	assert.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
	assert.Equal(t, "video/webm", resp.Header.Get("Content-Type"))
	assert.Equal(t, clipBytes(1000)[10:20], body)
	assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Contains(t, resp.Header.Get("Cache-Control"), "private")
	for k := range resp.Header {
		assert.False(t, strings.HasPrefix(k, "Access-Control-"), "no CORS: %s", k)
	}

	resp, _ = lbDo(t, http.MethodHead, base+"/stream/1/clip", nil, "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = lbDo(t, http.MethodGet, base+"/stream/1/pdf", nil, "")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
	resp, _ = lbDo(t, http.MethodGet, base+"/stream/2/clip", nil, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "only while the server's worker is live")
}

func TestLoopbackRejectsWrongTokenHostAndMethod(t *testing.T) {
	l, _ := newLoopbackEnv(t)
	base, err := l.Base()
	require.NoError(t, err)
	u, _ := url.Parse(base)
	root := "http://" + u.Host
	token := strings.TrimPrefix(u.Path, "/")
	wrong := strings.Repeat("A", len(token))

	for _, p := range []string{
		"/" + wrong + "/stream/1/clip", "/stream/1/clip", "/" + token, "/" + token + "/", "/" + token + "/stream/1",
		"/" + token + "/stream/1/clip/x", "/" + token + "/feed/1/clip", "/" + token + "/stream/x/clip",
		"/" + token + "/stream/0/clip", "/" + token + "/stream/1/..%2Fx", "/", "/favicon.ico",
	} {
		resp, _ := lbDo(t, http.MethodGet, root+p, nil, "")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, p)
	}
	for _, host := range []string{"localhost:" + u.Port(), "evil.example", "127.0.0.1", "127.0.0.1:1"} {
		resp, _ := lbDo(t, http.MethodGet, base+"/stream/1/clip", nil, host)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, host)
	}
	for _, m := range []string{http.MethodPost, http.MethodOptions, http.MethodPut, http.MethodDelete} {
		resp, _ := lbDo(t, m, base+"/stream/1/clip", http.Header{"Origin": {"https://evil.example"}, "Access-Control-Request-Method": {"GET"}}, "")
		assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, m)
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"), m)
	}
}

func TestLoopbackCloseStopsServingAndCancelsStreams(t *testing.T) {
	l, u := newLoopbackEnv(t)
	base, err := l.Base()
	require.NoError(t, err)
	resp, err := http.Get(base + "/stream/1/endless")
	require.NoError(t, err)
	defer resp.Body.Close()
	_, err = io.ReadFull(resp.Body, make([]byte, 64<<10))
	require.NoError(t, err)

	require.NoError(t, l.Close())
	select {
	case <-u.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the server did not close the upstream request")
	}
	c := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	_, err = c.Get(base + "/stream/1/clip")
	assert.Error(t, err, "nothing listens after Close")
	_, err = l.Base()
	assert.Error(t, err, "no restart after Close")
	assert.NoError(t, l.Close(), "Close twice is fine")
	assert.NoError(t, NewLoopback(NewStreamer(&testOrigin{})).Close(), "closing a server that never started")
}

// syncBuffer is a log sink safe for the server's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestLoopbackTokenNeverReachesTheLogs(t *testing.T) {
	var logs syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	l, _ := newLoopbackEnv(t)
	base, err := l.Base()
	require.NoError(t, err)
	token := base[strings.LastIndex(base, "/")+1:]
	for _, p := range []string{"/stream/1/clip", "/stream/1/pdf", "/stream/1/gone", "/stream/2/clip", "/other"} {
		lbDo(t, http.MethodGet, base+p, nil, "")
	}
	lbDo(t, http.MethodGet, base+"/stream/1/clip", http.Header{"Range": {"bytes=x"}}, "")
	lbDo(t, http.MethodGet, base+"/stream/1/clip", nil, "evil.example")
	lbDo(t, http.MethodPost, base+"/stream/1/clip", nil, "")
	require.NoError(t, l.Close())

	out := logs.String()
	assert.NotEmpty(t, out, "failures are logged (without the token)")
	assert.NotContains(t, out, token)
}
