package state

import (
	"slices"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// The held history of the open channel (spec «Поиск», Секция 1): s.older is
// one piece of the channel continuous on the server — history scrolled up to
// above the window, or the segment around a post jumped to. olderGap: the
// piece may not join the window (nothing proved it does yet); the hole is
// only ever between s.older and the window, and it is closed only by a
// proof: a page holding a post the window holds, or the server saying there
// is nothing newer while the window is live.
//
// Every history operation (JumpTo, LoadOlder, LoadNewer, a reread) captures
// a HistOp under the lock before its request and its page is applied only
// if the op still matches: histGen moves with every navigation and reset,
// gapGen with every gap opened and every reconnect (a LoadNewer begun
// before cannot close the new gap), the window generation and CRT with
// ResetWindows, the cursor with every page applied in its direction.

// Cursor is a raw post a page continues from: the oldest (before=) or
// newest (after=) post of the last page applied in that direction as the
// server sent it — shown or filtered out — so a page with nothing to show
// still moves on.
type Cursor struct {
	ID       string
	CreateAt int64
}

func cursorOf(p model.Post) Cursor { return Cursor{ID: p.ID, CreateAt: p.CreateAt} }

// HistOp is a history operation's capture: the channel and the generations
// (history, gap, window), the CRT mode and the cursor it was begun with.
type HistOp struct {
	Channel string
	Hist    uint64
	Gap     uint64
	Win     uint64
	CRT     bool
	Cursor  Cursor
}

// RevalOp is a reread of a stale segment: the held range from its oldest
// (Low) to its newest (High) post, the held posts oldest first (Posts: a
// reread whose cursor vanished continues from the next held one) and each
// held post's update_at, all as of the start.
type RevalOp struct {
	HistOp
	Low, High Cursor
	Posts     []Cursor
	Held      map[string]int64
}

// Reread is what a reread of a RevalOp found: High as GET /posts/{id}
// returns it (HighGone: 404), the before= pages and the posts it read one
// by one (Pages), and the cursor of every before= page (Bounds, High
// included): a held post sharing a cursor's create_at can lie beyond a
// page's limit, never returned — it is kept. Covered: the pages reach past
// Low or to the channel's first post from a cursor known to exist.
type Reread struct {
	High     *model.Post
	HighGone bool
	Pages    []model.PostList
	Bounds   []Cursor
	Covered  bool
}

func (s *Server) histOpLocked(channelID string) HistOp {
	return HistOp{Channel: channelID, Hist: s.histGen, Gap: s.gapGen, Win: s.winGen, CRT: s.crtLocked()}
}

// histCurrentLocked: op still describes the open channel's history (same
// channel, navigation, window generation and CRT mode).
func (s *Server) histCurrentLocked(op HistOp) (*Chan, bool) {
	ch := s.chans[op.Channel]
	if ch == nil || op.Channel != s.active || op.Hist != s.histGen || op.Win != s.winGen || op.CRT != s.crtLocked() {
		return nil, false
	}
	return ch, true
}

// historyHeldLocked: something was loaded above the open channel's window —
// posts, the fact that nothing older exists, a cursor past filtered posts,
// or a segment behind a gap.
func (s *Server) historyHeldLocked() bool {
	return len(s.older) > 0 || s.olderComplete || s.olderGap || s.olderCursor.ID != ""
}

// resetHistoryLocked drops the held history at once with everything that
// describes it, and invalidates the operations in flight.
func (s *Server) resetHistoryLocked() {
	s.older, s.olderComplete, s.olderGap, s.segStale = nil, false, false, false
	s.olderCursor, s.newerCursor = Cursor{}, Cursor{}
	s.histGen++
	s.gapGen++
	s.histRev++
}

// rawPosts: the posts of pages as the server listed them (Order), oldest
// first, each id once.
func rawPosts(lists ...model.PostList) []model.Post {
	var out []model.Post
	seen := map[string]bool{}
	for _, l := range lists {
		for _, p := range l.Ascending() {
			if !seen[p.ID] {
				seen[p.ID] = true
				out = append(out, p)
			}
		}
	}
	sortPosts(out)
	return out
}

// joinsWindowLocked: a page of raw posts up to newest proves the history
// reaches the window — it holds a post the window holds, or the server has
// nothing newer (next) and the window is live. The latter also needs the
// window not to start past the page: posts that arrived while the page was
// in flight may have pushed the window's first post out.
func joinsWindowLocked(ch *Chan, raw []model.Post, next string, newest Cursor) bool {
	for _, p := range raw {
		if indexOf(ch.Win.Posts, p.ID) >= 0 {
			return true
		}
	}
	w := ch.Win
	return next == "" && w.Loaded && !w.Stale && (len(w.Posts) == 0 || w.Posts[0].CreateAt <= newest.CreateAt)
}

// placeLocked merges raw page posts by id: a post the window holds stays
// there; one held in s.older is refreshed (newerOf); a new one goes to
// s.older when it is not newer than the window's first post (an unknown id
// at that very create_at included), else to the window, which missed it.
func (s *Server) placeLocked(ch *Chan, raw []model.Post, crt bool) {
	var add, win []model.Post
	for _, p := range raw {
		if !keep(p, crt) || s.gone.has(p.ID) || indexOf(ch.Win.Posts, p.ID) >= 0 {
			continue
		}
		if i := indexOf(s.older, p.ID); i >= 0 {
			s.older[i] = newerOf(p, s.older[i])
			continue
		}
		if len(ch.Win.Posts) == 0 || p.CreateAt <= ch.Win.Posts[0].CreateAt {
			add = append(add, p)
		} else {
			win = append(win, p)
		}
	}
	s.older = append(s.older, add...)
	sortPosts(s.older)
	for _, p := range win {
		s.upsertLocked(ch, p)
	}
}

// Holds reports whether the open channel shows postID now — in the window
// or in the held history.
func (s *Server) Holds(channelID, postID string) bool {
	_, ok := s.HeldPost(channelID, postID)
	return ok
}

// HeldPost is the post Holds finds.
func (s *Server) HeldPost(channelID, postID string) (model.Post, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil || channelID != s.active {
		return model.Post{}, false
	}
	if i := indexOf(ch.Win.Posts, postID); i >= 0 {
		return ch.Win.Posts[i], true
	}
	if i := indexOf(s.older, postID); i >= 0 {
		return s.older[i], true
	}
	return model.Post{}, false
}

// SegmentStale reports that the open channel's held history waits for a reread
// (BeginRevalidate).
func (s *Server) SegmentStale(channelID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return channelID == s.active && s.segStale
}

// BeginJump starts a jump to a post of the open channel. It is a new
// navigation whatever follows — even a post already held — so every history
// operation begun before is invalidated.
func (s *Server) BeginJump(channelID string) (HistOp, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.histGen++
	if s.chans[channelID] == nil || channelID != s.active {
		return HistOp{}, false
	}
	return s.histOpLocked(channelID), true
}

// SetSegment replaces the held history with the segment around target
// (before=/after= pages from it). The gap stays open unless the segment is
// proved to reach the window (joinsWindowLocked) at this moment.
func (s *Server) SetSegment(op HistOp, target model.Post, before, after model.PostList) (applied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.histCurrentLocked(op)
	if !ok {
		return false
	}
	raw := rawPosts(before, model.PostList{Order: []string{target.ID}, Posts: map[string]model.Post{target.ID: target}}, after)
	lo, hi := cursorOf(raw[0]), cursorOf(raw[len(raw)-1])
	s.older, s.olderComplete, s.segStale = nil, before.PrevPostID == "", false
	s.olderGap = !joinsWindowLocked(ch, raw, after.NextPostID, hi)
	if s.olderGap {
		s.gapGen++
	}
	s.olderCursor, s.newerCursor = lo, hi
	s.placeLocked(ch, raw, op.CRT)
	s.histRev++
	return true
}

// BeginLoadNewer starts loading the gap from the history's raw after-cursor;
// false without a gap.
func (s *Server) BeginLoadNewer(channelID string) (HistOp, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chans[channelID] == nil || channelID != s.active || !s.olderGap || s.newerCursor.ID == "" {
		return HistOp{}, false
	}
	op := s.histOpLocked(channelID)
	op.Cursor = s.newerCursor
	return op, true
}

// AppendNewer applies an after= page into the gap begun with op — the same
// gap, not one opened since (gapGen). Merged by id (placeLocked); the gap
// closes only on a proof checked now (joinsWindowLocked). progressed: the
// raw cursor moved.
func (s *Server) AppendNewer(op HistOp, page model.PostList) (closed, progressed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.histCurrentLocked(op)
	if !ok || op.Gap != s.gapGen || !s.olderGap || op.Cursor != s.newerCursor {
		return false, false
	}
	raw := rawPosts(page)
	newest := op.Cursor
	if len(raw) > 0 {
		newest = cursorOf(raw[len(raw)-1])
	}
	// The proof is taken before merging: with the gap closed, window posts
	// the merge pushes out move to s.older instead of being dropped.
	closed = joinsWindowLocked(ch, raw, page.NextPostID, newest)
	s.olderGap = !closed
	s.placeLocked(ch, raw, op.CRT)
	progressed = newest != s.newerCursor
	s.newerCursor = newest
	s.histRev++
	return closed, progressed
}

// olderCursorLocked: where LoadOlder continues from — the raw cursor of the
// last before= page, else the oldest post shown.
func (s *Server) olderCursorLocked(ch *Chan) Cursor {
	switch {
	case s.olderCursor.ID != "":
		return s.olderCursor
	case len(s.older) > 0:
		return cursorOf(s.older[0])
	case len(ch.Win.Posts) > 0:
		return cursorOf(ch.Win.Posts[0])
	}
	return Cursor{}
}

// BeginLoadOlder starts loading history above what the open channel shows.
func (s *Server) BeginLoadOlder(channelID string) (HistOp, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil || channelID != s.active {
		return HistOp{}, false
	}
	c := s.olderCursorLocked(ch)
	if c.ID == "" {
		return HistOp{}, false
	}
	op := s.histOpLocked(channelID)
	op.Cursor = c
	return op, true
}

// AppendOlder adds a before= page begun with op above the history. It lives
// only in memory and is dropped when the user leaves the channel.
func (s *Server) AppendOlder(op HistOp, page model.PostList) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.histCurrentLocked(op)
	if !ok || op.Cursor != s.olderCursorLocked(ch) {
		return
	}
	raw := rawPosts(page)
	var add []model.Post
	for _, p := range raw {
		if keep(p, op.CRT) && !s.gone.has(p.ID) && indexOf(s.older, p.ID) < 0 && indexOf(ch.Win.Posts, p.ID) < 0 {
			add = append(add, p)
		}
	}
	s.older = append(add, s.older...)
	sortPosts(s.older)
	s.olderComplete = page.PrevPostID == ""
	s.olderCursor = op.Cursor
	if len(raw) > 0 {
		s.olderCursor = cursorOf(raw[0])
	}
	s.histRev++
}

