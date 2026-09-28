package mmsync

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
	"github.com/spk/spk-mm-client/internal/state"
	"github.com/spk/spk-mm-client/internal/store"
)

func TestMergeDeltaNewerWins(t *testing.T) {
	k := func(key string) store.CacheKey { return store.CacheKey{Kind: "posts", Key: key} }
	e := func(key, v string) store.CacheEntry {
		return store.CacheEntry{Kind: "posts", Key: key, Data: []byte(v)}
	}
	put, del := mergeDelta(
		[]store.CacheEntry{e("a", "old"), e("b", "old"), e("keep", "old")}, []store.CacheKey{k("c"), k("gone")},
		[]store.CacheEntry{e("a", "new"), e("c", "new")}, []store.CacheKey{k("b")},
	)
	assert.ElementsMatch(t, []store.CacheEntry{e("a", "new"), e("c", "new"), e("keep", "old")}, put,
		"newer put beats older put; newer put beats older del")
	assert.ElementsMatch(t, []store.CacheKey{k("b"), k("gone")}, del, "newer del beats older put")
}

func openStore(t *testing.T) (*store.Store, store.Server) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	srv, err := st.AddServer(context.Background(), store.Server{URL: "https://mm.example", Name: "X", Token: "tok"})
	require.NoError(t, err)
	return st, srv
}

func TestFailedFlushIsRetriedWithTheNextOne(t *testing.T) {
	ctx := context.Background()
	broken, srv := openStore(t)
	require.NoError(t, broken.Close()) // every SaveCache now fails
	w := NewWorker(Config{Store: broken}, srv)
	w.st.SetUsers([]model.User{{ID: "u2", Username: "bob"}})
	w.st.SetLiveAt(42)
	w.flush(ctx)

	good, srv2 := openStore(t)
	require.Equal(t, srv.ID, srv2.ID)
	w.cfg.Store = good
	w.st.SetLiveAt(43)
	w.flush(ctx)
	entries, err := good.LoadCache(ctx, srv.ID)
	require.NoError(t, err)
	got := map[string]string{}
	for _, e := range entries {
		got[e.Kind+"/"+e.Key] = string(e.Data)
	}
	assert.Contains(t, got, "user/u2", "the failed flush's delta is not lost")
	assert.Equal(t, "43", got["live/at"], "the newer value wins")
	assert.Empty(t, w.unsavedPut)
	assert.Empty(t, w.unsavedDel)
}

func TestSessionEndDrainsBufferedEvents(t *testing.T) {
	var notes []state.NotifyCandidate
	w := NewWorker(Config{Hooks: Hooks{Notify: func(_ int64, c state.NotifyCandidate) { notes = append(notes, c) }}},
		store.Server{ID: 1, URL: "https://mm.example"})
	w.st.Bootstrap(state.Bootstrap{
		Me:       model.User{ID: "u1", Username: "alice"},
		Config:   state.Config{CollapsedThreads: "disabled"},
		Teams:    []model.Team{{ID: "t1", Name: "team"}},
		Channels: []model.Channel{{ID: "town", TeamID: "t1", Type: model.ChannelOpen, DisplayName: "Town", CreateAt: 1}},
		Members:  []model.ChannelMember{{ChannelID: "town", UserID: "u1", NotifyProps: map[string]string{"desktop": "default", "mark_unread": "all"}}},
	})
	w.st.SetWindow("town", nil, true, 1, 0)
	w.st.ClearGuard()

	pb, _ := json.Marshal(model.Post{ID: "p1", ChannelID: "town", UserID: "u2", Message: "buffered", CreateAt: 5000, UpdateAt: 5000})
	db, _ := json.Marshal(map[string]any{"post": string(pb), "channel_type": "O", "sender_name": "@bob"})
	events := make(chan ws.Event, 2)
	events <- ws.Event{Type: "posted", Data: db, Broadcast: ws.Broadcast{ChannelID: "town"}}
	events <- ws.Event{Type: "hello", Reset: true}
	close(events)

	w.drain(events)
	_, ok := w.st.FindPost("p1")
	assert.True(t, ok, "an event counted by Resume must be applied, not dropped")
	assert.Len(t, notes, 1)
	assert.True(t, w.needResync, "a reset hello seen while draining forces a resync on the next session")
}
