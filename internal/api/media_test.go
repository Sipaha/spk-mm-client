package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/media"
)

func TestMediaOriginStreamsThroughTheWorker(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "never live")

	resp, err := f.svc.Get(ctx, id, "/api/v4/users/u-bob/image", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	_, err = f.svc.Get(ctx, 999, "/api/v4/users/u-bob/image", nil)
	assert.ErrorIs(t, err, media.ErrNoServer)

	// Custom emoji are known once the first bootstrap has read the config.
	f.eventually(func() bool { eid, err := f.svc.EmojiID(ctx, id, "partyparrot"); return err == nil && eid == "e-parrot" }, "custom emoji name not resolved")

	fake.RevokeAll()
	fake.DropConnections(true)
	f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "session never expired")
	_, err = f.svc.Get(ctx, id, "/api/v4/users/u-bob/image", nil)
	assert.ErrorIs(t, err, media.ErrNoServer, "a dead session does not fetch")
}

func TestMediaCacheThroughTheService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.server(id).State == "live" }, "never live")
	mc, err := media.New(media.Options{Dir: t.TempDir(), Origin: f.svc})
	require.NoError(t, err)
	ts := httptest.NewServer(mc)
	defer ts.Close()
	for i := 0; i < 2; i++ {
		resp, err := http.Get(fmt.Sprintf("%s/media/%d/thumb/f-build", ts.URL, id))
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "image/jpeg", resp.Header.Get("Content-Type"))
	}
	assert.Equal(t, 1, fake.Hits("GET", "/api/v4/files/f-build/thumbnail"))
}

// A worker that is not live hands out no fetch: its request would fail
// with a network error (502) the UI remembers; ErrNoServer (404) is not
// remembered by the cache, and the UI retries when the server goes live.
func TestMediaOriginFetchesOnlyWhileLive(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "never live")
	f.eventually(func() bool { eid, err := f.svc.EmojiID(ctx, id, "partyparrot"); return err == nil && eid == "e-parrot" }, "custom emoji name not resolved")

	fake.SetDown(true)
	fake.DropConnections(false)
	f.eventually(func() bool { return f.server(id).State == "reconnecting" }, "never lost the connection")
	hits := fake.Hits("GET", "/api/v4/users/u-bob/image")
	_, err := f.svc.Get(ctx, id, "/api/v4/users/u-bob/image", nil)
	assert.ErrorIs(t, err, media.ErrNoServer, "not live: no fetch")
	_, err = f.svc.EmojiID(ctx, id, "not_known_yet")
	assert.ErrorIs(t, err, media.ErrNoServer)
	eid, err := f.svc.EmojiID(ctx, id, "partyparrot")
	require.NoError(t, err, "a name already known resolves without the network: its picture may be on disk")
	assert.Equal(t, "e-parrot", eid)
	assert.Equal(t, hits, fake.Hits("GET", "/api/v4/users/u-bob/image"))

	fake.SetDown(false)
	f.eventually(func() bool { return f.server(id).State == "live" }, "never live again")
	resp, err := f.svc.Get(ctx, id, "/api/v4/users/u-bob/image", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
}

// A 401 on a picture or an emoji name is the session dying: it goes the
// worker's re-auth way like every other request.
func TestMediaUnauthorizedAsksForSignIn(t *testing.T) {
	for _, tc := range []struct{ name, part string }{{"picture", "/users/u-bob/image"}, {"emoji", "/emoji/name/"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newChatFixture(t)
			fake := startFake(t)
			id := f.signIn(fake, "alice")
			ctx := context.Background()
			f.eventually(func() bool { return f.server(id).State == "live" }, "never live")
			fake.SetFailure(tc.part, http.StatusUnauthorized)
			if tc.name == "picture" {
				_, err := f.svc.Get(ctx, id, "/api/v4/users/u-bob/image", nil)
				require.Error(t, err)
			} else {
				_, err := f.svc.EmojiID(ctx, id, "not_in_the_list")
				require.Error(t, err)
			}
			f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "a media 401 did not ask for a new sign-in")
		})
	}
}

func TestMediaStreamBase(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	base, err := f.svc.MediaStreamBase(ctx)
	require.NoError(t, err)
	assert.Equal(t, "/media", base, "browser mode: the page's own /media/")

	f.svc.SetMediaStreamBase(func() (string, error) { return "http://127.0.0.1:1/tok", nil })
	base, err = f.svc.MediaStreamBase(ctx)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:1/tok", base)

	f.svc.SetMediaStreamBase(func() (string, error) { return "", errors.New("no socket") })
	_, err = f.svc.MediaStreamBase(ctx)
	var ce *CodedError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CodeInternal, ce.Code)
}

// The desktop path end to end: the loopback server streams a seeded clip
// of the fake through the worker, and a 401 there asks for a new sign-in.
func TestMediaStreamThroughTheLoopbackServer(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.server(id).State == "live" }, "never live")
	lb := media.NewLoopback(media.NewStreamer(f.svc))
	defer lb.Close()
	f.svc.SetMediaStreamBase(lb.Base)
	base, err := f.svc.MediaStreamBase(context.Background())
	require.NoError(t, err)

	get := func(file, rng string) *http.Response {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/stream/%d/%s", base, id, file), nil)
		require.NoError(t, err)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp
	}
	resp := get("f-clip-webm", "bytes=0-99")
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Equal(t, "video/webm", resp.Header.Get("Content-Type"))
	assert.Regexp(t, `^bytes 0-99/\d+$`, resp.Header.Get("Content-Range"))
	assert.Equal(t, "video/mp4", get("f-clip-mp4", "").Header.Get("Content-Type"))
	assert.Equal(t, "audio/ogg", get("f-tone-ogg", "").Header.Get("Content-Type"))
	assert.Equal(t, http.StatusUnsupportedMediaType, get("f-spec", "").StatusCode)

	fake.SetFailure("/files/f-clip-mp4", http.StatusUnauthorized)
	assert.Equal(t, http.StatusForbidden, get("f-clip-mp4", "bytes=0-9").StatusCode)
	f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "a stream 401 did not ask for a new sign-in")
	assert.Equal(t, http.StatusNotFound, get("f-clip-webm", "").StatusCode, "not live: no stream")
}
