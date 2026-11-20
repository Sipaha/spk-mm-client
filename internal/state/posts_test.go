package state

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

func mkPost(id, ch, user string, at int64) model.Post {
	return model.Post{ID: id, ChannelID: ch, UserID: user, Message: "msg " + id, CreateAt: at, UpdateAt: at}
}

func postedEv(p model.Post, mentions ...string) ws.Event {
	pb, _ := json.Marshal(p)
	d := map[string]any{"post": string(pb), "channel_type": "O", "sender_name": "@" + p.UserID}
	if len(mentions) > 0 {
		mb, _ := json.Marshal(mentions)
		d["mentions"] = string(mb)
	}
	db, _ := json.Marshal(d)
	return ws.Event{Type: "posted", Data: db, Broadcast: ws.Broadcast{ChannelID: p.ChannelID}}
}

func postEv(typ string, p model.Post) ws.Event {
	pb, _ := json.Marshal(p)
	db, _ := json.Marshal(map[string]any{"post": string(pb)})
	return ws.Event{Type: typ, Data: db, Broadcast: ws.Broadcast{ChannelID: p.ChannelID}}
}

func windowIDs(s *Server, ch string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, p := range s.chans[ch].Win.Posts {
		out = append(out, p.ID)
	}
	return out
}

func TestSetWindowTrimsAndKeepsNewerWSPosts(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000)}, true, 5, 0)
	s.ApplyEvent(postedEv(mkPost("ws", "off", "u2", 3000)))
	// a slower page fetch finishing after the WS post must not drop it
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000), mkPost("b", "off", "u2", 2000)}, true, 6, 0)
	assert.Equal(t, []string{"a", "b", "ws"}, windowIDs(s, "off"))

	var page []model.Post
	for i := 0; i < WindowSize+5; i++ {
		page = append(page, mkPost(fmt.Sprint("p", i), "town", "u2", int64(10+i)))
	}
	s.SetWindow("town", page, true, 7, 0)
	s.mu.Lock()
	w := s.chans["town"].Win
	s.mu.Unlock()
	assert.Len(t, w.Posts, WindowSize)
	assert.False(t, w.Complete, "trimmed window no longer reaches the start")
	assert.Equal(t, fmt.Sprint("p", WindowSize+4), w.Posts[WindowSize-1].ID)
}

func TestMergeSinceDropsDeletedAndHistory(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000), mkPost("b", "off", "u2", 2000)}, false, 5, 0)
	s.MarkStale(10)
	_, gen := s.FetchMode()
	edited := mkPost("a", "off", "u2", 1000)
	edited.Message, edited.EditAt, edited.UpdateAt = "edited", 4000, 4000
	hist := mkPost("h", "off", "u2", 1000)
	hist.OriginalID, hist.DeleteAt, hist.UpdateAt = "a", 4000, 4000
	del := mkPost("b", "off", "u2", 2000)
	del.DeleteAt, del.UpdateAt = 4100, 4100
	older := mkPost("old", "off", "u2", 500) // before the window start: not ours to insert
	newer := mkPost("c", "off", "u2", 4200)
	s.MergeSince("off", []model.Post{older, hist, edited, del, newer}, 99, gen)
	assert.Equal(t, []string{"a", "c"}, windowIDs(s, "off"))
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.chans["off"].Win
	assert.Equal(t, "edited", w.Posts[0].Message)
	assert.False(t, w.Stale)
	assert.Empty(t, w.GapAfter)
	assert.Equal(t, int64(99), w.SyncedAt)
}

