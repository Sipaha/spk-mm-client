package state

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// Thread focus (spec «Поиск», Секция 1б, «Уточнения» п.1–2): a reply
// beyond the 200 latest shown in a sliding window of ≤ ThreadMaxReplies
// around it, a tail of the ≤ ThreadPage latest replies, and a gap between
// them closed only on a proof.

// upFrom is the direction=up page from replies[i]: the n before it.
func upFrom(root model.Post, replies []model.Post, i, n int) model.PostList {
	from := max(0, i-n)
	return threadPage(root, replies[from:i], from > 0)
}

// downFrom is the direction=down page from replies[i]: the n after it.
func downFrom(root model.Post, replies []model.Post, i, n int) model.PostList {
	to := min(len(replies), i+1+n)
	return threadPage(root, replies[i+1:to], to < len(replies))
}

func replyAt(replies []model.Post, id string) int {
	return slices.IndexFunc(replies, func(p model.Post) bool { return p.ID == id })
}

// focusThread opens root's thread loaded on its latest page and focuses
// replies[i] with pages of 30 each side.
func focusThread(t *testing.T, s *Server, root model.Post, replies []model.Post, i int) {
	t.Helper()
	openLoaded(t, s, root, replies)
	op, held, ok := s.FocusThread(root.ChannelID, root.ID, replies[i].ID)
	require.True(t, ok)
	require.False(t, held)
	require.True(t, s.SetThreadFocus(op, replies[i], upFrom(root, replies, i, 30), downFrom(root, replies, i, 30)))
}

// loadFocus applies one page (n replies) from the focus's edge.
func loadFocus(t *testing.T, s *Server, root model.Post, all []model.Post, newer bool, n int) (closed, progressed bool) {
	t.Helper()
	op, ok := s.BeginFocusLoad(root.ID, newer)
	require.True(t, ok, "nothing to load")
	i := replyAt(all, op.Cursor.ID)
	require.GreaterOrEqual(t, i, 0)
	page := upFrom(root, all, i, n)
	if newer {
		page = downFrom(root, all, i, n)
	}
	return s.AppendFocus(op, newer, page)
}

func postIDs(posts []model.Post) []string {
	out := make([]string, 0, len(posts))
	for _, p := range posts {
		out = append(out, p.ID)
	}
	return out
}

func rids(replies []model.Post, from, to int) []string {
	var out []string
	for _, p := range replies[from:to] {
		out = append(out, p.ID)
	}
	return out
}

func TestFocusBeyond200InState(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 500)
	focusThread(t, s, root, replies, 99)
	v := mustThread(t, s, "R")
	want := append([]string{"R"}, rids(replies, 69, 130)...)
	want = append(want, rids(replies, 440, 500)...)
	assert.Equal(t, want, threadPostIDs(v))
	require.NotNil(t, v.Focus)
	assert.Equal(t, replies[99].ID, v.Focus.TargetID)
	assert.True(t, v.Focus.HasOlder)
	assert.True(t, v.Focus.HasNewer)
	assert.Equal(t, HistGap{Open: true, Gen: v.Focus.Gap.Gen, BeforeID: replies[440].ID}, v.Focus.Gap)
	assert.False(t, v.HasMore, "the plain thread's paging is off")
	assert.False(t, v.Capped)
}

