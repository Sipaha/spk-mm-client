package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheSaveLoadDeleteClear(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	srv, err := s.AddServer(ctx, Server{URL: "https://a", Name: "A"})
	require.NoError(t, err)
	require.NoError(t, s.SaveCache(ctx, srv.ID, []CacheEntry{
		{Kind: "meta", Key: "server", Data: []byte(`{"v":1}`)},
		{Kind: "chan", Key: "c1", Data: []byte(`1`)},
		{Kind: "chan", Key: "c2", Data: []byte(`2`)},
	}, nil))
	require.NoError(t, s.SaveCache(ctx, srv.ID,
		[]CacheEntry{{Kind: "chan", Key: "c1", Data: []byte(`11`)}},
		[]CacheKey{{Kind: "chan", Key: "c2"}}))
	got, err := s.LoadCache(ctx, srv.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []CacheEntry{
		{Kind: "meta", Key: "server", Data: []byte(`{"v":1}`)},
		{Kind: "chan", Key: "c1", Data: []byte(`11`)},
	}, got)
	require.NoError(t, s.ClearCache(ctx, srv.ID))
	got, err = s.LoadCache(ctx, srv.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
	_, err = s.GetServer(ctx, srv.ID)
	assert.NoError(t, err, "clearing the cache keeps the server and its session")
}

func TestCacheGoesWithTheServer(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	srv, _ := s.AddServer(ctx, Server{URL: "https://a", Name: "A"})
	require.NoError(t, s.SaveCache(ctx, srv.ID, []CacheEntry{{Kind: "chan", Key: "c1", Data: []byte(`1`)}}, nil))
	require.NoError(t, s.DeleteServer(ctx, srv.ID))
	var n int
	require.NoError(t, s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cache_entries`).Scan(&n))
	assert.Zero(t, n)
}
