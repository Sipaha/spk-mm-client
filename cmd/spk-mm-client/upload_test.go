package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/api"
	"github.com/spk/spk-mm-client/internal/events"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/store"
)

const uploadMax = 1000 // the fake server's MaxFileSize in these tests

// setupUploads is browser mode with attachments on, signed in to a fake
// server that takes files up to uploadMax bytes.
func setupUploads(t *testing.T) (ts *httptest.Server, token string, svc *api.Service, srv int64, fake *mmfake.Server) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	em := events.NewEmitter()
	fake = mmfake.Start(mmfake.Options{MaxFileSize: uploadMax})
	t.Cleanup(fake.Close)
	svc = api.NewService(st, em, func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
	svc.EnableAttachments(filepath.Join(t.TempDir(), "tmp"))
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(svc.Close)
	dist := fstest.MapFS{"index.html": {Data: []byte("<html><head></head><body></body></html>")}}
	h, token := newBrowserHandler(svc, em, dist, nil, false, &api.RecordingNotifier{}, nil, &api.RecordingOpener{}, &api.RecordingOpener{})
	ts = httptest.NewServer(h)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	s, err := svc.AddServer(ctx, fake.URL())
	require.NoError(t, err)
	_, err = svc.LoginWithPassword(ctx, s.ID, "alice", "secret")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		list, err := svc.ListServers(ctx)
		if err != nil || len(list) != 1 || list[0].State != "live" {
			return false
		}
		_, err = svc.GetChannel(ctx, s.ID, "c-offtopic")
		return err == nil
	}, 10*time.Second, 20*time.Millisecond)
	return ts, token, svc, s.ID, fake
}

func upload(t *testing.T, ts *httptest.Server, path string, body io.Reader, edit func(*http.Request)) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, body)
	require.NoError(t, err)
	req.Header.Set("Origin", ts.URL)
	req.Header.Set("Content-Type", "application/octet-stream")
	if edit != nil {
		edit(req)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func TestBrowserUploadAttachesTheRawBody(t *testing.T) {
	ts, token, svc, srv, _ := setupUploads(t)
	path := fmt.Sprintf("/api/attachments/%d/c-offtopic?name=%s&mime=%s", srv, url.QueryEscape("отчёт.txt"), url.QueryEscape("text/plain"))
	resp, out := upload(t, ts, path, strings.NewReader("hello"), bearer(token))
	require.Equal(t, http.StatusOK, resp.StatusCode, "%v", out)
	assert.Equal(t, "отчёт.txt", out["name"])
	assert.Equal(t, "text/plain", out["mime"])
	assert.EqualValues(t, 5, out["size"])
	list, err := svc.Attachments(context.Background(), srv, "c-offtopic", "")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, out["id"], list[0].ID)
}

// Task 4: ?root= routes the upload to a thread's reply composer instead of
// the channel's — the two lists never mix (attach.Store's (channel, root)
// key), and the root must be one the panel already opened (ThreadHeld),
// like every other attachment source.
func TestBrowserUploadWithRootGoesToTheThreadsComposer(t *testing.T) {
	ts, token, svc, srv, fake := setupUploads(t)
	ctx := context.Background()
	require.Eventually(t, func() bool {
		_, err := svc.GetChannel(ctx, srv, "c-town")
		return err == nil
	}, 10*time.Second, 20*time.Millisecond, "c-town never loaded")
	root := fake.SeedThread("c-town", "alice", 1)

	// Before the thread is opened, the root is not held: refused.
	path := fmt.Sprintf("/api/attachments/%d/c-town?root=%s&name=a.txt", srv, root)
	resp, out := upload(t, ts, path, strings.NewReader("x"), bearer(token))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%v", out)
	assert.Equal(t, "no_post", out["code"])

	_, err := svc.OpenThread(ctx, srv, "c-town", root)
	require.NoError(t, err)

	resp, out = upload(t, ts, path, strings.NewReader("reply file"), bearer(token))
	require.Equal(t, http.StatusOK, resp.StatusCode, "%v", out)
	assert.Equal(t, "a.txt", out["name"])

	threadList, err := svc.Attachments(ctx, srv, "c-town", root)
	require.NoError(t, err)
	require.Len(t, threadList, 1, "the thread's own composer has it")
	assert.Equal(t, out["id"], threadList[0].ID)

	channelList, err := svc.Attachments(ctx, srv, "c-town", "")
	require.NoError(t, err)
	assert.Empty(t, channelList, "the channel's own composer does not")
}

