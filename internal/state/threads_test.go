package state

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

// seededThread is a root in town (by u2) with n replies at 1001..1000+n,
// shaped like the server holds it: each reply carries the thread's total
// of its moment, the root's update_at is its newest reply's time.
func seededThread(id string, n int) (model.Post, []model.Post) {
	root := mkPost(id, "town", "u2", 1000)
	replies := make([]model.Post, n)
	for i := range replies {
		replies[i] = reply(fmt.Sprintf("%s-r%03d", id, i+1), id, []string{"u2", "u3"}[i%2], 1001+int64(i), int64(i+1))
	}
	root.ReplyCount = int64(n)
	if n > 0 {
		root.LastReplyAt, root.UpdateAt = replies[n-1].CreateAt, replies[n-1].CreateAt
	}
	return root, replies
}

// threadPage is GET /posts/{root}/thread's answer (direction=up): the root
// first, then the replies newest first.
func threadPage(root model.Post, replies []model.Post, hasNext bool) model.PostList {
	l := model.PostList{Order: []string{root.ID}, Posts: map[string]model.Post{root.ID: root}, HasNext: &hasNext}
	for i := len(replies) - 1; i >= 0; i-- {
		l.Order = append(l.Order, replies[i].ID)
		l.Posts[replies[i].ID] = replies[i]
	}
	return l
}

// latestPage is the first page: the newest ThreadPage replies.
func latestPage(root model.Post, replies []model.Post) model.PostList {
	from := max(0, len(replies)-ThreadPage)
	return threadPage(root, replies[from:], from > 0)
}

// olderPage is the page before the reply with id before (fromCreateAt/fromPost).
func olderPage(root model.Post, replies []model.Post, before string) model.PostList {
	to := slices.IndexFunc(replies, func(p model.Post) bool { return p.ID == before })
	from := max(0, to-ThreadPage)
	return threadPage(root, replies[from:to], from > 0)
}

func threadPostIDs(v ThreadView) []string {
	out := make([]string, 0, len(v.Posts))
	for _, p := range v.Posts {
		out = append(out, p.ID)
	}
	return out
}

func mustThread(t *testing.T, s *Server, root string) ThreadView {
	t.Helper()
	v, ok := s.ThreadView(root)
	require.True(t, ok, "thread %s not held", root)
	return v
}

func openLoaded(t *testing.T, s *Server, root model.Post, replies []model.Post) uint64 {
	t.Helper()
	epoch, need, ok := s.OpenThread(root.ChannelID, root.ID)
	require.True(t, ok)
	require.True(t, need)
	s.SetThreadPage(root.ID, epoch, latestPage(root, replies))
	return epoch
}

func townEv(typ string, v any, key string) ws.Event {
	b, _ := json.Marshal(v)
	d, _ := json.Marshal(map[string]any{key: string(b)})
	return ws.Event{Type: typ, Data: d, Broadcast: ws.Broadcast{ChannelID: "town"}}
}

