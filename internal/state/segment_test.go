package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// checkHistoryInvariantsLocked checks what can be checked locally of the
// history model (spec, Секция 1 «Инварианты»): continuity with the server is
// not provable here, so: s.older sorted and free of duplicates, no id both in
// s.older and in the window, and s.older never newer than the window's first
// post (equal create_at allowed) — required without a gap, and kept with one
// too (the feed shows s.older then the window).
func (s *Server) checkHistoryInvariantsLocked() error {
	seen := map[string]bool{}
	for i, p := range s.older {
		if seen[p.ID] {
			return fmt.Errorf("s.older: %s twice", p.ID)
		}
		seen[p.ID] = true
		if i > 0 && s.older[i-1].CreateAt > p.CreateAt {
			return fmt.Errorf("s.older: %s before %s is newer", s.older[i-1].ID, p.ID)
		}
	}
	ch := s.chans[s.active]
	if ch == nil {
		if len(s.older) > 0 || s.olderGap {
			return errors.New("history held without an open channel")
		}
		return nil
	}
	for _, p := range ch.Win.Posts {
		if seen[p.ID] {
			return fmt.Errorf("%s both in s.older and the window", p.ID)
		}
	}
	if n := len(s.older); n > 0 && len(ch.Win.Posts) > 0 && s.older[n-1].CreateAt > ch.Win.Posts[0].CreateAt {
		return fmt.Errorf("s.older ends at %s (%d), newer than the window's first %s (%d), gap=%v",
			s.older[n-1].ID, s.older[n-1].CreateAt, ch.Win.Posts[0].ID, ch.Win.Posts[0].CreateAt, s.olderGap)
	}
	return nil
}

func check(t *testing.T, s *Server) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NoError(t, s.checkHistoryInvariantsLocked())
}

// run: posts prefix<from>…prefix<from+n-1> of town by u2, the i-th at at0+10i.
func run(prefix string, from, n int, at0 int64) []model.Post {
	var out []model.Post
	for i := from; i < from+n; i++ {
		out = append(out, mkPost(fmt.Sprint(prefix, i), "town", "u2", at0+int64(i)*10))
	}
	return out
}

// plist is a server page: order newest first (as the server sends it), the
// given cursors.
func plist(prev, next string, posts ...model.Post) model.PostList {
	l := model.PostList{Posts: map[string]model.Post{}, PrevPostID: prev, NextPostID: next}
	sorted := slices.Clone(posts)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreateAt > sorted[j].CreateAt })
	for _, p := range sorted {
		l.Order = append(l.Order, p.ID)
		l.Posts[p.ID] = p
	}
	return l
}

func olderIDs(s *Server) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for _, p := range s.older {
		out = append(out, p.ID)
	}
	return out
}

func townIDs(t *testing.T, s *Server) []string {
	t.Helper()
	v, ok := s.ChannelView("town")
	require.True(t, ok)
	out := []string{}
	for _, p := range v.Posts {
		out = append(out, p.ID)
	}
	return out
}

func townView(t *testing.T, s *Server) ChannelView {
	t.Helper()
	v, ok := s.ChannelView("town")
	require.True(t, ok)
	return v
}

// segFixture: town open, its window w0…w(n-1) (at 10000+10i) loaded and
// live, nothing loaded above it.
func segFixture(t *testing.T, n int) *Server {
	t.Helper()
	s := newFixture()
	s.ClearGuard()
	s.SetActive("town")
	s.SetWindow("town", run("w", 0, n, 10_000), false, 5, 0)
	check(t, s)
	return s
}

// The segment around s5 (s_i at 1000+10i): before s0…s4 (more older),
// after s6…s10 (more newer).
func stdBefore() model.PostList { return plist("older", "s5", run("s", 0, 5, 1000)...) }
func stdTarget() model.Post     { return mkPost("s5", "town", "u2", 1050) }
func stdAfter() model.PostList  { return plist("s5", "s11", run("s", 6, 5, 1000)...) }

func jump(t *testing.T, s *Server, target model.Post, before, after model.PostList) {
	t.Helper()
	op, ok := s.BeginJump("town")
	require.True(t, ok)
	require.True(t, s.SetSegment(op, target, before, after))
	check(t, s)
}

func stdJump(t *testing.T, s *Server) { jump(t, s, stdTarget(), stdBefore(), stdAfter()) }

func sIDs(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprint("s", i))
	}
	return out
}

func wIDs(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprint("w", i))
	}
	return out
}