// BeginRevalidate starts a reread of the stale segment: its range and the
// update_at of every post held, as of now.
func (s *Server) BeginRevalidate(channelID string) (RevalOp, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chans[channelID] == nil || channelID != s.active || !s.segStale {
		return RevalOp{}, false
	}
	if len(s.older) == 0 { // nothing left to be wrong
		s.segStale = false
		s.histRev++
		return RevalOp{}, false
	}
	op := RevalOp{HistOp: s.histOpLocked(channelID), Low: cursorOf(s.older[0]), High: cursorOf(s.older[len(s.older)-1]),
		Held: make(map[string]int64, len(s.older))}
	for _, p := range s.older {
		op.Held[p.ID] = p.UpdateAt
		op.Posts = append(op.Posts, cursorOf(p))
	}
	return op, true
}

// ApplyRevalidation applies a reread r begun with op. Posts read refresh
// the held copies (newerOf) and fill the range. Only r.Covered proves a
// post missing from the pages is gone: then a held post missing is removed
// (and goes into the gone ring) — unless its copy changed after the start
// (a live edit or reaction), or it shares the create_at of a page's cursor
// (r.Bounds, High: before=cursor cannot return it) — and the segment is no
// longer stale. Dropped after a reconnect (gapGen) too: the pages may
// predate what it missed. applied: false when dropped — the worker begins
// it again.
func (s *Server) ApplyRevalidation(op RevalOp, r Reread) (applied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.histCurrentLocked(op.HistOp)
	if !ok || op.Gap != s.gapGen {
		return false
	}
	s.older = s.rereadLocked(s.older, op.Low, op.High, op.Held, r, append([]Cursor{op.High}, r.Bounds...),
		func(p model.Post) bool { return keep(p, op.CRT) },
		func(p model.Post) bool { return indexOf(ch.Win.Posts, p.ID) < 0 })
	if r.Covered {
		s.segStale = false
	}
	s.histRev++
	return true
}