func TestBrowserUploadNeedsTokenOriginAndLoopbackHost(t *testing.T) {
	ts, token, svc, srv, _ := setupUploads(t)
	path := fmt.Sprintf("/api/attachments/%d/c-offtopic?name=a.txt", srv)

	resp, _ := upload(t, ts, path, strings.NewReader("x"), nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "no bearer token")
	resp, _ = upload(t, ts, path, strings.NewReader("x"), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", "http://evil.example")
	})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "cross-origin")
	resp, _ = upload(t, ts, path, strings.NewReader("x"), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Del("Origin")
	})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "no Origin/Referer")
	resp, _ = upload(t, ts, path, strings.NewReader("x"), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Host = "evil.example"
		r.Header.Set("Origin", "http://evil.example")
	})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "DNS rebinding")
	resp, _ = upload(t, ts, path+"&token="+token, strings.NewReader("x"), nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "no query token on a POST")

	list, _ := svc.Attachments(context.Background(), srv, "c-offtopic", "")
	assert.Empty(t, list)
}

func TestBrowserUploadOverMaxFileSizeIs413(t *testing.T) {
	ts, token, svc, srv, _ := setupUploads(t)
	path := fmt.Sprintf("/api/attachments/%d/c-offtopic?name=big.bin", srv)
	big := strings.Repeat("x", uploadMax+1)

	resp, out := upload(t, ts, path, strings.NewReader(big), bearer(token))
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "declared length")
	assert.Equal(t, "too_large", out["code"])

	// No Content-Length (chunked): cut off while reading.
	resp, out = upload(t, ts, path, io.MultiReader(strings.NewReader(big)), bearer(token))
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "streamed")
	assert.Equal(t, "too_large", out["code"])

	resp, _ = upload(t, ts, path, strings.NewReader(big[:uploadMax]), bearer(token))
	assert.Equal(t, http.StatusOK, resp.StatusCode, "exactly MaxFileSize fits")
	list, _ := svc.Attachments(context.Background(), srv, "c-offtopic", "")
	assert.Len(t, list, 1)
}

func TestBrowserUploadNameIsCleaned(t *testing.T) {
	ts, token, _, srv, _ := setupUploads(t)
	for raw, want := range map[string]string{
		"../../etc/passwd":                "passwd",
		`C:\Users\me\report.pdf`:          "report.pdf",
		"pa\x07ss\u202ewd\u200f.txt\n":    "passwd.txt",
		"  \x00 ":                         "attachment",
		"":                                "attachment",
		strings.Repeat("я", 300) + ".png": strings.Repeat("я", 200-len(".png")) + ".png",
	} {
		path := fmt.Sprintf("/api/attachments/%d/c-offtopic?name=%s", srv, url.QueryEscape(raw))
		resp, out := upload(t, ts, path, strings.NewReader("x"), bearer(token))
		require.Equal(t, http.StatusOK, resp.StatusCode, "%q: %v", raw, out)
		assert.Equal(t, want, out["name"], "%q", raw)
		id, _ := out["id"].(string)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/RemoveAttachment", strings.NewReader(fmt.Sprintf(`{"id":%d,"attachment_id":%q}`, srv, id)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", ts.URL)
		r, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = r.Body.Close()
	}
}

func TestBrowserUploadErrors(t *testing.T) {
	ts, token, _, srv, _ := setupUploads(t)
	resp, out := upload(t, ts, "/api/attachments/x/c-offtopic?name=a", strings.NewReader("x"), bearer(token))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "not_found", out["code"])
	resp, out = upload(t, ts, fmt.Sprintf("/api/attachments/%d/c-nope?name=a", srv), strings.NewReader("x"), bearer(token))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "no_channel", out["code"])
	resp, out = upload(t, ts, fmt.Sprintf("/api/attachments/%d/c-offtopic?name=a", srv), strings.NewReader(""), bearer(token))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "empty_file", out["code"])
}