// Review focus 2: 20 threads × 250 replies opened, scrolled up to the cap,
// fed live replies at the cap, closed or replaced; then CRT switches and the
// worker lets go. The cache never holds more than ThreadCacheSize threads,
// ThreadMaxReplies replies each, a thread that is not open ThreadPage, and
// nothing once reset with no panel open.
func TestThreadCacheStaysBounded(t *testing.T) {
	s := crtFixture(true)
	check := func(step string) {
		t.Helper()
		s.mu.Lock()
		defer s.mu.Unlock()
		require.LessOrEqual(t, len(s.threads), ThreadCacheSize, step)
		require.Len(t, s.threadLRU, len(s.threads), step)
		for id, th := range s.threads {
			require.LessOrEqual(t, len(th.replies), ThreadMaxReplies, step)
			require.LessOrEqual(t, cap(th.replies), 2*(ThreadMaxReplies+1), step)
			if id != s.openThread {
				require.LessOrEqual(t, len(th.replies), ThreadPage, "%s: a closed thread is trimmed", step)
				require.LessOrEqual(t, cap(th.replies), 2*ThreadPage, "%s: its memory too", step)
			}
		}
	}
	for i := 0; i < 20; i++ {
		root, replies := seededThread(fmt.Sprintf("R%02d", i), 250)
		epoch := openLoaded(t, s, root, replies)
		check(fmt.Sprintf("thread %d opened", i))
		for mustThread(t, s, root.ID).HasMore {
			id, _ := s.OldestReply(root.ID)
			s.AppendOlderReplies(root.ID, epoch, id, olderPage(root, replies, id))
			check(fmt.Sprintf("thread %d scrolled", i))
		}
		v := mustThread(t, s, root.ID)
		require.True(t, v.Capped, "scrolling stops at the cap")
		require.Len(t, v.Posts, 1+ThreadMaxReplies)
		assert.Equal(t, replies[249].ID, v.Posts[ThreadMaxReplies].ID, "the newest are kept")

		for k := 0; k < 30; k++ {
			s.ApplyEvent(postedEv(reply(fmt.Sprintf("%s-live%02d", root.ID, k), root.ID, "u3", 5000+int64(k), 251+int64(k))))
		}
		check(fmt.Sprintf("thread %d live", i))
		v = mustThread(t, s, root.ID)
		require.Len(t, v.Posts, 1+ThreadMaxReplies, "a live reply at the cap pushes the oldest out")
		assert.Equal(t, fmt.Sprintf("%s-live29", root.ID), v.Posts[ThreadMaxReplies].ID)
		assert.Equal(t, int64(280), v.Posts[0].ReplyCount)

		if i%2 == 0 {
			s.CloseThread() // odd ones are replaced by the next one opened
		}
		check(fmt.Sprintf("thread %d closed", i))
		// Live replies keep a recent thread current, at its last page.
		for k := 0; k < 70; k++ {
			s.ApplyEvent(postedEv(reply(fmt.Sprintf("%s-late%02d", root.ID, k), root.ID, "u2", 6000+int64(k), 281+int64(k))))
		}
		check(fmt.Sprintf("thread %d live while recent", i))
		if i%2 == 0 {
			v = mustThread(t, s, root.ID)
			require.Len(t, v.Posts, 1+ThreadPage)
			assert.Equal(t, fmt.Sprintf("%s-late69", root.ID), v.Posts[ThreadPage].ID)
			assert.True(t, v.HasMore, "older ones can be read again")
		}
	}

	// CRT switches (the worker calls ResetThreads), the panel closes, the
	// worker stops (CloseThread + ResetThreads): nothing stays.
	s.ResetThreads()
	check("reset with the panel open")
	s.mu.Lock()
	require.LessOrEqual(t, len(s.threads), 1, "only the open thread's shell survives a reset")
	for _, th := range s.threads {
		require.Empty(t, th.replies)
		require.False(t, th.loaded)
	}
	s.mu.Unlock()
	s.CloseThread()
	s.ResetThreads()
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Empty(t, s.threads)
	assert.Empty(t, s.threadLRU)
	assert.Empty(t, s.openThread)
}

func TestClosingTrimsTheThread(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 150)
	epoch := openLoaded(t, s, root, replies)
	id, at := s.OldestReply("R")
	assert.Equal(t, replies[90].ID, id)
	assert.Equal(t, replies[90].CreateAt, at)
	s.AppendOlderReplies("R", epoch, id, olderPage(root, replies, id))
	v := mustThread(t, s, "R")
	require.Len(t, v.Posts, 1+120)
	assert.True(t, v.HasMore)
	assert.Equal(t, "R", s.OpenThreadID())

	s.CloseThread()
	assert.Equal(t, "", s.OpenThreadID())
	v = mustThread(t, s, "R")
	require.Len(t, v.Posts, 1+ThreadPage, "trimmed to the last page")
	assert.Equal(t, "R", v.Posts[0].ID, "the root stays")
	assert.Equal(t, replies[90].ID, v.Posts[1].ID)
	assert.Equal(t, replies[149].ID, v.Posts[ThreadPage].ID)
	assert.True(t, v.HasMore && v.Loaded && !v.Capped)

	_, need, ok := s.OpenThread("town", "R")
	assert.True(t, ok)
	assert.False(t, need, "reopened from memory")
	_, _, ok = s.OpenThread("nope", "R")
	assert.False(t, ok, "unknown channel")
}