func TestMarkStaleAndSyncItemsPriority(t *testing.T) {
	s := newFixture()
	s.SetWindow("town", []model.Post{mkPost("t1", "town", "u2", 1)}, false, 5, 0)
	s.MarkStale(50)
	items := s.SyncItems()
	byID := map[string]SyncItem{}
	for _, it := range items {
		byID[it.ChannelID] = it
	}
	require.Contains(t, byID, "town")
	assert.True(t, byID["town"].Loaded)
	assert.Equal(t, int64(50), byID["town"].SyncedAt, "stale window resumes from the last live moment")
	assert.False(t, byID["off"].Loaded)
	assert.Equal(t, 1, byID["dm2"].Priority, "DM with unread/mention first")
	assert.Equal(t, 2, byID["off"].Priority)
	assert.Equal(t, "dm2", items[0].ChannelID)
	assert.NotContains(t, byID, "arch", "archived channels are not fetched")
	s.SetActive("gm")
	it, ok := s.SyncItemFor("gm")
	require.True(t, ok)
	assert.Equal(t, 0, it.Priority, "the open channel jumps the queue")
	s.mu.Lock()
	assert.Equal(t, "t1", s.chans["town"].Win.GapAfter)
	s.mu.Unlock()
}

// oldestShown is the oldest post channelID shows.
func oldestShown(t *testing.T, s *Server, channelID string) string {
	t.Helper()
	v, ok := s.ChannelView(channelID)
	require.True(t, ok)
	require.NotEmpty(t, v.Posts)
	return v.Posts[0].ID
}

func TestAppendOlderOnlyForActiveChannel(t *testing.T) {
	s := newFixture()
	s.SetWindow("town", []model.Post{mkPost("w1", "town", "u2", 100)}, false, 5, 0)
	assert.False(t, appendOlder(t, s, "town", []model.Post{mkPost("o1", "town", "u2", 50)}, true), "not active: ignored")
	assert.Equal(t, "w1", oldestShown(t, s, "town"))
	s.SetActive("town")
	appendOlder(t, s, "town", []model.Post{mkPost("o1", "town", "u2", 50)}, true)
	assert.Equal(t, "o1", oldestShown(t, s, "town"))
	v, _ := s.ChannelView("town")
	assert.Equal(t, []string{"o1", "w1"}, []string{v.Posts[0].ID, v.Posts[1].ID})
	assert.False(t, v.HasMore)
	s.SetActive("off")
	v, _ = s.ChannelView("town")
	assert.Len(t, v.Posts, 1, "history is dropped when leaving the channel")
}

// Fix round 1, finding 2: trimming a full window for the active channel
// must not drop the evicted post from what the user is reading — it moves
// to the end of s.older instead, where ChannelView still shows it.
func TestTrimmedWindowPostMovesToOlderForActiveChannel(t *testing.T) {
	s := newFixture()
	var page []model.Post
	for i := 0; i < WindowSize; i++ {
		page = append(page, mkPost(fmt.Sprint("w", i), "town", "u2", int64(100+i)))
	}
	s.SetWindow("town", page, true, 5, 0)
	s.SetActive("town")
	appendOlder(t, s, "town", []model.Post{mkPost("old1", "town", "u2", 50)}, false)
	s.ClearGuard()
	s.ApplyEvent(postedEv(mkPost("new1", "town", "u2", int64(100+WindowSize))))

	v, _ := s.ChannelView("town")
	require.Len(t, v.Posts, WindowSize+2, "old1, the trimmed w0, and the still-full window")
	assert.Equal(t, "old1", v.Posts[0].ID)
	assert.Equal(t, "w0", v.Posts[1].ID, "trimmed post still visible, right after older history")
	assert.Equal(t, "new1", v.Posts[len(v.Posts)-1].ID)
}

// Fix round 1, finding 3: an empty final AppendOlder page (there is no more
// history) must clear HasMore even though s.older stays empty.
func TestHasMoreFalseAfterEmptyFinalAppendOlder(t *testing.T) {
	s := newFixture()
	s.SetWindow("town", []model.Post{mkPost("t1", "town", "u2", 100)}, false, 5, 0)
	s.SetActive("town")
	v, _ := s.ChannelView("town")
	assert.True(t, v.HasMore, "sanity: window incomplete, nothing fetched yet")
	appendOlder(t, s, "town", nil, true)
	v, _ = s.ChannelView("town")
	assert.False(t, v.HasMore, "AppendOlder said there is nothing older left")
}