func TestSegmentReplacesHistoryAndOpensGap(t *testing.T) {
	s := segFixture(t, 10)
	appendOlder(t, s, "town", []model.Post{mkPost("o1", "town", "u2", 5000)}, false)
	before := townView(t, s)
	require.False(t, before.Gap.Open)

	stdJump(t, s)
	assert.Equal(t, sIDs(0, 10), olderIDs(s), "the history is the segment now")
	v := townView(t, s)
	assert.Equal(t, append(sIDs(0, 10), wIDs(0, 9)...), townIDs(t, s), "segment, then the window")
	assert.True(t, v.Gap.Open, "nothing proves the segment joins the window")
	assert.Equal(t, "w0", v.Gap.BeforeID)
	assert.Greater(t, v.Gap.Gen, before.Gap.Gen, "a new gap has a new generation")
	assert.False(t, v.Gap.Stale)
	assert.Greater(t, v.HistRev, before.HistRev)
	assert.True(t, v.HasMore, "before= said there is more (prev_post_id)")
	assert.Empty(t, v.GapAfter, "the reconnect gap is a different thing")

	s.mu.Lock()
	assert.Equal(t, Cursor{ID: "s0", CreateAt: 1000}, s.olderCursor)
	assert.Equal(t, Cursor{ID: "s10", CreateAt: 1100}, s.newerCursor)
	s.mu.Unlock()

	// before= at the channel's first post: nothing older.
	jump(t, s, stdTarget(), plist("", "s5", run("s", 0, 5, 1000)...), stdAfter())
	assert.False(t, townView(t, s).HasMore)
}

func TestSegmentAdjacentToWindowHasNoGap(t *testing.T) {
	t.Run("after= reaches a window post", func(t *testing.T) {
		s := segFixture(t, 10)
		after := plist("s5", "w2", append(run("s", 6, 5, 1000), run("w", 0, 2, 10_000)...)...)
		jump(t, s, stdTarget(), stdBefore(), after)
		assert.False(t, townView(t, s).Gap.Open)
		assert.Equal(t, sIDs(0, 10), olderIDs(s), "window posts stay in the window only")
		assert.Equal(t, wIDs(0, 9), windowIDs(s, "town"))
	})
	t.Run("server says nothing newer, window live", func(t *testing.T) {
		s := segFixture(t, 0) // loaded, empty
		jump(t, s, stdTarget(), stdBefore(), plist("s5", "", run("s", 6, 5, 1000)...))
		assert.False(t, townView(t, s).Gap.Open)
	})
	t.Run("server says nothing newer, window stale", func(t *testing.T) {
		s := segFixture(t, 0)
		s.MarkStale(6)
		jump(t, s, stdTarget(), stdBefore(), plist("s5", "", run("s", 6, 5, 1000)...))
		assert.True(t, townView(t, s).Gap.Open, "a stale window may miss what is newer")
	})
	t.Run("window not loaded", func(t *testing.T) {
		s := newFixture()
		s.SetActive("town")
		jump(t, s, stdTarget(), stdBefore(), plist("s5", "", run("s", 6, 5, 1000)...))
		assert.True(t, townView(t, s).Gap.Open)
	})
}

func TestBeginJumpAlwaysBumpsHistGen(t *testing.T) {
	s := segFixture(t, 10)
	olderOp, ok := s.BeginLoadOlder("town")
	require.True(t, ok)
	assert.Equal(t, "w0", olderOp.Cursor.ID, "no history yet: from the window's first post")

	first, ok := s.BeginJump("town")
	require.True(t, ok)
	assert.True(t, s.Holds("town", "w3"))
	// A jump to a held post makes no request, but it is a new navigation.
	_, ok = s.BeginJump("town")
	require.True(t, ok)

	s.AppendOlder(olderOp, plist("", "w0", mkPost("o1", "town", "u2", 5000)))
	assert.Empty(t, olderIDs(s), "LoadOlder begun before the jump is dropped")
	assert.False(t, s.SetSegment(first, stdTarget(), stdBefore(), stdAfter()), "the superseded jump too")
	assert.Empty(t, olderIDs(s))

	_, ok = s.BeginJump("off")
	assert.False(t, ok, "not the open channel")
	assert.False(t, s.Holds("off", "w3"))
	assert.False(t, s.Holds("town", "s5"))
}

func TestGapTrimDoesNotLeakIntoOlder(t *testing.T) {
	s := segFixture(t, WindowSize)
	stdJump(t, s)
	for _, p := range run("live", 0, 70, 50_000) {
		s.ApplyEvent(postedEv(p))
		check(t, s)
	}
	assert.Equal(t, sIDs(0, 10), olderIDs(s), "trimmed window posts would sit inside the gap: dropped")
	win := windowIDs(s, "town")
	require.Len(t, win, WindowSize)
	assert.Equal(t, "live10", win[0])
	v := townView(t, s)
	assert.True(t, v.Gap.Open)
	assert.Equal(t, "live10", v.Gap.BeforeID)
}