// Review focus 3: replies that arrive over WS while the first page is in
// flight are kept, once; the pending reply stays; the root takes the count
// of the newest reply.
func TestThreadPageKeepsRepliesThatArrivedInFlight(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 10)
	epoch, need, ok := s.OpenThread("town", "R")
	require.True(t, ok && need)
	v := mustThread(t, s, "R")
	assert.False(t, v.Loaded)
	assert.True(t, v.Syncing)

	live := reply("R-live", "R", "u2", 2000, 11)
	eff := s.ApplyEvent(postedEv(live))
	assert.Equal(t, []string{"R"}, eff.Threads)
	s.ApplyEvent(postedEv(replies[9])) // the page carries it too
	p := s.AddPending("town", "R", "mine")

	s.SetThreadPage("R", epoch, threadPage(root, replies, false))
	v = mustThread(t, s, "R")
	want := []string{"R"}
	for _, r := range replies {
		want = append(want, r.ID)
	}
	want = append(want, "R-live", p.ID)
	assert.Equal(t, want, threadPostIDs(v))
	assert.True(t, v.Loaded && !v.Syncing && !v.HasMore)
	assert.Equal(t, int64(11), v.Posts[0].ReplyCount, "the live reply's total wins over the older page")
	assert.True(t, v.Posts[len(v.Posts)-1].Pending)
	assert.Equal(t, "town", v.ChannelID)
	assert.Equal(t, "Town Square", v.ChannelName)
	assert.Equal(t, "team", v.TeamName)
	assert.Equal(t, "u1", v.MeID)
	assert.True(t, v.CRT)
}

// Review focus 3: a page requested before ResetThreads or before its
// thread was evicted is dropped; it does not bring the thread back.
func TestLateThreadPageAfterResetIsIgnored(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 5)
	epoch, _, _ := s.OpenThread("town", "R")
	s.ResetThreads()
	s.SetThreadPage("R", epoch, threadPage(root, replies, false))
	s.AppendOlderReplies("R", epoch, "", threadPage(root, replies, false))
	s.FailThread("R", epoch, "unreachable")
	v := mustThread(t, s, "R")
	assert.False(t, v.Loaded, "the page from before the reset is dropped")
	assert.Empty(t, v.Error)
	assert.Empty(t, threadPostIDs(v))
	_, epoch2, need := s.ThreadFetch("R")
	assert.True(t, need, "the open thread is fetched again")
	assert.NotEqual(t, epoch, epoch2)

	for _, id := range []string{"A", "B", "C"} {
		r, rs := seededThread(id, 1)
		openLoaded(t, s, r, rs)
	}
	_, ok := s.ThreadView("R")
	require.False(t, ok, "evicted")
	s.SetThreadPage("R", epoch2, threadPage(root, replies, false))
	_, ok = s.ThreadView("R")
	assert.False(t, ok, "a late page does not bring an evicted thread back")
	s.mu.Lock()
	assert.Len(t, s.threads, ThreadCacheSize)
	s.mu.Unlock()

	// MarkStale (the stream was lost) also drops pages in flight: they may
	// predate the gap.
	r, rs := seededThread("D", 3)
	e, _, _ := s.OpenThread("town", "D")
	s.MarkStale(5)
	s.SetThreadPage("D", e, latestPage(r, rs))
	assert.False(t, mustThread(t, s, "D").Loaded)
	_, e, need = s.ThreadFetch("D")
	require.True(t, need)
	s.SetThreadPage("D", e, latestPage(r, rs))
	assert.True(t, mustThread(t, s, "D").Loaded)
	s.MarkStale(6)
	v = mustThread(t, s, "D")
	assert.True(t, v.Loaded && v.Syncing, "a stale thread stays readable")
	_, _, need = s.ThreadFetch("D")
	assert.True(t, need)
}

