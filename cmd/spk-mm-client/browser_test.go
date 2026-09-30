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
	"github.com/spk/spk-mm-client/internal/mm/model"
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
	for _, body := range []string{`{"ms":-1}`, `{"ms":10001}`, `{"ms":"bad"}`} {
		r := post("/api/_test/fake/post-latency", body)
		assert.Equal(t, http.StatusBadRequest, r.StatusCode)
		r.Body.Close()
	}
	for _, body := range []string{`{"ms":20}`, `{"ms":0}`} {
		r := post("/api/_test/fake/post-latency", body)
		assert.Equal(t, http.StatusOK, r.StatusCode)
		r.Body.Close()
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
	fakeClientConfig := func() map[string]string {
		resp, err := http.Get(fake.URL() + "/api/v4/config/client?format=old")
		require.NoError(t, err)
		defer resp.Body.Close()
		var cfg map[string]string
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&cfg))
		return cfg
	}
	assert.Equal(t, 200, post("/api/_test/fake/max-file-size", `{"bytes":10}`).StatusCode)
	assert.Equal(t, "10", fakeClientConfig()["MaxFileSize"])
	assert.Equal(t, 200, post("/api/_test/fake/max-file-size", `{"bytes":0}`).StatusCode)
	assert.Equal(t, 200, post("/api/_test/fake/file-attachments-enabled", `{"enabled":false}`).StatusCode)
	assert.Equal(t, "false", fakeClientConfig()["EnableFileAttachments"])
	assert.Equal(t, 200, post("/api/_test/fake/file-attachments-enabled", `{"enabled":true}`).StatusCode)
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

