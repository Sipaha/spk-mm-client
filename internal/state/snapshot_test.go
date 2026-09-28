package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/store"
)

func keysOf(put []store.CacheEntry) []string {
	var out []string
	for _, e := range put {
		out = append(out, e.Kind+"/"+e.Key)
	}
	return out
}

func TestSnapshotRoundTrip(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 100), mkPost("b", "off", "u2", 200)}, false, 5, 0)
	s.SetDraft("off", "half-written")
	s.SetActive("off")
	s.SetLiveAt(900)
	put, _ := s.TakeSnapshot()

	r := New(fixedNow)
	require.NoError(t, r.Restore(put))
	assert.True(t, r.HasData())
	assert.Equal(t, s.Sidebar("t1"), r.Sidebar("t1"))
	v, ok := r.ChannelView("off")
	require.True(t, ok)
	assert.Len(t, v.Posts, 2)
	assert.Equal(t, "half-written", v.Draft)
	assert.True(t, v.Syncing, "restored windows must be caught up")
	assert.Equal(t, "b", v.GapAfter)
	it, ok := r.SyncItemFor("off")
	require.True(t, ok)
	assert.Equal(t, int64(900), it.SyncedAt, "fresh at snapshot time → complete up to the last live moment")
	assert.Equal(t, "off", r.Sidebar("t1").SelectedChannelID)
	assert.Equal(t, "bob", findItem(r.Sidebar("t1"), "dm2").Name, "users restored")
}

func TestTakeSnapshotOnlyWritesWhatChanged(t *testing.T) {
	s := newFixture()
	s.TakeSnapshot()
	put, del := s.TakeSnapshot()
	assert.Empty(t, put)
	assert.Empty(t, del)
	s.ClearGuard()
	s.SetWindow("off", nil, true, 5, 0)
	s.TakeSnapshot()
	s.ApplyEvent(postedEv(mkPost("p", "off", "u2", 5000)))
	put, _ = s.TakeSnapshot()
	assert.ElementsMatch(t, []string{"chan/off", "posts/off"}, keysOf(put))
	s.SetLiveAt(1)
	put, _ = s.TakeSnapshot()
	assert.Equal(t, []string{"live/at"}, keysOf(put))
}

func TestLeftChannelIsDeletedFromSnapshot(t *testing.T) {
	s := newFixture()
	s.TakeSnapshot()
	b := fixture()
	b.Members = b.Members[1:] // left town
	s.Bootstrap(b)
	_, del := s.TakeSnapshot()
	assert.ElementsMatch(t, []store.CacheKey{{Kind: "chan", Key: "town"}, {Kind: "posts", Key: "town"}}, del)
}

func TestRestoreRejectsMissingOrForeignVersion(t *testing.T) {
	assert.ErrorIs(t, New(fixedNow).Restore(nil), ErrNoSnapshot)
	meta, _ := json.Marshal(map[string]any{"version": 99})
	err := New(fixedNow).Restore([]store.CacheEntry{{Kind: "meta", Key: "server", Data: meta}})
	assert.ErrorIs(t, err, ErrSnapshotVersion)
}

func TestResetWindowsDropsWindowsFromSnapshot(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 100)}, false, 5, 0)
	s.TakeSnapshot()
	s.ResetWindows()
	it, ok := s.SyncItemFor("off")
	require.True(t, ok)
	assert.False(t, it.Loaded, "the window is refetched from scratch")
	put, del := s.TakeSnapshot()
	assert.NotContains(t, keysOf(put), "posts/off")
	assert.Contains(t, del, store.CacheKey{Kind: "posts", Key: "off"}, "a stale-kind window must not come back after restart")
}