func TestReactionsAndEditsReachTheThread(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 3)
	s.SetWindow("town", []model.Post{root}, true, 5, 0)
	openLoaded(t, s, root, replies)

	eff := s.ApplyEvent(townEv("reaction_added", model.Reaction{UserID: "u2", PostID: replies[1].ID, EmojiName: "tada"}, "reaction"))
	assert.Equal(t, []string{"R"}, eff.Threads)
	v := mustThread(t, s, "R")
	assert.Equal(t, []ReactionView{{Emoji: "tada", Count: 1}}, v.Posts[2].Reactions)

	eff = s.ApplyEvent(townEv("reaction_added", model.Reaction{UserID: "u3", PostID: "R", EmojiName: "+1"}, "reaction"))
	assert.Equal(t, []string{"town"}, eff.Channels)
	assert.Equal(t, []string{"R"}, eff.Threads)
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1}}, mustThread(t, s, "R").Posts[0].Reactions)
	cv, _ := s.ChannelView("town")
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1}}, cv.Posts[0].Reactions, "and the feed's copy")

	edited := replies[0]
	edited.Message, edited.EditAt, edited.UpdateAt = "fixed", 3000, 3000
	eff = s.ApplyEvent(postEv("post_edited", edited))
	assert.Equal(t, []string{"R"}, eff.Threads)
	assert.Equal(t, "fixed", mustThread(t, s, "R").Posts[1].Message)

	er := root
	er.Message, er.EditAt, er.UpdateAt = "root fixed", 3100, 3100
	s.ApplyPostUpdate(er)
	v = mustThread(t, s, "R")
	assert.Equal(t, "root fixed", v.Posts[0].Message)
	assert.Equal(t, int64(3), v.Posts[0].ReplyCount)

	// Our own click on a reply only the thread holds (CRT).
	ch, was, ok := s.ReactLocalWas(replies[2].ID, "smile", true)
	require.True(t, ok)
	assert.False(t, was)
	assert.Equal(t, []string{"R"}, ch.Threads)
	assert.Equal(t, []ReactionView{{Emoji: "smile", Count: 1, Mine: true}}, mustThread(t, s, "R").Posts[3].Reactions)
	assert.Equal(t, []string{"R"}, s.SetPostSaved(replies[2].ID, true).Threads)
	assert.True(t, mustThread(t, s, "R").Posts[3].Saved)
}

func TestPendingReplyShowsInTheThread(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 3)
	s.SetWindow("town", []model.Post{root}, true, 5, 0)
	openLoaded(t, s, root, replies)

	p := s.AddPending("town", "R", "my reply")
	v := mustThread(t, s, "R")
	last := v.Posts[len(v.Posts)-1]
	assert.Equal(t, p.ID, last.ID)
	assert.True(t, last.Pending)
	assert.Equal(t, "my reply", last.Message)
	cv, _ := s.ChannelView("town")
	assert.Equal(t, []string{"R"}, func() []string {
		var out []string
		for _, x := range cv.Posts {
			out = append(out, x.ID)
		}
		return out
	}(), "under CRT a reply being sent shows in its thread only")

	mine := reply("R-mine", "R", "u1", 3000, 4)
	mine.PendingPostID = p.ID
	eff := s.ApplyEvent(postedEv(mine))
	assert.Contains(t, eff.Threads, "R")
	s.PostCreated(mine)
	v = mustThread(t, s, "R")
	assert.Equal(t, []string{"R", replies[0].ID, replies[1].ID, replies[2].ID, "R-mine"}, threadPostIDs(v))
	assert.False(t, v.Posts[4].Pending)
	assert.Equal(t, int64(4), v.Posts[0].ReplyCount)

	p2 := s.AddPending("town", "R", "again")
	assert.Equal(t, []string{"R"}, s.FailPending("town", p2.ID).Threads)
	assert.True(t, mustThread(t, s, "R").Posts[5].Failed)
	assert.Equal(t, []string{"R"}, s.DropPending("town", p2.ID).Threads)
	assert.Len(t, mustThread(t, s, "R").Posts, 5)
}