func TestAppendNewerClosesGapOnlyOnProof(t *testing.T) {
	s := segFixture(t, 10)
	stdJump(t, s)

	op, ok := s.BeginLoadNewer("town")
	require.True(t, ok)
	assert.Equal(t, "s10", op.Cursor.ID)
	closed, progressed := s.AppendNewer(op, plist("s10", "s16", run("s", 11, 5, 1000)...))
	check(t, s)
	assert.False(t, closed)
	assert.True(t, progressed)
	assert.Equal(t, sIDs(0, 15), olderIDs(s))

	// next_post_id "" proves nothing while the window may be behind.
	s.MarkStale(6)
	op, ok = s.BeginLoadNewer("town")
	require.True(t, ok)
	closed, progressed = s.AppendNewer(op, plist("s15", "", run("s", 16, 5, 1000)...))
	check(t, s)
	assert.False(t, closed, "window stale")
	assert.True(t, progressed)
	assert.True(t, townView(t, s).Gap.Open)
	s.MergeSince("town", nil, 7, 0)

	// A page with nothing to show still moves the raw cursor.
	filtered := run("s", 21, 2, 1000)
	for i := range filtered {
		filtered[i].OriginalID = "x" // edit-history rows
	}
	op, ok = s.BeginLoadNewer("town")
	require.True(t, ok)
	closed, progressed = s.AppendNewer(op, plist("s20", "s23", filtered...))
	check(t, s)
	assert.False(t, closed, "filtered posts are no proof")
	assert.True(t, progressed)
	assert.Equal(t, sIDs(0, 20), olderIDs(s))
	op, ok = s.BeginLoadNewer("town")
	require.True(t, ok)
	assert.Equal(t, "s22", op.Cursor.ID)

	// An empty page: no progress.
	closed, progressed = s.AppendNewer(op, plist("s22", "s23"))
	assert.False(t, closed)
	assert.False(t, progressed)

	// Reaching a window post closes the gap.
	op, ok = s.BeginLoadNewer("town")
	require.True(t, ok)
	closed, progressed = s.AppendNewer(op, plist("s22", "w2", append(run("s", 23, 3, 1000), run("w", 0, 2, 10_000)...)...))
	check(t, s)
	assert.True(t, closed)
	assert.True(t, progressed)
	assert.False(t, townView(t, s).Gap.Open)
	assert.Equal(t, append(sIDs(0, 20), sIDs(23, 25)...), olderIDs(s))
	assert.Equal(t, wIDs(0, 9), windowIDs(s, "town"))
	_, ok = s.BeginLoadNewer("town")
	assert.False(t, ok, "no gap left")
}

func TestAppendNewerKeepsUnknownIDAtWindowBoundary(t *testing.T) {
	s := segFixture(t, 10)
	stdJump(t, s)
	op, ok := s.BeginLoadNewer("town")
	require.True(t, ok)
	x := mkPost("x", "town", "u2", 10_000) // the window's first post's create_at, unknown id
	y := mkPost("y", "town", "u2", 10_005) // newer than w0, the window never had it
	page := plist("s10", "w2", x, mkPost("w0", "town", "u2", 10_000), y, mkPost("w1", "town", "u2", 10_010))
	closed, _ := s.AppendNewer(op, page)
	check(t, s)
	assert.True(t, closed)
	assert.Equal(t, append(sIDs(0, 10), "x"), olderIDs(s), "kept, not dropped as 'the window has it'")
	assert.Equal(t, append([]string{"w0", "y"}, wIDs(1, 9)...), windowIDs(s, "town"), "y went to the window")
}

func TestAppendNewerFromOldGapGenIsDropped(t *testing.T) {
	s := segFixture(t, 10)
	stdJump(t, s)
	op, ok := s.BeginLoadNewer("town")
	require.True(t, ok)
	// A catch-up overflow reloads the latest page: a new gap.
	s.SetWindow("town", run("n", 0, 5, 90_000), false, 9, 0)
	check(t, s)
	closed, progressed := s.AppendNewer(op, plist("s10", "", append(run("s", 11, 2, 1000), run("w", 0, 2, 10_000)...)...))
	assert.False(t, closed)
	assert.False(t, progressed)
	assert.Equal(t, sIDs(0, 10), olderIDs(s), "nothing applied")
	assert.True(t, townView(t, s).Gap.Open)
}

func TestMarkStaleInvalidatesInFlightLoadNewer(t *testing.T) {
	// An empty live window: "nothing newer" alone would close the gap.
	s := segFixture(t, 0)
	stdJump(t, s)
	op, ok := s.BeginLoadNewer("town")
	require.True(t, ok)
	s.MarkStale(6)
	s.MergeSince("town", nil, 7, 0) // the window is live again
	page := plist("s10", "", run("s", 11, 2, 1000)...)
	closed, progressed := s.AppendNewer(op, page)
	assert.False(t, closed, "begun before the reconnect")
	assert.False(t, progressed)
	assert.Equal(t, sIDs(0, 10), olderIDs(s))
	assert.True(t, townView(t, s).Gap.Open)

	op, ok = s.BeginLoadNewer("town")
	require.True(t, ok)
	closed, _ = s.AppendNewer(op, page)
	check(t, s)
	assert.True(t, closed, "sanity: the same answer to a request begun now closes it")
}