// rereadLocked applies a reread r to held (oldest first), a range from low
// to high whose posts had the update_at of at when it began, and returns
// what is held after it — the channel's segment and a thread's focus share
// it. Posts read (ok: of the kind held) refresh their held copies (newerOf);
// those not held, inside the range and admitted (not held elsewhere, like
// the window), fill it. Only r.Covered proves a held post missing from the
// pages is gone: then it is removed and goes into the gone ring — unless its
// copy changed after the start (a live edit or reaction), or it shares the
// create_at of a bound (a page's cursor the pages cannot have returned it
// past).
func (s *Server) rereadLocked(held []model.Post, low, high Cursor, at map[string]int64, r Reread, bounds []Cursor,
	ok, admit func(model.Post) bool) []model.Post {
	var fresh []model.Post
	if r.High != nil && !r.HighGone {
		fresh = append(fresh, *r.High)
	}
	fresh = append(fresh, rawPosts(r.Pages...)...)
	seen := map[string]bool{}
	var add []model.Post
	for _, p := range fresh {
		seen[p.ID] = true
		if !ok(p) || s.gone.has(p.ID) {
			continue
		}
		if i := indexOf(held, p.ID); i >= 0 {
			held[i] = newerOf(p, held[i])
			continue
		}
		if p.CreateAt >= low.CreateAt && p.CreateAt <= high.CreateAt && admit(p) && indexOf(add, p.ID) < 0 {
			add = append(add, p)
		}
	}
	held = append(held, add...)
	sortPosts(held)
	if !r.Covered {
		return held
	}
	tied := func(p model.Post) bool {
		return slices.ContainsFunc(bounds, func(b Cursor) bool { return b.CreateAt == p.CreateAt && b.ID != p.ID })
	}
	return slices.DeleteFunc(held, func(p model.Post) bool {
		was, isHeld := at[p.ID]
		gone := isHeld && !seen[p.ID] && p.UpdateAt == was && !tied(p)
		if gone {
			// Into the gone ring, so a page read before cannot bring it
			// back. Only the ring: a root re-read here already has its
			// count, and replyGoneLocked would lower it again.
			s.gone.add(p.ID)
		}
		return gone
	})
}