func TestForgottenChannelDropsItsThreads(t *testing.T) {
	s := crtFixture(true)
	offRoot := mkPost("O", "off", "u2", 1000)
	e, _, ok := s.OpenThread("off", "O")
	require.True(t, ok)
	s.SetThreadPage("O", e, threadPage(offRoot, nil, false))
	s.CloseThread()
	root, replies := seededThread("R", 2)
	openLoaded(t, s, root, replies)

	d, _ := json.Marshal(map[string]any{"channel_id": "off"})
	s.ApplyEvent(ws.Event{Type: "user_removed", Data: d, Broadcast: ws.Broadcast{UserID: "u1"}})
	_, ok = s.ThreadView("O")
	assert.False(t, ok, "the left channel's thread is dropped")
	_, ok = s.ThreadView("R")
	assert.True(t, ok)
	assert.Equal(t, "R", s.OpenThreadID())

	d, _ = json.Marshal(map[string]any{"channel_id": "town"})
	s.ApplyEvent(ws.Event{Type: "user_removed", Data: d, Broadcast: ws.Broadcast{UserID: "u1"}})
	_, ok = s.ThreadView("R")
	assert.False(t, ok)
	assert.Equal(t, "", s.OpenThreadID(), "the open one too")
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Empty(t, s.threadLRU)
}

func TestDeletedRootAndRepliesInTheThread(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 3)
	s.SetWindow("town", []model.Post{root}, true, 5, 0)
	epoch := openLoaded(t, s, root, replies)

	eff := s.ApplyEvent(deletedEv(replies[1]))
	assert.Equal(t, []string{"R"}, eff.Threads)
	v := mustThread(t, s, "R")
	assert.Equal(t, []string{"R", replies[0].ID, replies[2].ID}, threadPostIDs(v))
	assert.Equal(t, int64(2), v.Posts[0].ReplyCount, "the thread's root copy is lowered too")
	n, _ := rootCount(t, s, "town", "R")
	assert.Equal(t, int64(2), n, "once, with the feed's copy")
	s.ApplyEvent(deletedEv(replies[1]))
	assert.Equal(t, int64(2), mustThread(t, s, "R").Posts[0].ReplyCount)

	eff = s.ApplyEvent(deletedEv(root))
	assert.Equal(t, []string{"R"}, eff.Threads)
	v = mustThread(t, s, "R")
	assert.True(t, v.RootDeleted && v.Loaded && !v.Syncing)
	assert.Empty(t, v.Posts)
	_, _, need := s.ThreadFetch("R")
	assert.False(t, need, "nothing to fetch for a deleted root")

	// Late: a page read before the deletion, the root's echo, a reply.
	s.SetThreadPage("R", epoch, latestPage(root, replies))
	s.ApplyEvent(postedEv(reply("R-late", "R", "u3", 4000, 4)))
	v = mustThread(t, s, "R")
	assert.True(t, v.RootDeleted)
	assert.Empty(t, v.Posts)

	// A thread whose root is gone on the server (404).
	e2, _, _ := s.OpenThread("town", "S")
	s.FailThread("S", e2, ThreadNotFound)
	v = mustThread(t, s, "S")
	assert.True(t, v.RootDeleted && v.Loaded)
	assert.Empty(t, v.Error)

	// Other failures are shown and retried by opening again.
	s.CloseThread()
	s.ResetThreads()
	e3, _, _ := s.OpenThread("town", "R")
	s.FailThread("R", e3, "unreachable")
	v = mustThread(t, s, "R")
	assert.Equal(t, "unreachable", v.Error)
	_, need, _ = s.OpenThread("town", "R")
	assert.True(t, need)
}