func TestFocusTailSeededAndBounded(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 500)
	focusThread(t, s, root, replies, 99)
	// 70 live replies: the tail keeps the latest ThreadPage; the oldest go
	// (into the gap: they come back with it).
	all := slices.Clone(replies)
	for i := range 70 {
		p := reply(fmt.Sprintf("R-live%02d", i), "R", "u3", 3000+int64(i), 501+int64(i))
		all = append(all, p)
		s.ApplyEvent(postedEv(p))
	}
	v := mustThread(t, s, "R")
	want := append([]string{"R"}, rids(all, 69, 130)...)
	want = append(want, rids(all, 510, 570)...)
	assert.Equal(t, want, threadPostIDs(v), "tail ≤ ThreadPage, seeded then live")
	assert.Equal(t, all[510].ID, v.Focus.Gap.BeforeID)

	// The gap loaded: the window slides (≤ ThreadMaxReplies), nothing lost
	// or twice, and it closes on the tail.
	closed := false
	for range 20 {
		if closed, _ = loadFocus(t, s, root, all, true, ThreadPage); closed {
			break
		}
		f := s.threads["R"].focus
		require.LessOrEqual(t, len(f.replies), ThreadMaxReplies)
	}
	require.True(t, closed)
	v = mustThread(t, s, "R")
	ids := threadPostIDs(v)[1:]
	assert.Equal(t, rids(all, len(all)-len(ids), len(all)), ids, "one contiguous run up to the latest")
	assert.LessOrEqual(t, len(ids), ThreadMaxReplies+ThreadPage)
	assert.False(t, v.Focus.Gap.Open)
	assert.False(t, v.Focus.HasNewer)
	assert.True(t, v.Focus.HasOlder, "the up edge was let go of")

	// Closed: a tail overflow moves into the segment (no hole), which
	// slides again.
	for i := range 10 {
		p := reply(fmt.Sprintf("R-more%02d", i), "R", "u3", 4000+int64(i), 600+int64(i))
		all = append(all, p)
		s.ApplyEvent(postedEv(p))
	}
	v = mustThread(t, s, "R")
	ids = threadPostIDs(v)[1:]
	assert.Equal(t, rids(all, len(all)-len(ids), len(all)), ids)
	assert.False(t, v.Focus.Gap.Open)
	assert.LessOrEqual(t, len(s.threads["R"].focus.replies), ThreadMaxReplies)
	assert.Len(t, s.threads["R"].replies, ThreadPage)
}

func TestFocusSlidingWindowInState(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 500)
	focusThread(t, s, root, replies, 99) // 69..129
	for range 3 {
		loadFocus(t, s, root, replies, true, ThreadPage) // up to 309
	}
	f := s.threads["R"].focus
	assert.Equal(t, rids(replies, 110, 310), postIDs(f.replies), "down let go of the up edge")
	assert.Equal(t, replies[110].ID, f.up.ID, "its cursor kept")
	assert.False(t, f.upDone)
	loadFocus(t, s, root, replies, false, ThreadPage)
	f = s.threads["R"].focus
	assert.Equal(t, rids(replies, 50, 250), postIDs(f.replies), "up brings them back, lets go of the down edge")
	assert.Equal(t, replies[249].ID, f.down.ID)
	loadFocus(t, s, root, replies, true, ThreadPage)
	assert.Equal(t, rids(replies, 110, 310), postIDs(s.threads["R"].focus.replies))
	for range 2 {
		loadFocus(t, s, root, replies, false, ThreadPage)
	}
	v := mustThread(t, s, "R")
	assert.False(t, v.Focus.HasOlder, "the first reply reached")
	assert.Equal(t, rids(replies, 0, 200), postIDs(s.threads["R"].focus.replies))
	_, ok := s.BeginFocusLoad("R", false)
	assert.False(t, ok)
}

func TestFocusGapClosesOnProofOnlyForCurrentGen(t *testing.T) {
	root, replies := seededThread("R", 300)
	t.Run("an op of an older generation closes nothing", func(t *testing.T) {
		s := crtFixture(true)
		focusThread(t, s, root, replies, 99)
		op, ok := s.BeginFocusLoad("R", true)
		require.True(t, ok)
		s.MarkThreadStale("R")
		closed, _ := s.AppendFocus(op, true, downFrom(root, replies, replyAt(replies, op.Cursor.ID), 200))
		assert.False(t, closed)
		v := mustThread(t, s, "R")
		assert.True(t, v.Focus.Gap.Open)
		assert.Len(t, v.Posts, 1+61+60, "the page was dropped")
	})
	t.Run("not while the tail is stale", func(t *testing.T) {
		s := crtFixture(true)
		focusThread(t, s, root, replies, 99)
		s.MarkThreadStale("R")
		closed, progressed := loadFocus(t, s, root, replies, true, 200) // reaches the tail and the end
		assert.False(t, closed)
		assert.True(t, progressed)
		assert.True(t, mustThread(t, s, "R").Focus.Gap.Open)
		// The tail read again: the next page proves it.
		epoch, _, _ := s.OpenThread("town", "R")
		s.SetThreadPage("R", epoch, latestPage(root, replies))
		require.NotNil(t, mustThread(t, s, "R").Focus, "opening the open thread again keeps its focus")
		closed, _ = loadFocus(t, s, root, replies, true, ThreadPage)
		assert.True(t, closed)
		v := mustThread(t, s, "R")
		assert.False(t, v.Focus.Gap.Open)
		ids := threadPostIDs(v)[1:]
		assert.Equal(t, rids(replies, len(replies)-len(ids), len(replies)), ids)
	})
	t.Run("an intersection with the tail", func(t *testing.T) {
		s := crtFixture(true)
		focusThread(t, s, root, replies, 99)
		closed, _ := loadFocus(t, s, root, replies, true, ThreadPage) // 130..189
		assert.False(t, closed)
		closed, _ = loadFocus(t, s, root, replies, true, ThreadPage) // 190..249: tail is 240..299
		assert.True(t, closed)
		v := mustThread(t, s, "R")
		assert.Equal(t, append([]string{"R"}, rids(replies, 69, 300)...), threadPostIDs(v))
		assert.Empty(t, v.Focus.Gap.BeforeID)
	})
	t.Run("nothing newer, the tail live", func(t *testing.T) {
		s := crtFixture(true)
		focusThread(t, s, root, replies, 99)
		for _, p := range replies[180:] { // deleted: the tail is left empty
			s.ApplyEvent(deletedEv(p))
		}
		closed, _ := loadFocus(t, s, root, replies[:180], true, 30)
		assert.False(t, closed, "has_next")
		closed, _ = loadFocus(t, s, root, replies[:180], true, 30)
		assert.True(t, closed, "has_next=false with a live tail")
	})
}