func TestAppendOlderUsesRawCursor(t *testing.T) {
	s := segFixture(t, 10)
	f0 := mkPost("f0", "town", "u2", 990)
	f0.OriginalID = "x"
	jump(t, s, stdTarget(), plist("older", "s5", append([]model.Post{f0}, run("s", 1, 4, 1000)...)...), stdAfter())
	assert.Equal(t, sIDs(1, 10), olderIDs(s))

	op, ok := s.BeginLoadOlder("town")
	require.True(t, ok)
	assert.Equal(t, Cursor{ID: "f0", CreateAt: 990}, op.Cursor, "the oldest raw post, not the oldest shown")
	g0, g1 := mkPost("g0", "town", "u2", 800), mkPost("g1", "town", "u2", 810)
	g0.OriginalID, g1.OriginalID = "x", "x"
	s.AppendOlder(op, plist("more", "f0", g0, g1))
	check(t, s)
	assert.Equal(t, sIDs(1, 10), olderIDs(s))
	op, ok = s.BeginLoadOlder("town")
	require.True(t, ok)
	assert.Equal(t, "g0", op.Cursor.ID, "a page with nothing to show still moves the cursor")
	assert.True(t, townView(t, s).HasMore)

	s.AppendOlder(op, plist("", "g0", mkPost("o", "town", "u2", 500)))
	check(t, s)
	assert.Equal(t, append([]string{"o"}, sIDs(1, 10)...), olderIDs(s))
	assert.False(t, townView(t, s).HasMore)
	s.AppendOlder(op, plist("", "g0", mkPost("o", "town", "u2", 500)))
	assert.Equal(t, append([]string{"o"}, sIDs(1, 10)...), olderIDs(s), "a twin of an applied page: its cursor is gone")
}

func TestMergeSinceUpdatesSegmentCopies(t *testing.T) {
	s := segFixture(t, 10)
	stdJump(t, s)
	edited := mkPost("s3", "town", "u2", 1030)
	edited.Message, edited.EditAt, edited.UpdateAt = "edited", 50_000, 50_000
	deleted := mkPost("s4", "town", "u2", 1040)
	deleted.DeleteAt, deleted.UpdateAt = 50_000, 50_000
	inGap := mkPost("n1", "town", "u2", 5000)   // between the segment and the window
	newer := mkPost("n2", "town", "u2", 20_000) // after the window
	stale := mkPost("s6", "town", "u2", 1060)   // a row read earlier than the copy held
	stale.Message, stale.UpdateAt = "old", 1000 // (update_at below the copy's)
	s.MergeSince("town", []model.Post{edited, deleted, inGap, newer, stale}, 9, 0)
	check(t, s)

	assert.Equal(t, append(sIDs(0, 3), sIDs(5, 10)...), olderIDs(s), "s4 deleted, n1 not added to the segment")
	v := townView(t, s)
	byID := map[string]PostView{}
	for _, p := range v.Posts {
		byID[p.ID] = p
	}
	assert.Equal(t, "edited", byID["s3"].Message)
	assert.Equal(t, "msg s6", byID["s6"].Message, "newerOf keeps the newer copy")
	assert.NotContains(t, byID, "n1", "a post inside the gap waits for the gap to be loaded")
	assert.Contains(t, windowIDs(s, "town"), "n2")
	assert.True(t, v.Gap.Open)
}

func TestSetWindowOverflowKeepsSegmentAsStale(t *testing.T) {
	t.Run("browsed history", func(t *testing.T) {
		s := segFixture(t, 10)
		appendOlder(t, s, "town", []model.Post{mkPost("o1", "town", "u2", 5000)}, true)
		gen := townView(t, s).Gap.Gen
		s.SetWindow("town", run("n", 0, 5, 90_000), false, 9, 0)
		check(t, s)
		v := townView(t, s)
		assert.Equal(t, append(append([]string{"o1"}, wIDs(0, 9)...), "n0", "n1", "n2", "n3", "n4"), townIDs(t, s),
			"the reader is not thrown down: the old window joins the history (R9a)")
		assert.True(t, v.Gap.Open)
		assert.Greater(t, v.Gap.Gen, gen)
		assert.True(t, v.Gap.Stale, "edits in the history may have been missed")
		assert.Equal(t, "n0", v.Gap.BeforeID)
		assert.False(t, v.HasMore, "the history still reaches the first post")
		op, ok := s.BeginLoadNewer("town")
		require.True(t, ok)
		assert.Equal(t, "w9", op.Cursor.ID, "the gap is loaded from the newest held post")
	})
	t.Run("segment", func(t *testing.T) {
		s := segFixture(t, 10)
		stdJump(t, s)
		gen := townView(t, s).Gap.Gen
		s.SetWindow("town", run("n", 0, 5, 90_000), false, 9, 0)
		check(t, s)
		v := townView(t, s)
		assert.Equal(t, sIDs(0, 10), olderIDs(s))
		assert.True(t, v.Gap.Open)
		assert.Greater(t, v.Gap.Gen, gen)
		assert.True(t, v.Gap.Stale)
	})
	t.Run("overlapping page keeps the gap state", func(t *testing.T) {
		s := segFixture(t, 10)
		stdJump(t, s)
		gen := townView(t, s).Gap.Gen
		s.SetWindow("town", run("w", 0, 11, 10_000), false, 9, 0)
		check(t, s)
		v := townView(t, s)
		assert.Equal(t, gen, v.Gap.Gen)
		assert.True(t, v.Gap.Open)
		assert.False(t, v.Gap.Stale)
	})
	t.Run("window loaded after the jump", func(t *testing.T) {
		s := newFixture()
		s.SetActive("town")
		stdJump(t, s)
		s.SetWindow("town", run("w", 0, 10, 10_000), false, 9, 0)
		check(t, s)
		v := townView(t, s)
		assert.True(t, v.Gap.Open)
		assert.False(t, v.Gap.Stale, "the first page of the window, not an overflow")
		assert.Equal(t, append(sIDs(0, 10), wIDs(0, 9)...), townIDs(t, s))
	})
	t.Run("window loaded after the jump reaches the segment", func(t *testing.T) {
		s := newFixture()
		s.SetActive("town")
		stdJump(t, s)
		s.SetWindow("town", append(run("s", 9, 2, 1000), run("w", 0, 10, 10_000)...), false, 9, 0)
		check(t, s)
		assert.False(t, townView(t, s).Gap.Open, "the window holds a segment post: joined")
		assert.Equal(t, sIDs(0, 8), olderIDs(s))
	})
}