// windowPost returns the window copy of the post with the given id, for
// asserting on fields upsertLocked may or may not have overwritten.
func windowPost(s *Server, ch, id string) (model.Post, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := indexOf(s.chans[ch].Win.Posts, id); i >= 0 {
		return s.chans[ch].Win.Posts[i], true
	}
	return model.Post{}, false
}

// The frontend keys a feed row by pending_post_id (falling back to id) so
// the pending-post row and its eventual confirmed row share one virtualizer
// key and don't remount. That only holds if PendingPostID survives every
// upsert after the confirming one — a plain edit echo, a re-fetched page —
// even when that particular payload doesn't carry it.
func TestUpsertPreservesPendingPostIDAcrossLaterUpdatesWithoutIt(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("off", nil, true, 0, 0)
	created := mkPost("real1", "off", "u2", 1000)
	created.PendingPostID = "u2:1"
	s.PostCreated(created)

	p, ok := windowPost(s, "off", "real1")
	require.True(t, ok)
	assert.Equal(t, "u2:1", p.PendingPostID, "sanity: the confirming post carries it")

	edited := mkPost("real1", "off", "u2", 1000)
	edited.UpdateAt, edited.EditAt = 2000, 2000
	// edited.PendingPostID left empty, as a real edit echo would arrive.
	s.ApplyPostUpdate(edited)

	p, ok = windowPost(s, "off", "real1")
	require.True(t, ok)
	assert.Equal(t, "u2:1", p.PendingPostID, "an update without pending_post_id must not erase the stored value")
	assert.Equal(t, int64(2000), p.EditAt, "the update itself still applies")
}

// Spike §4.1 п.6: the server moves a root's update_at to each reply's time,
// so a page whose root is older than the newest reply we applied live was
// read before that reply — it must not roll the count back. A newer page
// wins.
func TestSetWindowKeepsNewerLocalReplyCount(t *testing.T) {
	for _, crt := range []bool{true, false} {
		t.Run(fmt.Sprint("crt=", crt), func(t *testing.T) {
			s := crtFixture(crt)
			root := mkPost("root", "town", "u2", 1000)
			root.ReplyCount, root.LastReplyAt, root.UpdateAt = 1, 2000, 2000
			s.SetWindow("town", []model.Post{root}, true, 5, 0)
			s.ApplyEvent(postedEv(reply("r2", "root", "u3", 3000, 2)))

			old := root // read before r2
			s.SetWindow("town", []model.Post{old}, true, 6, 0)
			n, last := rootCount(t, s, "town", "root")
			assert.Equal(t, int64(2), n, "SetWindow: an older page keeps the live count")
			assert.Equal(t, int64(3000), last)

			edited := root // an edit echo read before r2, update_at still behind it
			edited.Message, edited.EditAt, edited.UpdateAt = "edited", 2500, 2500
			s.ApplyPostUpdate(edited)
			p, _ := windowPost(s, "town", "root")
			assert.Equal(t, "edited", p.Message, "the edit itself applies")
			assert.Equal(t, int64(2), p.ReplyCount, "upsert: an older root keeps the live count")
			assert.Equal(t, int64(3000), p.LastReplyAt)

			fresh := root // read after r2 and one more reply we missed
			fresh.ReplyCount, fresh.LastReplyAt, fresh.UpdateAt = 3, 3500, 3500
			s.SetWindow("town", []model.Post{fresh}, true, 7, 0)
			n, last = rootCount(t, s, "town", "root")
			assert.Equal(t, int64(3), n, "a newer page takes its own count")
			assert.Equal(t, int64(3500), last)
		})
	}
}

// Spike §4.1 п.7: under CRT a reply does not make the channel need a view
// (its read state is the thread's), only roots do.
func TestNeedsViewUnderCRTCountsRootsOnly(t *testing.T) {
	s := crtFixture(true)
	s.SetWindow("town", []model.Post{mkPost("root", "town", "u2", 1000)}, true, 5, 0)
	require.False(t, s.NeedsView("town"), "sanity: town is read")
	s.ApplyEvent(postedEv(reply("r1", "root", "u2", 5000, 1), "u1"))
	assert.False(t, s.NeedsView("town"), "a reply (even one mentioning us) is not the channel's to read")
	s.ApplyEvent(postedEv(mkPost("root2", "town", "u2", 6000)))
	assert.True(t, s.NeedsView("town"), "a new root is")

	s = crtFixture(false)
	s.ApplyEvent(postedEv(reply("r1", "root", "u2", 5000, 1)))
	assert.True(t, s.NeedsView("town"), "without CRT a reply is an ordinary message")
}

