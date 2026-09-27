package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/api"
	"github.com/spk/spk-mm-client/internal/events"
	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/store"
)

func setup(t *testing.T, testAPI bool) (*httptest.Server, string, *mmfake.Server) {
	ts, token, fake, _ := setupWithService(t, testAPI)
	return ts, token, fake
}

func setupWithService(t *testing.T, testAPI bool) (*httptest.Server, string, *mmfake.Server, *api.Service) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	em := events.NewEmitter()
	fake := mmfake.Start(mmfake.Options{})
	t.Cleanup(fake.Close)
	svc := api.NewService(st, em, func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
	dist := fstest.MapFS{"index.html": {Data: []byte("<html><head></head><body></body></html>")}}
	notes := &api.RecordingNotifier{}
	svc.SetNotifier(notes)
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(svc.Close)
	mc, err := media.New(media.Options{Dir: t.TempDir(), Origin: svc})
	require.NoError(t, err)
	svc.SetFileOpener((&api.RecordingOpener{}).Open)
	h, token := newBrowserHandler(svc, em, dist, fake, testAPI, notes, mc, &api.RecordingOpener{}, recordReveals(svc))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, token, fake, svc
}

func TestMediaNeedsThePageCookie(t *testing.T) {
	ts, token, fake, svc := setupWithService(t, false)
	ctx := context.Background()
	srv, err := svc.AddServer(ctx, fake.URL())
	require.NoError(t, err)
	_, err = svc.LoginWithPassword(ctx, srv.ID, "alice", "secret")
	require.NoError(t, err)
	require.Eventually(t, func() bool { // media is fetched only while live
		list, err := svc.ListServers(ctx)
		return err == nil && len(list) == 1 && list[0].State == "live"
	}, 10*time.Second, 20*time.Millisecond)
	u := fmt.Sprintf("%s/media/%d/avatar/u-bob?v=0", ts.URL, srv.ID)

	resp, err := http.Get(u)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "no cookie, no token")

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err = c.Get(ts.URL + "/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	var ck *http.Cookie
	for _, x := range resp.Cookies() {
		if x.Name == "spk_media" {
			ck = x
		}
	}
	require.NotNil(t, ck, "the page sets the media cookie")
	assert.True(t, ck.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, ck.SameSite)
	assert.Equal(t, "/media/", ck.Path)

	resp, err = c.Get(u)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))

	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode, "API-style callers may use the bearer token")
}

func TestIndexCarriesTokenAndIsNotCached(t *testing.T) {
	ts, token, _ := setup(t, false)
	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), `<meta name="spk-mm-client-api-token" content="`+token+`">`)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

// DNS rebinding: evil.example resolves to 127.0.0.1, so the browser sends
// Host: evil.example:PORT and a matching Origin. The server must not hand out
// the token nor accept API calls for a non-loopback Host.
func TestNonLoopbackHostIsRejected(t *testing.T) {
	ts, token, _ := setup(t, true)
	port := ts.URL[strings.LastIndex(ts.URL, ":")+1:]
	evil := "evil.example:" + port

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Host = evil
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.NotContains(t, string(body), token)

	for _, path := range []string{"/api/ListServers", "/api/_test/deeplink"} {
		req, _ = http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(`{}`))
		req.Host = evil
		req.Header.Set("Origin", "http://"+evil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events?token="+token, nil)
	req.Host = evil
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err, "SSE stream must be refused, not opened")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "SSE")
}

func TestLoopbackHostsAreAccepted(t *testing.T) {
	ts, token, _ := setup(t, false)
	port := ts.URL[strings.LastIndex(ts.URL, ":")+1:]
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port, "LOCALHOST:" + port} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode, host)

		req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/ListServers", strings.NewReader(`{}`))
		req.Host = host
		req.Header.Set("Origin", "http://"+host)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode, host)
	}
}