// Carry from Task 2: a deleted root is recorded in the gone ring like a
// deleted reply, so its late echo does not bring it back into the feed.
func TestLateEchoOfADeletedRootIsIgnored(t *testing.T) {
	for _, crt := range []bool{true, false} {
		t.Run(fmt.Sprint("crt=", crt), func(t *testing.T) {
			s := crtFixture(crt)
			root := mkPost("R", "town", "u1", 1000)
			s.SetWindow("town", []model.Post{mkPost("x", "town", "u2", 900), root}, true, 5, 0)
			s.RemovePost("R")
			s.PostCreated(root) // our CreatePost's response, late
			s.ApplyEvent(postedEv(root))
			assert.Equal(t, []string{"x"}, windowIDs(s, "town"))
			s.SetWindow("town", []model.Post{mkPost("x", "town", "u2", 900), root}, true, 6, 0)
			assert.Equal(t, []string{"x"}, windowIDs(s, "town"), "nor a page read before the deletion")
		})
	}
}

// Carry from Task 2: MergeSince never turns an unloaded window into a
// loaded one holding just the since= rows.
func TestMergeSinceOnAnUnloadedWindowIsDropped(t *testing.T) {
	s := crtFixture(false)
	_, gen := s.FetchMode()
	s.MergeSince("town", []model.Post{mkPost("p", "town", "u2", 2000)}, 5, gen)
	v, _ := s.ChannelView("town")
	assert.False(t, v.Loaded)
	assert.Empty(t, v.Posts)
}

// Without CRT a reply's context line takes the root from the thread cache
// when the feed does not show it.
func TestReplyContextComesFromTheThreadCache(t *testing.T) {
	s := crtFixture(false)
	root, replies := seededThread("R", 2)
	root.Message = "the   root\ntext"
	openLoaded(t, s, root, replies)
	s.SetWindow("town", []model.Post{replies[1]}, false, 5, 0)
	v, _ := s.ChannelView("town")
	require.Len(t, v.Posts, 1)
	assert.Equal(t, "bob", v.Posts[0].RootAuthor)
	assert.Equal(t, "the root text", v.Posts[0].RootSnippet)
}

func TestThreadAuthorsAreLoadedAndPolled(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 2)
	replies[1].UserID = "u9"
	openLoaded(t, s, root, replies)
	assert.Contains(t, s.MissingUserIDs(), "u9")
	assert.Contains(t, s.StatusTargets(0), "u9", "the open thread's authors")
	s.CloseThread()
	assert.NotContains(t, s.StatusTargets(0), "u9", "not once it is closed")
}

// Fix round 1, minor 1: a reaction or a root edit applied live while a
// (re)load is in flight survives the older page; our click reads "was
// mine" from the first copy, not from whichever copy changed.
func TestLiveChangesInFlightSurviveThePage(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 3)
	openLoaded(t, s, root, replies)
	s.MarkStale(5) // a reload is due
	_, epoch, need := s.ThreadFetch("R")
	require.True(t, need)

	s.ApplyEvent(townEv("reaction_added", model.Reaction{UserID: "u3", PostID: replies[0].ID, EmojiName: "tada", CreateAt: 5000}, "reaction"))
	edited := root
	edited.Message, edited.EditAt, edited.UpdateAt = "edited", 5100, 5100
	s.ApplyPostUpdate(edited)
	s.SetThreadPage("R", epoch, latestPage(root, replies)) // read before both
	v := mustThread(t, s, "R")
	assert.Equal(t, "edited", v.Posts[0].Message, "the newer root copy is kept")
	assert.Equal(t, int64(3), v.Posts[0].ReplyCount)
	assert.Equal(t, []ReactionView{{Emoji: "tada", Count: 1}}, v.Posts[1].Reactions, "the live reaction is kept")

	// A page read after the reaction (its update_at moved) wins.
	fresh := replies[0]
	fresh.UpdateAt = 5200
	rs := slices.Clone(replies)
	rs[0] = fresh
	s.MarkStale(6)
	_, epoch, _ = s.ThreadFetch("R")
	s.SetThreadPage("R", epoch, latestPage(root, rs))
	assert.Empty(t, mustThread(t, s, "R").Posts[1].Reactions)
}