// contiguousPart: the posts of w known contiguous on the server — all of a
// live window, those up to GapAfter of a stale one.
func contiguousPart(w Window) []model.Post {
	if !w.Stale {
		return w.Posts
	}
	return w.Posts[:indexOf(w.Posts, w.GapAfter)+1]
}

// windowReplacedLocked keeps the held history in step with a new latest
// page (merged) replacing the window old. A page that no longer reaches the
// old window's first post (a catch-up overflow) leaves a hole: held history
// stays where the reader is, behind a new gap, and is stale — the catch-up
// covered only the latest page. The old window joins it when the history
// reached it (no gap), so a reader in it keeps their place too. With
// nothing held, it goes as before.
// Held posts the page holds go (the window has them), and so do those newer
// than its first post that it lacks (gone on the server); a held post in the
// page proves the history joins it.
func (s *Server) windowReplacedLocked(old Window, merged []model.Post) {
	reaches := len(old.Posts) > 0 && len(merged) > 0 && merged[0].CreateAt <= old.Posts[0].CreateAt
	switch {
	case !s.olderGap && reaches:
	case !s.olderGap && len(s.older) == 0:
		s.resetHistoryLocked()
		return
	case !reaches && old.Loaded:
		if !s.olderGap {
			// The old window joins the history: it stays where the reader
			// is, as history, up to where it was live (GapAfter: what came
			// after arrived across a lost stream, not contiguous).
			s.older = append(s.older, contiguousPart(old)...)
			sortPosts(s.older)
			s.newerCursor = cursorOf(s.older[len(s.older)-1])
		}
		s.olderGap, s.segStale = true, true
		s.gapGen++
		s.histRev++
	case !s.olderGap: // no window before: nothing proved the history joins this one
		s.newerCursor = cursorOf(s.older[len(s.older)-1])
		s.olderGap = true
		s.gapGen++
		s.histRev++
	}
	if len(merged) == 0 {
		return
	}
	joined := false
	s.older = slices.DeleteFunc(s.older, func(p model.Post) bool {
		if indexOf(merged, p.ID) >= 0 {
			joined = true
			return true
		}
		return p.CreateAt > merged[0].CreateAt
	})
	if joined && s.olderGap {
		s.olderGap = false
		s.histRev++
	}
}