func TestFocusInvalidatedByCloseAndReopenInState(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 300)
	focusThread(t, s, root, replies, 99)
	op, ok := s.BeginFocusLoad("R", true)
	require.True(t, ok)
	s.CloseThread()
	_, _, ok = s.OpenThread("town", "R")
	require.True(t, ok)
	closed, progressed := s.AppendFocus(op, true, downFrom(root, replies, 129, 60))
	assert.False(t, closed || progressed)
	assert.Nil(t, mustThread(t, s, "R").Focus)

	// A focus begun, then another: the first one's pages are dropped.
	first, _, _ := s.FocusThread("town", "R", replies[20].ID)
	second, _, _ := s.FocusThread("town", "R", replies[40].ID)
	assert.False(t, s.SetThreadFocus(first, replies[20], upFrom(root, replies, 20, 30), downFrom(root, replies, 20, 30)))
	assert.Nil(t, mustThread(t, s, "R").Focus)
	require.True(t, s.SetThreadFocus(second, replies[40], upFrom(root, replies, 40, 30), downFrom(root, replies, 40, 30)))
	v := mustThread(t, s, "R")
	assert.Equal(t, replies[40].ID, v.Focus.TargetID)
	gen := v.Focus.Gap.Gen

	// ResetThreads (CRT switched) drops it.
	op, _ = s.BeginFocusLoad("R", false)
	s.ResetThreads()
	s.AppendFocus(op, false, upFrom(root, replies, 10, 30))
	assert.Nil(t, mustThread(t, s, "R").Focus)
	assert.NotZero(t, gen)
}

func TestFocusHeldReplyNeedsNoFocusInState(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 100)
	openLoaded(t, s, root, replies)
	_, held, ok := s.FocusThread("town", "R", replies[80].ID)
	assert.True(t, ok)
	assert.True(t, held)
	_, held, _ = s.FocusThread("town", "R", "R")
	assert.True(t, held, "the root is always shown")
	_, held, _ = s.FocusThread("town", "R", replies[10].ID)
	assert.False(t, held, "not held (only the latest page is)")
	_, _, ok = s.FocusThread("nochannel", "R", replies[10].ID)
	assert.False(t, ok)
}