// staleSegment: the standard segment, then a catch-up overflow made it stale.
func staleSegment(t *testing.T) *Server {
	t.Helper()
	s := segFixture(t, 10)
	stdJump(t, s)
	s.SetWindow("town", run("n", 0, 5, 90_000), false, 9, 0)
	require.True(t, townView(t, s).Gap.Stale)
	return s
}

func segPost(t *testing.T, s *Server, id string) model.Post {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	i := indexOf(s.older, id)
	require.GreaterOrEqual(t, i, 0, id)
	return s.older[i]
}

func TestRevalidationDropsOnlyWhenCovered(t *testing.T) {
	without := func(ps []model.Post, ids ...string) []model.Post {
		return slices.DeleteFunc(ps, func(p model.Post) bool { return slices.Contains(ids, p.ID) })
	}
	t.Run("partial", func(t *testing.T) {
		s := staleSegment(t)
		op, ok := s.BeginRevalidate("town")
		require.True(t, ok)
		assert.Equal(t, Cursor{ID: "s0", CreateAt: 1000}, op.Low)
		assert.Equal(t, Cursor{ID: "s10", CreateAt: 1100}, op.High)
		assert.Len(t, op.Held, 11)
		high := mkPost("s10", "town", "u2", 1100)
		s.ApplyRevalidation(op, Reread{High: &high, Pages: []model.PostList{plist("s5", "s10", without(run("s", 5, 5, 1000), "s7")...)}})
		check(t, s)
		assert.Equal(t, sIDs(0, 10), olderIDs(s), "not covered: nothing removed")
		assert.True(t, townView(t, s).Gap.Stale)
	})
	t.Run("covered", func(t *testing.T) {
		s := staleSegment(t)
		op, ok := s.BeginRevalidate("town")
		require.True(t, ok)
		fresh := mkPost("s2", "town", "u2", 1020)
		fresh.Message, fresh.EditAt, fresh.UpdateAt = "edited offline", 40_000, 40_000
		page := append(without(run("s", 0, 10, 1000), "s0", "s2", "s3"), fresh)
		rev := townView(t, s).HistRev
		// The first post and the cursor post (high) were deleted offline, s3 too.
		s.ApplyRevalidation(op, Reread{HighGone: true, Pages: []model.PostList{plist("", "s10", page...)}, Covered: true})
		check(t, s)
		assert.Equal(t, append(sIDs(1, 2), sIDs(4, 9)...), olderIDs(s))
		assert.Equal(t, "edited offline", segPost(t, s, "s2").Message)
		v := townView(t, s)
		assert.False(t, v.Gap.Stale)
		assert.True(t, v.Gap.Open, "stale and gap are separate")
		assert.Greater(t, v.HistRev, rev)
		_, ok = s.BeginRevalidate("town")
		assert.False(t, ok, "nothing left to revalidate")
	})
	t.Run("live edits during the reread win", func(t *testing.T) {
		s := staleSegment(t)
		op, ok := s.BeginRevalidate("town")
		require.True(t, ok)
		e3 := mkPost("s3", "town", "u2", 1030)
		e3.Message, e3.EditAt, e3.UpdateAt = "live", 60_000, 60_000
		s.ApplyPostUpdate(e3) // the page below was read before it: s3 was missing then (lagging replica)
		e6 := mkPost("s6", "town", "u2", 1060)
		e6.Message, e6.EditAt, e6.UpdateAt = "live", 60_000, 60_000
		s.ApplyPostUpdate(e6)
		high := mkPost("s10", "town", "u2", 1100)
		s.ApplyRevalidation(op, Reread{High: &high, Pages: []model.PostList{plist("", "s10", without(run("s", 0, 10, 1000), "s3")...)}, Covered: true})
		check(t, s)
		assert.Equal(t, sIDs(0, 10), olderIDs(s), "s3 changed since the start: not removed")
		assert.Equal(t, "live", segPost(t, s, "s6").Message, "newerOf")
		assert.False(t, townView(t, s).Gap.Stale)
	})
	t.Run("same create_at as high", func(t *testing.T) {
		s := segFixture(t, 10)
		tie := mkPost("t10", "town", "u2", 1100)
		jump(t, s, stdTarget(), stdBefore(), plist("s5", "s11", append(run("s", 6, 5, 1000), tie)...))
		s.SetWindow("town", run("n", 0, 5, 90_000), false, 9, 0)
		op, ok := s.BeginRevalidate("town")
		require.True(t, ok)
		other := "t10"
		if op.High.ID == "t10" {
			other = "s10"
		}
		high := mkPost(op.High.ID, "town", "u2", 1100)
		// before=high never returns a post with high's own create_at.
		s.ApplyRevalidation(op, Reread{High: &high, Pages: []model.PostList{plist("", op.High.ID, run("s", 0, 10, 1000)...)}, Covered: true})
		assert.Contains(t, olderIDs(s), other, "not covered by before=high: kept")
	})
	t.Run("superseded", func(t *testing.T) {
		s := staleSegment(t)
		op, ok := s.BeginRevalidate("town")
		require.True(t, ok)
		stdJump(t, s)
		s.ApplyRevalidation(op, Reread{HighGone: true, Pages: []model.PostList{plist("", "s10")}, Covered: true})
		assert.Equal(t, sIDs(0, 10), olderIDs(s), "a new jump drops the old reread")
	})
}