func TestTestAPIAbsentByDefault(t *testing.T) {
	ts, token, _ := setup(t, false)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/_test/fake-url", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestTestAPIFakeURLAndDeeplink(t *testing.T) {
	ts, token, fake := setup(t, true)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/_test/fake-url", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var out map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, fake.URL(), out["url"])

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/_test/deeplink", strings.NewReader(`{"url":"mmauth://callback?MMAUTHTOKEN=x"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", ts.URL)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var e map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	assert.Equal(t, "no_pending_login", e["code"], "no login was started")
}

// TestGracefulShutdownEndsOpenSSEConnectionPromptly guards against a
// regression where cancelling the server's context left the SSE
// (/api/events) handler running forever: Shutdown only waits for active
// connections, it does not cancel their request contexts, so with the UI's
// SSE tab open, Ctrl+C used to hang the process.
//
// The assertion is on the CLIENT side of the stream, not on
// serveWithGracefulShutdown's return: Server.Shutdown closes the listener
// (and so makes ListenAndServe return http.ErrServerClosed) as soon as it is
// called, regardless of whether any active connection ever finishes — so
// "serveWithGracefulShutdown returns quickly" holds even with the bug and
// does not discriminate. What the BaseContext fix actually changes is
// whether the open SSE connection itself gets torn down, which is only
// observable by reading past its still-open body on the client.
func TestGracefulShutdownEndsOpenSSEConnectionPromptly(t *testing.T) {
	t.Setenv("SPK_MM_CLIENT_HOME", t.TempDir())

	// Reserve a free port up front (listen-then-close) so the client below
	// can dial it as soon as the server goroutine starts.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	ctx, cancel := context.WithCancel(context.Background())
	srv, cancelBase, token, cleanup, err := buildBrowserServer(ctx, browserOpts{Port: port})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	done := make(chan error, 1)
	go func() { done <- serveWithGracefulShutdown(ctx, srv, cancelBase) }()

	url := fmt.Sprintf("http://127.0.0.1:%d/api/events?token=%s", port, token)
	var resp *http.Response
	require.Eventually(t, func() bool {
		resp, err = http.Get(url)
		return err == nil
	}, 2*time.Second, 20*time.Millisecond, "server did not start listening in time")
	t.Cleanup(func() { _ = resp.Body.Close() })

	// Confirm the SSE stream is actually open by reading its preamble: the
	// ": ok" comment line plus the blank line that terminates it, both
	// written and flushed on connect.
	r := bufio.NewReader(resp.Body)
	line, err := r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, ": ok\n", line)
	blank, err := r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "\n", blank)

	cancel() // simulate Ctrl+C / SIGTERM while the UI still has the SSE tab open

	// The fix under test: the client must observe end-of-stream (the server
	// finishing the response once its handler's request context is
	// canceled) within a few seconds. Without BaseContext wiring the request
	// context, the handler is still blocked on its ping ticker (up to 25s)
	// or forever, so the client's Read would still be blocked when this
	// deadline fires.
	readDone := make(chan error, 1)
	go func() {
		_, err := r.ReadString('\n')
		readDone <- err
	}()
	select {
	case err := <-readDone:
		assert.ErrorIs(t, err, io.EOF, "client should observe the server ending the SSE response")
	case <-time.After(3 * time.Second):
		t.Fatal("client did not observe end of SSE stream within 3s of shutdown")
	}

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5s with an open SSE connection")
	}
}