func TestFocusPendingReactionsAndRootDelete(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 300)
	s.SetWindow("town", []model.Post{root}, true, 5, 0)
	focusThread(t, s, root, replies, 99)
	target := replies[99]

	p := s.AddPending("town", "R", "my reply")
	v := mustThread(t, s, "R")
	assert.Equal(t, p.ID, v.Posts[len(v.Posts)-1].ID, "a reply being sent at the end, after the tail")

	eff := s.ApplyEvent(townEv("reaction_added", model.Reaction{UserID: "u2", PostID: target.ID, EmojiName: "tada"}, "reaction"))
	assert.Equal(t, []string{"R"}, eff.Threads)
	at := func(id string) PostView {
		v := mustThread(t, s, "R")
		i := slices.IndexFunc(v.Posts, func(p PostView) bool { return p.ID == id })
		require.GreaterOrEqual(t, i, 0, id)
		return v.Posts[i]
	}
	assert.Equal(t, []ReactionView{{Emoji: "tada", Count: 1}}, at(target.ID).Reactions)
	_, _, ok := s.ReactLocalWas(target.ID, "smile", true)
	assert.True(t, ok, "a focused reply is held")

	edited := target
	edited.Message, edited.EditAt, edited.UpdateAt = "fixed", 5000, 5000
	assert.Equal(t, []string{"R"}, s.ApplyEvent(postEv("post_edited", edited)).Threads)
	assert.Equal(t, "fixed", at(target.ID).Message)

	s.ApplyEvent(deletedEv(replies[100]))
	assert.NotContains(t, threadPostIDs(mustThread(t, s, "R")), replies[100].ID)

	assert.Equal(t, "R", s.ThreadRootOf(replies[90].ID))
	assert.Contains(t, s.threadsHoldingLocked(replies[90].ID), "R")

	s.ApplyEvent(deletedEv(root))
	v = mustThread(t, s, "R")
	assert.True(t, v.RootDeleted)
	assert.Nil(t, v.Focus)
	for _, p := range v.Posts {
		assert.True(t, p.Pending, "only our reply being sent is left")
	}
}

func TestPlainThreadAfterFocusClosed(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 300)
	focusThread(t, s, root, replies, 99)
	s.CloseThread()
	epoch, _, ok := s.OpenThread("town", "R")
	require.True(t, ok)
	v := mustThread(t, s, "R")
	assert.Nil(t, v.Focus)
	assert.Equal(t, append([]string{"R"}, rids(replies, 240, 300)...), threadPostIDs(v), "the latest page")
	assert.True(t, v.HasMore)
	s.AppendOlderReplies("R", epoch, replies[240].ID, olderPage(root, replies, replies[240].ID))
	assert.Len(t, mustThread(t, s, "R").Posts, 1+120, "paging up again")
}

// Reconnect: the focus goes stale; a closed gap opens again (the tail joins
// the segment — the stream was lost after it); the reread applies an
// offline edit and deletion, removing only once covered.
func TestFocusRevalidationInState(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 300)
	focusThread(t, s, root, replies, 99)
	loadFocus(t, s, root, replies, true, ThreadPage)
	closed, _ := loadFocus(t, s, root, replies, true, ThreadPage)
	require.True(t, closed)
	s.MarkStale(5000)
	v := mustThread(t, s, "R")
	assert.True(t, v.Focus.Gap.Open, "reopened: the tail may miss replies")
	assert.True(t, v.Focus.Gap.Stale)
	assert.LessOrEqual(t, len(s.threads["R"].focus.replies), ThreadMaxReplies)

	op, ok := s.BeginFocusRevalidate("R")
	require.True(t, ok)
	f := s.threads["R"].focus
	assert.Equal(t, f.replies[0].ID, op.Low.ID)
	assert.Equal(t, f.replies[len(f.replies)-1].ID, op.High.ID)

	server := slices.Clone(replies)
	server[150].Message, server[150].EditAt, server[150].UpdateAt = "edited offline", 6000, 6000
	deleted := server[160].ID
	server = slices.Delete(server, 160, 161)
	lo := replyAt(server, op.Low.ID)
	hi := replyAt(server, op.High.ID)
	high := server[hi]
	// Not covered: nothing removed, still stale.
	partial := Reread{High: &high, Pages: []model.PostList{upFrom(root, server, hi, 30)}}
	require.True(t, s.ApplyFocusRevalidation(op, partial))
	v = mustThread(t, s, "R")
	assert.True(t, v.Focus.Gap.Stale)
	assert.Contains(t, threadPostIDs(v), deleted)
	// Covered.
	op, _ = s.BeginFocusRevalidate("R")
	full := Reread{High: &high, Pages: []model.PostList{upFrom(root, server, hi, hi-lo+1)}, Covered: true}
	require.True(t, s.ApplyFocusRevalidation(op, full))
	v = mustThread(t, s, "R")
	assert.False(t, v.Focus.Gap.Stale)
	assert.NotContains(t, threadPostIDs(v), deleted)
	i := slices.IndexFunc(v.Posts, func(p PostView) bool { return p.ID == replies[150].ID })
	assert.Equal(t, "edited offline", v.Posts[i].Message)

	// Dropped after a reconnect during it.
	s.MarkStale(7000)
	op, _ = s.BeginFocusRevalidate("R")
	s.MarkStale(8000)
	assert.False(t, s.ApplyFocusRevalidation(op, full))
}