func TestResetsClearSegmentAtomically(t *testing.T) {
	resets := map[string]func(s *Server){
		"other channel": func(s *Server) { s.SetActive("off") },
		"A→B→A":         func(s *Server) { s.SetActive("off"); s.SetActive("town") },
		"ResetWindows":  func(s *Server) { s.ResetWindows() },
		"forget": func(s *Server) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.forgetChannelLocked("town")
		},
	}
	for name, reset := range resets {
		t.Run(name, func(t *testing.T) {
			s := staleSegment(t)
			op, ok := s.BeginLoadNewer("town")
			require.True(t, ok)
			s.mu.Lock()
			hist, gap, rev := s.histGen, s.gapGen, s.histRev
			s.mu.Unlock()

			reset(s)
			s.mu.Lock()
			assert.Empty(t, s.older)
			assert.False(t, s.olderGap)
			assert.False(t, s.segStale)
			assert.False(t, s.olderComplete)
			assert.Zero(t, s.olderCursor)
			assert.Zero(t, s.newerCursor)
			assert.Greater(t, s.histGen, hist)
			assert.Greater(t, s.gapGen, gap)
			assert.Greater(t, s.histRev, rev)
			s.mu.Unlock()
			closed, progressed := s.AppendNewer(op, plist("s10", "", run("s", 11, 2, 1000)...))
			assert.False(t, closed)
			assert.False(t, progressed)
			assert.Empty(t, olderIDs(s))
		})
	}
}

func TestEmptyWindowKeepsGapOpen(t *testing.T) {
	s := segFixture(t, 3)
	stdJump(t, s)
	for _, id := range wIDs(0, 2) {
		s.RemovePost(id)
		check(t, s)
	}
	v := townView(t, s)
	assert.True(t, v.Gap.Open, "an empty window proves nothing")
	assert.Empty(t, v.Gap.BeforeID)
	assert.Equal(t, sIDs(0, 10), townIDs(t, s))
}

func TestLiveEventsReachSegment(t *testing.T) {
	s := segFixture(t, 10)
	before := run("s", 0, 5, 1000)
	before[1].UserID = "u3"
	before[2].UserID = "u9" // not loaded
	jump(t, s, stdTarget(), plist("older", "s5", before...), stdAfter())

	e := mkPost("s3", "town", "u2", 1030)
	e.Message, e.EditAt, e.UpdateAt = "edited", 50_000, 50_000
	s.ApplyEvent(postEv("post_edited", e))
	s.ApplyEvent(townEv("reaction_added", model.Reaction{UserID: "u2", PostID: "s4", EmojiName: "tada", CreateAt: 50_000}, "reaction"))
	s.ApplyEvent(postedEv(reply("r1", "s6", "u2", 60_000, 1)))
	s.ApplyEvent(postEv("post_deleted", mkPost("s7", "town", "u2", 1070)))
	check(t, s)

	byID := map[string]PostView{}
	for _, p := range townView(t, s).Posts {
		byID[p.ID] = p
	}
	assert.Equal(t, "edited", byID["s3"].Message)
	require.Len(t, byID["s4"].Reactions, 1)
	assert.Equal(t, "tada", byID["s4"].Reactions[0].Emoji)
	assert.Equal(t, int64(1), byID["s6"].ReplyCount)
	assert.NotContains(t, byID, "s7")
	assert.Contains(t, s.StatusTargets(100), "u3", "segment authors are polled")
	assert.Contains(t, s.MissingUserIDs(), "u9", "and loaded")
	c, ok := s.heldChannelOf("s8")
	assert.True(t, ok)
	assert.Equal(t, "town", c)
}

