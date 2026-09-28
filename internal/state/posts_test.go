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
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000)}, true, 5)
	s.ApplyEvent(postedEv(mkPost("ws", "off", "u2", 3000)))
	// a slower page fetch finishing after the WS post must not drop it
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000), mkPost("b", "off", "u2", 2000)}, true, 6)
	assert.Equal(t, []string{"a", "b", "ws"}, windowIDs(s, "off"))

	var page []model.Post
	for i := 0; i < WindowSize+5; i++ {
		page = append(page, mkPost(fmt.Sprint("p", i), "town", "u2", int64(10+i)))
	}
	s.SetWindow("town", page, true, 7)
	s.mu.Lock()
	w := s.chans["town"].Win
	s.mu.Unlock()
	assert.Len(t, w.Posts, WindowSize)
	assert.False(t, w.Complete, "trimmed window no longer reaches the start")
	assert.Equal(t, fmt.Sprint("p", WindowSize+4), w.Posts[WindowSize-1].ID)
}

func TestMergeSinceDropsDeletedAndHistory(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000), mkPost("b", "off", "u2", 2000)}, false, 5)
	s.MarkStale(10)
	edited := mkPost("a", "off", "u2", 1000)
	edited.Message, edited.EditAt, edited.UpdateAt = "edited", 4000, 4000
	hist := mkPost("h", "off", "u2", 1000)
	hist.OriginalID, hist.DeleteAt, hist.UpdateAt = "a", 4000, 4000
	del := mkPost("b", "off", "u2", 2000)
	del.DeleteAt, del.UpdateAt = 4100, 4100
	older := mkPost("old", "off", "u2", 500) // before the window start: not ours to insert
	newer := mkPost("c", "off", "u2", 4200)
	s.MergeSince("off", []model.Post{older, hist, edited, del, newer}, 99)
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
	s.SetWindow("town", []model.Post{mkPost("t1", "town", "u2", 1)}, false, 5)
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

func TestAppendOlderOnlyForActiveChannel(t *testing.T) {
	s := newFixture()
	s.SetWindow("town", []model.Post{mkPost("w1", "town", "u2", 100)}, false, 5)
	s.AppendOlder("town", []model.Post{mkPost("o1", "town", "u2", 50)}, true)
	assert.Equal(t, "w1", s.OldestPostID("town"), "not active: ignored")
	s.SetActive("town")
	s.AppendOlder("town", []model.Post{mkPost("o1", "town", "u2", 50)}, true)
	assert.Equal(t, "o1", s.OldestPostID("town"))
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
	s.SetWindow("town", page, true, 5)
	s.SetActive("town")
	s.AppendOlder("town", []model.Post{mkPost("old1", "town", "u2", 50)}, false)
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
	s.SetWindow("town", []model.Post{mkPost("t1", "town", "u2", 100)}, false, 5)
	s.SetActive("town")
	v, _ := s.ChannelView("town")
	assert.True(t, v.HasMore, "sanity: window incomplete, nothing fetched yet")
	s.AppendOlder("town", nil, true)
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
	s.SetWindow("off", nil, true, 0)
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
			s.SetWindow("town", []model.Post{root}, true, 5)
			s.ApplyEvent(postedEv(reply("r2", "root", "u3", 3000, 2)))

			old := root // read before r2
			s.SetWindow("town", []model.Post{old}, true, 6)
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
			s.SetWindow("town", []model.Post{fresh}, true, 7)
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
	s.SetWindow("town", []model.Post{mkPost("root", "town", "u2", 1000)}, true, 5)
	require.False(t, s.NeedsView("town"), "sanity: town is read")
	s.ApplyEvent(postedEv(reply("r1", "root", "u2", 5000, 1), "u1"))
	assert.False(t, s.NeedsView("town"), "a reply (even one mentioning us) is not the channel's to read")
	s.ApplyEvent(postedEv(mkPost("root2", "town", "u2", 6000)))
	assert.True(t, s.NeedsView("town"), "a new root is")

	s = crtFixture(false)
	s.ApplyEvent(postedEv(reply("r1", "root", "u2", 5000, 1)))
	assert.True(t, s.NeedsView("town"), "without CRT a reply is an ordinary message")
}
