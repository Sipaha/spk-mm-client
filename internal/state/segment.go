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

// cursor is a raw post a page continues from: the oldest (before=) or
// newest (after=) post of the last page applied in that direction as the
// server sent it — shown or filtered out — so a page with nothing to show
// still moves on.
type cursor struct {
	ID       string
	CreateAt int64
}

func cursorOf(p model.Post) cursor { return cursor{ID: p.ID, CreateAt: p.CreateAt} }

// HistOp is a history operation's capture: the channel and the generations
// (history, gap, window), the CRT mode and the cursor it was begun with.
type HistOp struct {
	Channel string
	Hist    uint64
	Gap     uint64
	Win     uint64
	CRT     bool
	Cursor  cursor
}

// RevalOp is a reread of a stale segment: the held range from its oldest
// (Low) to its newest (High) post and each held post's update_at, all as of
// the start.
type RevalOp struct {
	HistOp
	Low, High cursor
	Held      map[string]int64
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
	s.olderCursor, s.newerCursor = cursor{}, cursor{}
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
func joinsWindowLocked(ch *Chan, raw []model.Post, next string, newest cursor) bool {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil || channelID != s.active {
		return false
	}
	return indexOf(ch.Win.Posts, postID) >= 0 || indexOf(s.older, postID) >= 0
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
func (s *Server) olderCursorLocked(ch *Chan) cursor {
	switch {
	case s.olderCursor.ID != "":
		return s.olderCursor
	case len(s.older) > 0:
		return cursorOf(s.older[0])
	case len(ch.Win.Posts) > 0:
		return cursorOf(ch.Win.Posts[0])
	}
	return cursor{}
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
	}
	return op, true
}

// ApplyRevalidation applies a reread begun with op: high (GET of High;
// highGone — 404) and the before=High pages. Posts read refresh the held
// copies (newerOf) and fill the range. Only covered (the worker paged down
// past Low or to the channel's first post) proves a post missing from the
// pages is gone: then a held post missing is removed — unless its copy
// changed after the start (a live edit or reaction), or it shares High's
// create_at (before=High cannot return it) — and the segment is no longer
// stale. Dropped after a reconnect (gapGen) too: the pages may predate what
// it missed.
func (s *Server) ApplyRevalidation(op RevalOp, high *model.Post, highGone bool, pages []model.PostList, covered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.histCurrentLocked(op.HistOp)
	if !ok || op.Gap != s.gapGen {
		return
	}
	var fresh []model.Post
	if high != nil && !highGone {
		fresh = append(fresh, *high)
	}
	fresh = append(fresh, rawPosts(pages...)...)
	seen := map[string]bool{}
	var add []model.Post
	for _, p := range fresh {
		seen[p.ID] = true
		if !keep(p, op.CRT) || s.gone.has(p.ID) {
			continue
		}
		if i := indexOf(s.older, p.ID); i >= 0 {
			s.older[i] = newerOf(p, s.older[i])
			continue
		}
		if p.CreateAt >= op.Low.CreateAt && p.CreateAt <= op.High.CreateAt && indexOf(ch.Win.Posts, p.ID) < 0 && indexOf(add, p.ID) < 0 {
			add = append(add, p)
		}
	}
	s.older = append(s.older, add...)
	sortPosts(s.older)
	if covered {
		s.older = slices.DeleteFunc(s.older, func(p model.Post) bool {
			at, held := op.Held[p.ID]
			return held && !seen[p.ID] && p.UpdateAt == at && (p.ID == op.High.ID || p.CreateAt != op.High.CreateAt)
		})
		s.segStale = false
	}
	s.histRev++
}

// windowReplacedLocked keeps the held history in step with a new latest
// page (merged) replacing the window old. A page that no longer reaches the
// old window's first post (a catch-up overflow) leaves a hole: held history
// stays where the reader is, behind a new gap, and is stale — the catch-up
// covered only the latest page. With nothing held, it goes as before.
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