// heldChannelOf is channelOfPostLocked under the lock.
func (s *Server) heldChannelOf(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channelOfPostLocked(id)
}

func TestHistRevBumpsOnEveryAppliedPage(t *testing.T) {
	s := segFixture(t, 10)
	rev := func() uint64 { return townView(t, s).HistRev }
	r := rev()
	stdJump(t, s)
	assert.Greater(t, rev(), r, "SetSegment")

	r = rev()
	op, _ := s.BeginLoadNewer("town")
	s.AppendNewer(op, plist("s10", "s12", mkPost("s11", "town", "u2", 1110)))
	assert.Greater(t, rev(), r, "AppendNewer")

	r = rev()
	older, _ := s.BeginLoadOlder("town")
	s.AppendOlder(older, plist("more", "s0", mkPost("o", "town", "u2", 900)))
	assert.Greater(t, rev(), r, "AppendOlder")

	r = rev()
	s.AppendOlder(older, plist("more", "s0", mkPost("o", "town", "u2", 900)))
	assert.Equal(t, r, rev(), "a dropped page changes nothing")

	s.SetWindow("town", run("n", 0, 5, 90_000), false, 9, 0)
	r = rev()
	rop, ok := s.BeginRevalidate("town")
	require.True(t, ok)
	s.ApplyRevalidation(rop, Reread{})
	assert.Greater(t, rev(), r, "ApplyRevalidation")
}

// appendOlder loads a page of history the way the worker does: begin (the
// cursor), then the page from it.
func appendOlder(t *testing.T, s *Server, ch string, posts []model.Post, complete bool) bool {
	t.Helper()
	op, ok := s.BeginLoadOlder(ch)
	if !ok {
		return false
	}
	prev := "more"
	if complete {
		prev = ""
	}
	s.AppendOlder(op, plist(prev, op.Cursor.ID, posts...))
	check(t, s)
	return true
}

func TestGapViewJSON(t *testing.T) {
	s := segFixture(t, 3)
	stdJump(t, s)
	b, err := json.Marshal(townView(t, s))
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	var gap map[string]any
	require.NoError(t, json.Unmarshal(m["gap"], &gap))
	assert.Equal(t, true, gap["open"])
	assert.Equal(t, "w0", gap["before_id"])
	assert.Contains(t, gap, "gen")
	assert.Contains(t, gap, "stale")
	assert.Contains(t, m, "hist_rev")
}

// R9a: an overflow while the gap was closed keeps the old window — it is
// contiguous with the history — as history, up to where the window was
// live (GapAfter): posts after it arrived across a lost stream.
func TestOverflowKeepsOldWindowAsHistory(t *testing.T) {
	s := segFixture(t, 10)
	appendOlder(t, s, "town", []model.Post{mkPost("o1", "town", "u2", 5000)}, true)
	s.MarkStale(9)
	s.ApplyEvent(postedEv(mkPost("x", "town", "u2", 20_000))) // after the stream loss: not contiguous
	check(t, s)
	s.SetWindow("town", run("n", 0, 5, 90_000), false, 9, 0)
	check(t, s)
	assert.Equal(t, append([]string{"o1"}, wIDs(0, 9)...), olderIDs(s))
	v := townView(t, s)
	assert.True(t, v.Gap.Open)
	assert.True(t, v.Gap.Stale)
	op, ok := s.BeginLoadNewer("town")
	require.True(t, ok)
	assert.Equal(t, "w9", op.Cursor.ID)
}

// R9b: with the gap open and the window empty, a since= row of a post not
// held and created before the window's sync point is an edit of a post
// inside the gap — it does not enter the window, so it cannot pass for an
// intersection proof when the gap is loaded.
func TestSinceRowInsideGapStaysOutOfEmptyWindow(t *testing.T) {
	s := segFixture(t, 3)
	stdJump(t, s)
	for _, id := range wIDs(0, 2) {
		s.RemovePost(id)
	}
	s.MergeSince("town", nil, 20_000, 0)
	stray := mkPost("s20", "town", "u2", 1200)
	stray.EditAt, stray.UpdateAt, stray.Message = 25_000, 25_000, "edited in the gap"
	fresh := mkPost("n0", "town", "u2", 30_000)
	s.MergeSince("town", []model.Post{stray, fresh}, 31_000, 0)
	check(t, s)
	assert.Equal(t, append(sIDs(0, 10), "n0"), townIDs(t, s), "the edit inside the gap waits for the gap's loading")
	op, ok := s.BeginLoadNewer("town")
	require.True(t, ok)
	closed, progressed := s.AppendNewer(op, plist("s10", "s21", append(run("s", 11, 9, 1000), stray)...))
	check(t, s)
	assert.True(t, progressed)
	assert.False(t, closed, "s20 was never in the window: no intersection")
	assert.True(t, townView(t, s).Gap.Open)
}