func TestTestAPIFakeControlsAndNotifications(t *testing.T) {
	ts, token, fake := setup(t, true)
	post := func(path, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", ts.URL)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}
	resp := post("/api/_test/fake/post", `{"channel_id":"c-offtopic","username":"bob","message":"from test"}`)
	require.Equal(t, 200, resp.StatusCode)
	found := false
	for _, p := range fake.VisiblePosts("c-offtopic") {
		found = found || p.Message == "from test"
	}
	assert.True(t, found)
	assert.Equal(t, 200, post("/api/_test/fake/throttle-file", `{"bytes_per_sec":100000}`).StatusCode)
	assert.Equal(t, 200, post("/api/_test/fake/throttle-file", `{"bytes_per_sec":0}`).StatusCode)
	assert.Equal(t, 200, post("/api/_test/fake/throttle-upload", `{"bytes_per_sec":100000}`).StatusCode)
	assert.Equal(t, 200, post("/api/_test/fake/throttle-upload", `{"bytes_per_sec":0}`).StatusCode)
	assert.Equal(t, 200, post("/api/_test/fake/fail-uploads", `{"n":1}`).StatusCode)
	assert.Equal(t, 200, post("/api/_test/fake/drop", `{"lose":true}`).StatusCode)
	assert.Equal(t, 200, post("/api/_test/fake/revoke", `{}`).StatusCode)
	assert.Equal(t, 0, fake.ActiveSessions())
	assert.Equal(t, 200, post("/api/_test/notification-click", `{"server_id":1,"channel_id":"c-town"}`).StatusCode)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/_test/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var list []api.Notification
	require.NoError(t, json.NewDecoder(r.Body).Decode(&list))
	assert.Empty(t, list)

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/_test/opened-files", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var files []string
	require.NoError(t, json.NewDecoder(r.Body).Decode(&files))
	assert.Equal(t, []string{}, files)
}

// "Show in folder" has no file manager in browser mode: e2e reads the
// requests from the test-API.
func TestTestAPIRevealedFiles(t *testing.T) {
	dl := t.TempDir()
	t.Setenv("SPK_MM_CLIENT_DOWNLOADS", dl)
	ts, token, fake, svc := setupWithService(t, true)
	ctx := context.Background()
	srv, err := svc.AddServer(ctx, fake.URL())
	require.NoError(t, err)
	_, err = svc.LoginWithPassword(ctx, srv.ID, "alice", "secret")
	require.NoError(t, err)
	saved, err := svc.DownloadFile(ctx, srv.ID, "f-spec")
	require.NoError(t, err)
	list, err := svc.Downloads(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	do := func(method, path, body string) *http.Response {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", ts.URL)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}
	require.Equal(t, 200, do(http.MethodPost, "/api/RevealDownload", fmt.Sprintf(`{"id":%d}`, list[0].ID)).StatusCode)
	var files []string
	require.NoError(t, json.NewDecoder(do(http.MethodGet, "/api/_test/revealed-files", "").Body).Decode(&files))
	assert.Equal(t, []string{saved.Path}, files)
}

// Browser mode: the base MediaStreamBase returns through the HTTP transport
// composes, as documented (<base>/<srv>/stream/<id>), into a URL the media
// handler streams.
func TestMediaStreamBaseComposesInBrowserMode(t *testing.T) {
	ts, token, fake, svc := setupWithService(t, false)
	ctx := context.Background()
	srv, err := svc.AddServer(ctx, fake.URL())
	require.NoError(t, err)
	_, err = svc.LoginWithPassword(ctx, srv.ID, "alice", "secret")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		list, err := svc.ListServers(ctx)
		return err == nil && len(list) == 1 && list[0].State == "live"
	}, 10*time.Second, 20*time.Millisecond)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/MediaStreamBase", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL) // the CSRF guard of state-changing methods
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var base string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&base))
	_ = resp.Body.Close()
	assert.Equal(t, "/media", base)

	req, err = http.NewRequest(http.MethodGet, fmt.Sprintf("%s%s/%d/stream/%s", ts.URL, base, srv.ID, "f-clip-webm"), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Range", "bytes=0-99")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Equal(t, "video/webm", resp.Header.Get("Content-Type"))
	assert.Len(t, body, 100)
}

// The previous run's attachment spools are swept after startup, not
// during it; the staged media kind is wired to the service.
func TestBrowserModeSweepsOldSpoolsAndServesStaged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SPK_MM_CLIENT_HOME", home)
	old := filepath.Join(home, "tmp", "attach-old")
	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o700))
	require.NoError(t, os.WriteFile(old, []byte("x"), 0o600))

	srv, cancelBase, token, cleanup, err := buildBrowserServer(context.Background(), browserOpts{})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	t.Cleanup(cancelBase)
	require.Eventually(t, func() bool {
		_, err := os.Stat(old)
		return errors.Is(err, os.ErrNotExist)
	}, 5*time.Second, 10*time.Millisecond, "old spool not swept")

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/media/1/staged/abc", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a known kind: no such attachment (not 400)")
}