// Review, important 3: a page requested before ResetWindows (possibly in
// the other CRT mode) is dropped on landing — it must not install a window
// as Loaded that nothing refetches. A page requested after goes in.
func TestPageFromBeforeResetWindowsIsDropped(t *testing.T) {
	s := crtFixture(false)
	s.SetActive("town")
	_, gen := s.FetchMode()
	s.SetWindow("town", []model.Post{mkPost("a", "town", "u2", 100)}, false, 5, gen)
	older, ok := s.BeginLoadOlder("town")
	require.True(t, ok)
	s.ResetWindows()

	s.MergeSince("town", []model.Post{mkPost("b", "town", "u2", 200)}, 6, gen)
	s.SetWindow("off", []model.Post{mkPost("c", "off", "u2", 100)}, true, 6, gen)
	s.AppendOlder(older, plist("", "a", mkPost("o", "town", "u2", 50)))
	for _, ch := range []string{"town", "off"} {
		it, ok := s.SyncItemFor(ch)
		require.True(t, ok, "%s still needs a fetch", ch)
		assert.False(t, it.Loaded, ch)
	}
	v, _ := s.ChannelView("town")
	assert.Empty(t, v.Posts)

	crt, gen2 := s.FetchMode()
	assert.False(t, crt)
	assert.NotEqual(t, gen, gen2)
	s.SetWindow("town", []model.Post{mkPost("a", "town", "u2", 100)}, false, 7, gen2)
	appendOlder(t, s, "town", []model.Post{mkPost("o", "town", "u2", 50)}, true)
	v, _ = s.ChannelView("town")
	assert.Equal(t, []string{"o", "a"}, []string{v.Posts[0].ID, v.Posts[1].ID})
}

func TestMarkStaleAdvancesSyncGenAndDropsInFlightFetch(t *testing.T) {
	s := newFixture()
	_, gen0 := s.FetchMode()
	require.True(t, s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000)}, false, 1000, gen0))

	// First reconnect marks off stale; fetcher captures gen1 before its HTTP call.
	s.MarkStale(1500)
	it1, _, gen1, ok := s.BeginFetch("off")
	require.True(t, ok)
	assert.NotEqual(t, gen0, gen1)
	assert.True(t, it1.Loaded)

	// While that fetch is in flight, a second stream loss occurs.
	s.MarkStale(2500)
	_, gen2 := s.FetchMode()
	assert.NotEqual(t, gen1, gen2)

	// In-flight MergeSince and SetWindow with gen1 must both be dropped,
	// preserving Stale=true and GapAfter="a" so the post-resync fetch still runs.
	applied, needReload := s.MergeSince("off", []model.Post{mkPost("old", "off", "u2", 1200)}, 1600, gen1)
	assert.False(t, applied)
	assert.False(t, needReload)
	assert.False(t, s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000)}, false, 1600, gen1))

	v, _ := s.ChannelView("off")
	assert.True(t, v.Syncing, "in-flight fetch must not clear Stale after MarkStale")
	assert.Equal(t, "a", v.GapAfter)
	_, stillNeed := s.SyncItemFor("off")
	assert.True(t, stillNeed)

	// Post-resync catch-up with gen2 applies cleanly.
	applied, needReload = s.MergeSince("off", []model.Post{mkPost("b", "off", "u2", 2600)}, 2700, gen2)
	assert.True(t, applied)
	assert.False(t, needReload)
	v, _ = s.ChannelView("off")
	assert.False(t, v.Syncing)
	assert.Empty(t, v.GapAfter)
	assert.Equal(t, []string{"a", "b"}, windowIDs(s, "off"))
}