// R9c: a post removed by a covered revalidation goes into the gone ring: a
// page read before (a lagging replica) cannot bring it back.
func TestRevalidationRemovalsAreGone(t *testing.T) {
	s := staleSegment(t)
	op, ok := s.BeginRevalidate("town")
	require.True(t, ok)
	high := mkPost("s10", "town", "u2", 1100)
	page := slices.DeleteFunc(run("s", 0, 10, 1000), func(p model.Post) bool { return p.ID == "s3" })
	require.True(t, s.ApplyRevalidation(op, Reread{High: &high, Pages: []model.PostList{plist("", "s10", page...)}, Covered: true}))
	assert.NotContains(t, olderIDs(s), "s3")
	stdJump(t, s) // its pages still hold s3
	assert.NotContains(t, olderIDs(s), "s3", "a removed post is not resurrected")
}

// Fix round 1, item 2: a held post sharing a page cursor's create_at can lie
// beyond that page's limit, and before=cursor never returns it: kept.
func TestRevalidationKeepsTiesAtPageBounds(t *testing.T) {
	s := segFixture(t, 10)
	tie := mkPost("t4", "town", "u2", 1040) // s4's create_at
	jump(t, s, stdTarget(), plist("older", "s5", append(run("s", 0, 5, 1000), tie)...), stdAfter())
	s.SetWindow("town", run("n", 0, 5, 90_000), false, 9, 0)
	op, ok := s.BeginRevalidate("town")
	require.True(t, ok)
	require.Len(t, op.Posts, 12)
	assert.Equal(t, op.Low, op.Posts[0])
	assert.Equal(t, op.High, op.Posts[11])
	high := mkPost("s10", "town", "u2", 1100)
	first := plist("s4", "s10", run("s", 4, 6, 1000)...) // the page's limit cut t4 off
	second := plist("", "s4", run("s", 0, 4, 1000)...)   // before=s4: create_at < 1040
	require.True(t, s.ApplyRevalidation(op, Reread{High: &high, Pages: []model.PostList{first, second},
		Bounds: []Cursor{{ID: "s4", CreateAt: 1040}}, Covered: true}))
	check(t, s)
	assert.Contains(t, olderIDs(s), "t4")
	assert.False(t, townView(t, s).Gap.Stale)
}

// Codex review, finding 1: a jump whose pages overlap the held history
// keeps the fresher local copy of a post edited live after its pages were
// read — the pages are merged with the held copies (newerOf) before the
// held history is replaced.
func TestOverlappingJumpKeepsLiveEdit(t *testing.T) {
	s := segFixture(t, 60)
	stdJump(t, s) // s0…s10 held
	op, ok := s.BeginJump("town")
	require.True(t, ok)
	before := plist("s0", "s11", run("s", 6, 5, 1000)...) // s6…s10, read before the edit
	target := mkPost("s11", "town", "u2", 1110)
	after := plist("s11", "s20", run("s", 12, 5, 1000)...)
	edited := segPost(t, s, "s8")
	edited.Message, edited.EditAt, edited.UpdateAt = "live edit", 99_000, 99_000
	s.ApplyPostUpdate(edited)
	require.True(t, s.SetSegment(op, target, before, after))
	check(t, s)
	assert.Equal(t, "live edit", segPost(t, s, "s8").Message)
	assert.Equal(t, sIDs(6, 16), olderIDs(s))
}

// Codex review, finding 2: a jump's pages read before a reconnect may miss
// what the lost stream carried (the catch-up updated only the copies held
// then). The segment is applied, but stale — to be reread — not fresh.
func TestSegmentReadBeforeReconnectIsStale(t *testing.T) {
	t.Run("answer before the catch-up", func(t *testing.T) {
		s := segFixture(t, 60)
		op, ok := s.BeginJump("town")
		require.True(t, ok)
		s.MarkStale(20_000)
		require.True(t, s.SetSegment(op, stdTarget(), stdBefore(), stdAfter()))
		check(t, s)
		assert.True(t, s.SegmentStale("town"), "an old gap generation must not become a fresh segment")
		assert.True(t, townView(t, s).Gap.Stale)
	})
	t.Run("catch-up completed before the answer", func(t *testing.T) {
		s := segFixture(t, 60)
		op, ok := s.BeginJump("town")
		require.True(t, ok)
		s.MarkStale(20_000)
		s.MergeSince("town", nil, 20_001, 0) // caught up: the window is live again
		require.False(t, townView(t, s).Syncing)
		require.True(t, s.SetSegment(op, stdTarget(), stdBefore(), stdAfter()))
		check(t, s)
		assert.True(t, s.SegmentStale("town"))
		assert.Equal(t, sIDs(0, 10), olderIDs(s))

		// A jump begun now is fresh again.
		jump(t, s, stdTarget(), stdBefore(), stdAfter())
		assert.False(t, s.SegmentStale("town"))
	})
}
