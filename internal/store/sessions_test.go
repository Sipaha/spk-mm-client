package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplaceSessionKeepsOnlyItsOwnAccountSnapshot(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	srv, err := st.AddServer(ctx, Server{URL: "https://a"})
	require.NoError(t, err)
	require.NoError(t, st.SetSession(ctx, srv.ID, "old", "alice-id", "alice"))
	data := []CacheEntry{{Kind: "meta", Key: "server", Data: []byte(`{"version":1}`)}}
	require.NoError(t, st.SaveCache(ctx, srv.ID, data, nil))
	require.NoError(t, st.ReplaceSession(ctx, srv.ID, "same-account", "alice-id", "alice"))
	got, err := st.LoadCache(ctx, srv.ID)
	require.NoError(t, err)
	assert.Equal(t, data, got)
	require.NoError(t, st.ReplaceSession(ctx, srv.ID, "other-account", "bob-id", "bob"))
	got, err = st.LoadCache(ctx, srv.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
	saved, err := st.GetServer(ctx, srv.ID)
	require.NoError(t, err)
	assert.Equal(t, "bob-id", saved.UserID)
	assert.Equal(t, "other-account", saved.Token)
	assert.ErrorIs(t, st.ReplaceSession(ctx, 999, "tok", "u", "name"), ErrNotFound)
}

func TestSessionChangeRollsBackWhenCacheRemovalFails(t *testing.T) {
	for _, end := range []bool{false, true} {
		t.Run(map[bool]string{false: "replace", true: "logout"}[end], func(t *testing.T) {
			st := openTest(t)
			ctx := context.Background()
			srv, err := st.AddServer(ctx, Server{URL: "https://a"})
			require.NoError(t, err)
			require.NoError(t, st.SetSession(ctx, srv.ID, "old", "alice-id", "alice"))
			data := []CacheEntry{{Kind: "meta", Key: "server", Data: []byte(`{"version":1}`)}}
			require.NoError(t, st.SaveCache(ctx, srv.ID, data, nil))
			_, err = st.db.ExecContext(ctx, `CREATE TRIGGER refuse_cache BEFORE DELETE ON cache_entries BEGIN SELECT RAISE(ABORT, 'injected'); END`)
			require.NoError(t, err)
			if end {
				err = st.EndSession(ctx, srv.ID)
			} else {
				err = st.ReplaceSession(ctx, srv.ID, "new", "bob-id", "bob")
			}
			require.Error(t, err)
			saved, err := st.GetServer(ctx, srv.ID)
			require.NoError(t, err)
			assert.Equal(t, "old", saved.Token)
			assert.Equal(t, "alice-id", saved.UserID)
			got, err := st.LoadCache(ctx, srv.ID)
			require.NoError(t, err)
			assert.Equal(t, data, got)
		})
	}
}

func TestEndSessionClearsCredentialsAndSnapshot(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	srv, err := st.AddServer(ctx, Server{URL: "https://a"})
	require.NoError(t, err)
	require.NoError(t, st.SetSession(ctx, srv.ID, "old", "alice-id", "alice"))
	require.NoError(t, st.SaveCache(ctx, srv.ID, []CacheEntry{{Kind: "meta", Key: "server", Data: []byte(`1`)}}, nil))
	require.NoError(t, st.EndSession(ctx, srv.ID))
	saved, err := st.GetServer(ctx, srv.ID)
	require.NoError(t, err)
	assert.False(t, saved.SignedIn())
	assert.Empty(t, saved.UserID)
	got, err := st.LoadCache(ctx, srv.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.ErrorIs(t, st.EndSession(ctx, 999), ErrNotFound)
}