func TestMergeSinceTriggersReloadOnMetadataGap(t *testing.T) {
	for _, crt := range []bool{false, true} {
		t.Run(fmt.Sprint("crt=", crt), func(t *testing.T) {
			s := crtFixture(crt)
			_, gen := s.FetchMode()
			require.True(t, s.SetWindow("town", []model.Post{mkPost("p1", "town", "u2", 1000)}, false, 1000, gen))

			// Server metadata advances by 1 root post during an outage.
			b := fixture()
			if crt {
				b.Config.CollapsedThreads = "always_on"
			}
			b.Channels[0].TotalMsgCount++
			b.Channels[0].TotalMsgCountRoot++
			b.Channels[0].LastPostAt = 2000
			b.Channels[0].LastRootPostAt = 2000
			s.Bootstrap(b)
			s.MarkStale(1500)
			_, gen2 := s.FetchMode()

			// A posts?since= response that missed the new post leaves metadata ahead
			// of what the window accounted for -> MergeSince requests full reload.
			applied, needReload := s.MergeSince("town", nil, 2500, gen2)
			assert.True(t, applied)
			assert.True(t, needReload)
			it, ok := s.SyncItemFor("town")
			require.True(t, ok)
			assert.True(t, it.NeedReload)

			// Authoritative SetWindow clears NeedReload and Stale.
			require.True(t, s.SetWindow("town", []model.Post{mkPost("p1", "town", "u2", 1000), mkPost("p2", "town", "u2", 2000)}, false, 2600, gen2))
			_, ok = s.SyncItemFor("town")
			assert.False(t, ok)
		})
	}
}

func TestReconcileMetadataMarksDiscrepantWindowForReload(t *testing.T) {
	for _, crt := range []bool{false, true} {
		t.Run(fmt.Sprint("crt=", crt), func(t *testing.T) {
			s := crtFixture(crt)
			_, gen := s.FetchMode()
			require.True(t, s.SetWindow("town", []model.Post{mkPost("p1", "town", "u2", 1000)}, false, 1000, gen))
			s.ClearGuard()
			assert.Empty(t, s.ReconcileMetadata(), "matching metadata triggers no reload")

			// Under CRT, a reply bumps TotalMsgCount/LastPostAt but not root fields:
			// must NOT trigger a channel reload.
			if crt {
				b := fixture()
				b.Config.CollapsedThreads = "always_on"
				b.Channels[0].TotalMsgCount++
				b.Channels[0].LastPostAt = 1500
				s.Bootstrap(b)
				assert.True(t, s.ClearGuard())
				assert.Empty(t, s.ReconcileMetadata(), "CRT reply metadata must not reload root-only channel window")
			}

			// A missed root post bumps TotalMsgCountRoot/LastRootPostAt and TotalMsgCount/LastPostAt.
			b := fixture()
			if crt {
				b.Config.CollapsedThreads = "always_on"
			}
			b.Channels[0].TotalMsgCount += 2
			b.Channels[0].TotalMsgCountRoot++
			b.Channels[0].LastPostAt = 3000
			b.Channels[0].LastRootPostAt = 3000
			s.Bootstrap(b)
			assert.True(t, s.ClearGuard())

			items := s.ReconcileMetadata()
			require.Len(t, items, 1)
			assert.Equal(t, "town", items[0].ChannelID)
			assert.True(t, items[0].NeedReload)
		})
	}
}

func TestRestoreSetsNeedReloadForColdStartSelfHeal(t *testing.T) {
	s := newFixture()
	_, gen := s.FetchMode()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000), mkPost("c", "off", "u2", 3000)}, false, 4000, gen)
	s.SetDraft("off", "keep me")
	s.SetLiveAt(4000)
	put, _ := s.TakeSnapshot()

	r := New(fixedNow)
	require.NoError(t, r.Restore(put))
	it, ok := r.SyncItemFor("off")
	require.True(t, ok)
	assert.True(t, it.Loaded, "restored window stays readable offline")
	assert.True(t, it.NeedReload, "cold start schedules authoritative latest-page reload")
	v, ok := r.ChannelView("off")
	require.True(t, ok)
	assert.Equal(t, "keep me", v.Draft)
}