// Fix round 2, ruling 1: when the copies of a post (feed, thread) disagree
// on our reaction, the click is always sent (was = !add) — both endpoints
// are idempotent; guessing from one copy could drop it.
func TestClickOnDisagreeingCopiesIsAlwaysSent(t *testing.T) {
	for _, add := range []bool{true, false} {
		t.Run(fmt.Sprint("add=", add), func(t *testing.T) {
			s := crtFixture(true)
			root, replies := seededThread("R", 1)
			mine := root
			mine.Metadata = &model.PostMetadata{Reactions: []model.Reaction{{UserID: "u1", PostID: "R", EmojiName: "+1"}}}
			feed, thread := root, mine // the panel shows ours, the feed not
			if add {
				feed, thread = mine, root
			}
			s.SetWindow("town", []model.Post{feed}, true, 5, 0)
			openLoaded(t, s, thread, replies)
			_, was, ok := s.ReactLocalWas("R", "+1", add)
			require.True(t, ok)
			assert.Equal(t, !add, was, "the request goes out")
		})
	}
	// Agreeing copies still tell the truth.
	s := crtFixture(true)
	root, replies := seededThread("R", 1)
	s.SetWindow("town", []model.Post{root}, true, 5, 0)
	openLoaded(t, s, root, replies)
	_, was, _ := s.ReactLocalWas("R", "+1", true)
	assert.False(t, was)
	_, was, _ = s.ReactLocalWas("R", "+1", false)
	assert.True(t, was)
}

// Fix round 2, ruling 2: the echo of our own click moves update_at too
// (the reaction is already there, but the server moved the post's).
func TestOwnEchoMovesUpdateAt(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 1)
	s.SetWindow("town", []model.Post{root}, true, 5, 0)
	openLoaded(t, s, root, replies)
	s.ReactLocalWas("R", "+1", true)
	s.ApplyEvent(townEv("reaction_added", model.Reaction{UserID: "u1", PostID: "R", EmojiName: "+1", CreateAt: 5000}, "reaction"))
	p, _ := windowPost(s, "town", "R")
	assert.Equal(t, int64(5000), p.UpdateAt, "feed copy")
	s.mu.Lock()
	assert.Equal(t, int64(5000), s.threads["R"].root.UpdateAt, "thread copy")
	s.mu.Unlock()
}

// Fix round 2, ruling 3: a feed page read before a live reaction or edit
// does not overwrite it (as in SetThreadPage).
func TestPageDoesNotOverwriteLiveChangesInTheFeed(t *testing.T) {
	s := crtFixture(false)
	a, b := mkPost("a", "town", "u2", 1000), mkPost("b", "town", "u3", 1100)
	s.SetWindow("town", []model.Post{a, b}, true, 5, 0)
	s.ApplyEvent(townEv("reaction_added", model.Reaction{UserID: "u3", PostID: "a", EmojiName: "tada", CreateAt: 5000}, "reaction"))
	eb := b
	eb.Message, eb.EditAt, eb.UpdateAt = "edited", 5100, 5100
	s.ApplyPostUpdate(eb)
	s.SetWindow("town", []model.Post{a, b}, true, 6, 0) // read before both
	pa, _ := windowPost(s, "town", "a")
	pb, _ := windowPost(s, "town", "b")
	require.NotNil(t, pa.Metadata)
	assert.Len(t, pa.Metadata.Reactions, 1, "the live reaction is kept")
	assert.Equal(t, "edited", pb.Message, "the live edit is kept")

	fresh := a
	fresh.UpdateAt = 5200 // read after: the server's copy wins
	s.SetWindow("town", []model.Post{fresh, eb}, true, 7, 0)
	pa, _ = windowPost(s, "town", "a")
	assert.Nil(t, pa.Metadata)
}