// Self-review: another thread's page landing (a recent thread, trimmed on
// arrival) or its staleness hint leaves the open focus working.
func TestFocusSurvivesOtherThreadsEvents(t *testing.T) {
	s := crtFixture(true)
	other, otherReplies := seededThread("O", 100)
	epoch, _, _ := s.OpenThread("town", "O") // its page in flight…
	root, replies := seededThread("R", 300)
	focusThread(t, s, root, replies, 99) // …while R opens focused
	s.SetThreadPage("O", epoch, latestPage(other, otherReplies))
	s.MarkThreadStale("O")
	_, progressed := loadFocus(t, s, root, replies, true, ThreadPage)
	assert.True(t, progressed)
	_, progressed = loadFocus(t, s, root, replies, false, ThreadPage)
	assert.True(t, progressed)
	assert.Len(t, mustThread(t, s, "R").Posts, 1+61+120+60)

	// Opening another thread and coming back: a focus begun before (R plain,
	// nothing dropped) is not wanted any more.
	s.CloseThread()
	s.OpenThread("town", "R")
	op, _, _ := s.FocusThread("town", "R", replies[20].ID)
	s.OpenThread("town", "O")
	s.OpenThread("town", "R")
	assert.False(t, s.SetThreadFocus(op, replies[20], upFrom(root, replies, 20, 30), downFrom(root, replies, 20, 30)))
}

// Fix round 1, item 1: a jump to a held reply while a focus's pages are in
// flight is the newer navigation — the pending focus must not land.
func TestHeldRetargetDropsPendingFocus(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 300)
	openLoaded(t, s, root, replies)
	op, held, _ := s.FocusThread("town", "R", replies[20].ID)
	require.False(t, held)
	_, held, _ = s.FocusThread("town", "R", replies[280].ID)
	require.True(t, held)
	assert.False(t, s.SetThreadFocus(op, replies[20], upFrom(root, replies, 20, 30), downFrom(root, replies, 20, 30)))
	v := mustThread(t, s, "R")
	assert.Nil(t, v.Focus)
	assert.Contains(t, threadPostIDs(v), replies[280].ID)
}

// Fix round 1, item 3: rev moves only with data applied.
func TestFocusRevOnlyOnAppliedData(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 300)
	focusThread(t, s, root, replies, 99)
	s.MarkThreadStale("R") // the tail not live: the gap cannot close
	_, progressed := loadFocus(t, s, root, replies, true, 300)
	require.True(t, progressed)
	rev := mustThread(t, s, "R").Focus.Rev
	_, progressed = loadFocus(t, s, root, replies, true, 60) // empty, has_next=false again
	assert.False(t, progressed)
	assert.Equal(t, rev, mustThread(t, s, "R").Focus.Rev, "nothing applied")

	for _, p := range slices.Clone(s.threads["R"].focus.replies) {
		s.ApplyEvent(deletedEv(p))
	}
	s.MarkStale(9000)
	rev = mustThread(t, s, "R").Focus.Rev
	_, ok := s.BeginFocusRevalidate("R")
	assert.False(t, ok)
	v := mustThread(t, s, "R")
	assert.False(t, v.Focus.Gap.Stale)
	assert.Equal(t, rev, v.Focus.Rev)
}

// Fix round 1, item 4: closing the focus of a short thread (read whole
// meanwhile) leaves it complete — nothing older to load.
func TestShortThreadCompleteAfterFocusClosed(t *testing.T) {
	s := crtFixture(true)
	root, replies := seededThread("R", 50)
	op, held, ok := s.FocusThread("town", "R", replies[10].ID) // cold: nothing held
	require.True(t, ok)
	require.False(t, held)
	require.True(t, s.SetThreadFocus(op, replies[10], upFrom(root, replies, 10, 30), downFrom(root, replies, 10, 30)))
	epoch, _, _ := s.OpenThread("town", "R")
	s.SetThreadPage("R", epoch, latestPage(root, replies))
	s.CloseThread()
	s.OpenThread("town", "R")
	v := mustThread(t, s, "R")
	assert.Nil(t, v.Focus)
	assert.Len(t, v.Posts, 51)
	assert.False(t, v.HasMore)
}
