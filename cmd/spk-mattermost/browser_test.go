package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/store"
)

func setup(t *testing.T, testAPI bool) (*httptest.Server, string, *mmfake.Server) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	em := events.NewEmitter()
	fake := mmfake.Start(mmfake.Options{})
	t.Cleanup(fake.Close)
	svc := api.NewService(st, em, func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
	dist := fstest.MapFS{"index.html": {Data: []byte("<html><head></head><body></body></html>")}}
	h, token := newBrowserHandler(svc, em, dist, fake, testAPI)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, token, fake
}

func TestIndexCarriesTokenAndIsNotCached(t *testing.T) {
	ts, token, _ := setup(t, false)
	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), `<meta name="spk-mattermost-api-token" content="`+token+`">`)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
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
	t.Setenv("SPK_MATTERMOST_HOME", t.TempDir())

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