// Fix round 1, minor 2: an older page applies only on top of the reply it
// was requested from.
func TestOlderPageAppliesOnlyToItsCursor(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 150)
	epoch := openLoaded(t, s, root, replies)
	cursor, _ := s.OldestReply("R")
	page := olderPage(root, replies, cursor)
	s.AppendOlderReplies("R", epoch, cursor, page)
	require.Len(t, mustThread(t, s, "R").Posts, 1+120)
	s.AppendOlderReplies("R", epoch, cursor, page) // a concurrent twin
	require.Len(t, mustThread(t, s, "R").Posts, 1+120)

	// Evicted and reopened meanwhile: other replies are held now.
	for _, id := range []string{"A", "B", "C"} {
		r, rs := seededThread(id, 1)
		openLoaded(t, s, r, rs)
	}
	root2, replies2 := seededThread("R", 170)
	epoch = openLoaded(t, s, root2, replies2)
	before := threadPostIDs(mustThread(t, s, "R"))
	s.AppendOlderReplies("R", epoch, cursor, page)
	assert.Equal(t, before, threadPostIDs(mustThread(t, s, "R")), "not grafted onto other replies")
}

// Fix round 1, minor 3: a thread closed while its page was in flight is
// trimmed once the page lands.
func TestThreadClosedWhilePageInFlightIsTrimmed(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 60)
	epoch, _, _ := s.OpenThread("town", "R")
	for k := 0; k < 30; k++ {
		s.ApplyEvent(postedEv(reply(fmt.Sprintf("R-live%02d", k), "R", "u3", 5000+int64(k), 61+int64(k))))
	}
	s.CloseThread()
	s.SetThreadPage("R", epoch, latestPage(root, replies))
	v := mustThread(t, s, "R")
	assert.Len(t, v.Posts, 1+ThreadPage)
	assert.True(t, v.HasMore)

	r2, rs2 := seededThread("S", 150)
	e2 := openLoaded(t, s, r2, rs2)
	cursor, _ := s.OldestReply("S")
	s.CloseThread()
	s.AppendOlderReplies("S", e2, cursor, olderPage(r2, rs2, cursor))
	assert.Len(t, mustThread(t, s, "S").Posts, 1+ThreadPage)
}

// Fix round 1, minor 5: a failed load is not "syncing".
func TestFailedThreadIsNotSyncing(t *testing.T) {
	s := crtFixture(true)
	e, _, _ := s.OpenThread("town", "R")
	s.FailThread("R", e, "unreachable")
	v := mustThread(t, s, "R")
	assert.Equal(t, "unreachable", v.Error)
	assert.False(t, v.Syncing)
}

// Fix round 1, minor 6: a reply's id opens its root's thread — at once if
// the reply is held, after the page otherwise (its order[0] is the reply,
// with root_id).
func TestOpeningAReplyOpensItsRoot(t *testing.T) {
	s := crtFixture(false)
	root, replies := seededThread("R", 2)
	s.SetWindow("town", []model.Post{root, replies[0], replies[1]}, true, 5, 0)
	assert.Equal(t, "R", s.ThreadRootOf(replies[1].ID))
	assert.Equal(t, "R", s.ThreadRootOf("R"))
	assert.Equal(t, "unknown", s.ThreadRootOf("unknown"))

	_, _, ok := s.OpenThread("town", "X")
	require.True(t, ok)
	_, need, ok := s.RedirectThread("X", "R")
	require.True(t, ok)
	assert.True(t, need)
	assert.Equal(t, "R", s.OpenThreadID())
	v := mustThread(t, s, "X")
	assert.Equal(t, "R", v.RootID, "the reply's id resolves to its root's thread")
	_, ok = s.ThreadView("nope")
	assert.False(t, ok)
	s.mu.Lock()
	assert.Nil(t, s.threads["X"])
	s.mu.Unlock()
	_, _, ok = s.RedirectThread("gone", "R")
	assert.False(t, ok, "only a held thread is redirected")
}
