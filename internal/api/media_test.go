package api

import (
	"context"
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