func TestTestAPIThreadControls(t *testing.T) {
	ts, token, fake := setup(t, true)
	post := func(path, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", ts.URL)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}
	get := func(path string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}

	resp := post("/api/_test/fake/thread", `{"channel_id":"c-town","username":"alice","replies":3}`)
	require.Equal(t, 200, resp.StatusCode)
	var out struct {
		RootID string `json:"root_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.RootID)
	assert.Len(t, fake.VisiblePosts("c-town"), 150+1+3, "seeded town-square posts + thread root + 3 replies")

	// fake/post with an explicit root_id posts a reply, not a new root.
	resp = post("/api/_test/fake/post", `{"channel_id":"c-town","username":"carol","message":"another reply","root_id":"`+out.RootID+`"}`)
	require.Equal(t, 200, resp.StatusCode)

	resp = post("/api/_test/fake/edit", `{"post_id":"`+out.RootID+`","message":"edited root"}`)
	assert.Equal(t, 200, resp.StatusCode)

	assert.Equal(t, 200, post("/api/_test/fake/crt", `{"mode":"always_on"}`).StatusCode)
	cfgResp, err := http.Get(fake.URL() + "/api/v4/config/client?format=old")
	require.NoError(t, err)
	defer cfgResp.Body.Close()
	var cfg map[string]string
	require.NoError(t, json.NewDecoder(cfgResp.Body).Decode(&cfg))
	assert.Equal(t, "always_on", cfg["CollapsedThreads"])

	resp = post("/api/_test/fake/delete", `{"post_id":"`+out.RootID+`"}`)
	assert.Equal(t, 200, resp.StatusCode)

	reads := get("/api/_test/fake/thread-reads")
	require.Equal(t, 200, reads.StatusCode)
	var readList []map[string]any
	require.NoError(t, json.NewDecoder(reads.Body).Decode(&readList))
	assert.Empty(t, readList, "no PUT read calls were made in this test")
}

// fake/search-calls lists every search the fake answered (e2e: which pages
// the client asked for, and with which terms).
func TestTestAPISearchCalls(t *testing.T) {
	ts, token, fake := setup(t, true)
	login, err := http.Post(fake.URL()+"/api/v4/users/login", "application/json", strings.NewReader(`{"login_id":"alice","password":"secret"}`))
	require.NoError(t, err)
	login.Body.Close()
	req, _ := http.NewRequest(http.MethodPost, fake.URL()+"/api/v4/teams/t-fake/posts/search",
		strings.NewReader(`{"terms":"message from:bob","page":1,"per_page":20}`))
	req.Header.Set("Authorization", "Bearer "+login.Header.Get("Token"))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/_test/fake/search-calls", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var calls []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&calls))
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]any{"team_id": "t-fake", "user_id": "u-alice", "terms": "message from:bob",
		"is_or_search": false, "page": float64(1), "per_page": float64(20)}, calls[0])
}

// fake/webhook posts as an incoming webhook: from_webhook plus the given
// overrides (e2e: a GitLab-like post with its own name and icon).
func TestTestAPIWebhookPost(t *testing.T) {
	ts, token, fake := setup(t, true)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/_test/fake/webhook", strings.NewReader(
		`{"channel_id":"c-town","username":"bob","message":"pipeline passed","override_username":"GitLab",`+
			`"override_icon_url":"/static/images/webhook-icon.png","override_icon_emoji":":tada:"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	posts := fake.VisiblePosts("c-town")
	p := posts[len(posts)-1]
	assert.Equal(t, out.ID, p.ID)
	assert.True(t, bool(p.Props.FromWebhook))
	assert.Equal(t, "GitLab", string(p.Props.OverrideUsername))
	assert.Equal(t, "/static/images/webhook-icon.png", p.Props.OverrideIconURL)
	assert.Equal(t, ":tada:", p.Props.OverrideIconEmoji)
}

// fake/webhook also carries message_attachments straight through to
// Props.Attachments (density-brief 2026-09-29: the screenshot fixture for a
// Jenkins-style CI notification card, posted live via this test-API route
// rather than baked into the default seed — c-town's seed shape and every
// test that counts its posts/unreads must stay exactly as before).
func TestTestAPIWebhookPostWithAttachments(t *testing.T) {
	ts, token, fake := setup(t, true)
	body := `{"channel_id":"c-town","username":"bob","override_username":"jenkins","attachments":[` +
		`{"color":"#00c100","title":"sample-app - build completed - 006","text":"Branch: **master**",` +
		`"fields":[{"title":"Changes","value":"- [abc1234](https://example.com/commit/abc1234) fix","short":false}],` +
		`"footer":"build #006"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/_test/fake/webhook", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	posts := fake.VisiblePosts("c-town")
	p := posts[len(posts)-1]
	require.Len(t, p.Props.Attachments, 1)
	a := p.Props.Attachments[0]
	assert.Equal(t, "#00c100", a.Color)
	assert.Equal(t, "sample-app - build completed - 006", a.Title)
	assert.Empty(t, a.Pretext, "pretext-less, per the density brief's fixture spec")
	require.Len(t, a.Fields, 1)
	assert.Equal(t, "Changes", a.Fields[0].Title)
	assert.Equal(t, model.Flag(false), a.Fields[0].Short)
	assert.Equal(t, "build #006", a.Footer)
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

// The test API's notification-click passes a reply's root_id on to the UI
// (open_channel), like a click on a desktop notification.
func TestTestAPINotificationClickCarriesTheRoot(t *testing.T) {
	ts, token, _ := setup(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events?token="+token, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	line, err := r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, ": ok\n", line)

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/_test/notification-click", strings.NewReader(`{"server_id":1,"channel_id":"c-town","root_id":"root1"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", ts.URL)
	click, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = click.Body.Close()
	require.Equal(t, 200, click.StatusCode)
	for {
		line, err := r.ReadString('\n')
		require.NoError(t, err, "no open_channel event")
		if strings.Contains(line, `"open_channel"`) || strings.Contains(line, "channel_id") {
			if strings.Contains(line, `"c-town"`) {
				assert.Contains(t, line, `"root_id":"root1"`)
				return
			}
		}
	}
}
