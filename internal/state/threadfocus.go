package state

import (
	"cmp"
	"slices"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// Thread focus (spec «Поиск», Секция 1б, «Уточнения» п.1–2): a reply of the
// open thread beyond the replies it holds is shown in a segment around it —
// a sliding window of at most ThreadMaxReplies loaded by direction=up and
// direction=down pages — while the thread's own replies (t.replies) become
// the tail: the latest replies, at most ThreadPage, seeded with those held
// when the focus is set and then fed live (the oldest go: they come back
// with the gap). Between the segment and the tail there is a gap until a
// proof closes it: a down page holding a tail reply, or a down page with
// nothing newer (has_next false) — applied in the focus generation it was
// begun in, while the tail is live (loaded, not stale). With the gap
// closed the segment joins the tail: replies the tail lets go of move into
// the segment. The segment and the tail never hold the same reply.
//
// Only the open thread has a focus. It goes with a new focus, the thread
// closed or let go of, ResetThreads and a deleted root; every operation
// applies its page only in the focus generation (gen) and the thread
// epoch it was begun in, from the edge cursor it was begun with. A
// reconnect marks the focus stale (reread: BeginFocusRevalidate) and opens
// a closed gap — the tail may miss replies from then on.

// threadFocus is the open thread's focus.
type threadFocus struct {
	target   string
	replies  []model.Post // oldest first, ≤ ThreadMaxReplies, none also in the tail
	up, down Cursor       // raw cursors of the segment's edges: the next up/down page continues from them
	upDone   bool         // the segment starts at the thread's first reply
	downDone bool         // the last down page had nothing newer
	gap      bool         // not proved to join the tail
	gen      uint64       // the focus generation (Server.focusGen) its operations are begun in
	rev      uint64       // moves with every page or reread applied (the UI anchors to it)
	stale    bool         // to be reread: edits and deletions may be missing (a reconnect)
}

// ThreadFocusOp is a focus operation's capture: the thread, the epoch and
// focus generation, the CRT mode and the edge cursor it was begun with.
type ThreadFocusOp struct {
	Root    string
	Channel string
	Epoch   uint64
	Gen     uint64
	CRT     bool
	Cursor  Cursor
}

// FocusRevalOp is a reread of a stale focus: its held range from Low to
// High and each held reply's update_at, as of the start (RevalOp's
// counterpart).
type FocusRevalOp struct {
	ThreadFocusOp
	Low, High Cursor
	Held      map[string]int64
}

// ThreadFocusView is the focus in ThreadView: the reply focused, whether
// older or newer replies can be loaded into the segment, the gap to the
// tail (BeforeID: the tail's first reply, "" while it is empty; Stale: the
// segment waits for a reread) and the revision pages are applied with.
type ThreadFocusView struct {
	TargetID string  `json:"target_id"`
	HasOlder bool    `json:"has_older"`
	HasNewer bool    `json:"has_newer"`
	Gap      HistGap `json:"gap"`
	Rev      uint64  `json:"rev"`
}

// threadReplies: the replies of rootID in pages as the server sent them,
// oldest first by (create_at, id) — the order thread pages go by — each
// once.
func threadReplies(rootID string, lists ...model.PostList) []model.Post {
	var out []model.Post
	for _, l := range lists {
		for _, p := range l.Posts {
			if p.RootID == rootID && !slices.ContainsFunc(out, func(q model.Post) bool { return q.ID == p.ID }) {
				out = append(out, p)
			}
		}
	}
	slices.SortFunc(out, func(a, b model.Post) int {
		return cmp.Or(cmp.Compare(a.CreateAt, b.CreateAt), cmp.Compare(a.ID, b.ID))
	})
	return out
}

func hasNext(l model.PostList) bool { return l.HasNext != nil && *l.HasNext }

// liveReplyLocked: p is a reply the thread cache shows.
func (s *Server) liveReplyLocked(p model.Post) bool {
	return p.DeleteAt == 0 && p.OriginalID == "" && !s.gone.has(p.ID)
}

// reply is the held copy of a reply — in the tail or the focus — or nil.
func (t *thread) reply(id string) *model.Post {
	if i := indexOf(t.replies, id); i >= 0 {
		return &t.replies[i]
	}
	if t.focus != nil {
		if i := indexOf(t.focus.replies, id); i >= 0 {
			return &t.focus.replies[i]
		}
	}
	return nil
}

// eachReply calls fn with every reply held, the focus's first.
func (t *thread) eachReply(fn func(*model.Post)) {
	if t.focus != nil {
		for i := range t.focus.replies {
			fn(&t.focus.replies[i])
		}
	}
	for i := range t.replies {
		fn(&t.replies[i])
	}
}

// bumpFocusLocked moves the focus generation: operations begun before are
// dropped. f (if any) goes on in the new one.
func (s *Server) bumpFocusLocked(f *threadFocus) {
	s.focusGen++
	if f != nil {
		f.gen = s.focusGen
	}
}

func (s *Server) touchFocusLocked(f *threadFocus) {
	s.focusRev++
	f.rev = s.focusRev
}

// dropFocusLocked ends t's focus (if any): the thread is a plain one again,
// its tail its latest replies. Another thread's focus is not touched.
func (s *Server) dropFocusLocked(t *thread) {
	if t != nil && t.focus != nil {
		t.focus = nil // the tail's own completeness stands (setTailLocked, trimTailLocked)
		t.capped = false
		s.bumpFocusLocked(nil)
	}
}

// FocusThread opens rootID's thread (OpenThread) to show replyID. held: the
// reply (or the root) is shown already — the tail, the focus or the plain
// thread's replies hold it — nothing to load. Otherwise the thread's focus,
// if any, ends (its operations are dropped) and op is what SetThreadFocus
// applies the segment with. ok=false: channelID is not known.
func (s *Server) FocusThread(channelID, rootID, replyID string) (op ThreadFocusOp, held bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, _, ok := s.openThreadLocked(channelID, rootID); !ok {
		return ThreadFocusOp{}, false, false
	}
	t := s.threads[rootID]
	if replyID == rootID || t.reply(replyID) != nil {
		if t.focus != nil {
			t.focus.target = replyID
		}
		// A newer navigation: a focus begun before must not land (an
		// existing one goes by its own generation).
		s.bumpFocusLocked(nil)
		return ThreadFocusOp{}, true, true
	}
	s.dropFocusLocked(t)
	s.bumpFocusLocked(nil)
	return ThreadFocusOp{Root: rootID, Channel: channelID, Epoch: s.threadEpoch, Gen: s.focusGen, CRT: s.crtLocked()}, false, true
}

// focusThreadLocked: the open thread op was begun on, in the same epoch,
// CRT mode and focus generation — its focus's, or (none yet: the op of
// FocusThread) the latest one, which every new opening of a thread, close,
// reset and new focus moves.
func (s *Server) focusThreadLocked(op ThreadFocusOp) (*thread, bool) {
	t := s.threads[op.Root]
	if t == nil || op.Root != s.openThread || t.channelID != op.Channel || t.rootDeleted ||
		op.Epoch != s.threadEpoch || op.CRT != s.crtLocked() {
		return nil, false
	}
	gen := s.focusGen
	if t.focus != nil {
		gen = t.focus.gen
	}
	return t, op.Gen == gen
}

// SetThreadFocus sets the focus begun with op: target (GET /posts/{id} —
// pages from a cursor may leave it out) and the up/down pages around it.
// The tail is the latest ThreadPage replies held now. The gap stays open
// unless proved closed at this moment (proveFocusLocked).
func (s *Server) SetThreadFocus(op ThreadFocusOp, target model.Post, up, down model.PostList) (applied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.focusThreadLocked(op)
	if !ok || t.focus != nil {
		return false
	}
	self := model.PostList{Order: []string{target.ID}, Posts: map[string]model.Post{target.ID: target}}
	raw := threadReplies(op.Root, up, self, down)
	if len(raw) == 0 { // target is not a reply of the thread
		return false
	}
	f := &threadFocus{target: target.ID, gen: op.Gen, gap: true,
		up: cursorOf(raw[0]), down: cursorOf(raw[len(raw)-1]), upDone: !hasNext(up), downDone: !hasNext(down)}
	t.focus = f
	if r, ok := up.Posts[op.Root]; ok && t.root.ID == "" && r.DeleteAt == 0 {
		t.root = r // not held in the feed: shown before the tail's page lands
	}
	if n := len(t.replies); n > ThreadPage {
		t.replies = slices.Clone(t.replies[n-ThreadPage:])
		t.complete = false
	}
	t.capped = false
	s.placeFocusLocked(t, raw)
	s.proveFocusLocked(t, raw, f.downDone, f.down)
	s.slideFocusLocked(t, true)
	s.touchFocusLocked(f)
	return true
}

// placeFocusLocked merges raw replies by id: a reply the tail holds stays
// there, one the focus holds is refreshed (newerOf); a new one goes to the
// focus unless newer than the tail's first reply — then the tail missed it.
func (s *Server) placeFocusLocked(t *thread, raw []model.Post) {
	f := t.focus
	var add, tail []model.Post
	for _, p := range raw {
		if !s.liveReplyLocked(p) {
			continue
		}
		if i := indexOf(t.replies, p.ID); i >= 0 {
			t.replies[i] = newerOf(p, t.replies[i])
			continue
		}
		if i := indexOf(f.replies, p.ID); i >= 0 {
			f.replies[i] = newerOf(p, f.replies[i])
			continue
		}
		if len(t.replies) > 0 && p.CreateAt > t.replies[0].CreateAt {
			tail = append(tail, p)
		} else {
			add = append(add, p)
		}
	}
	f.replies = append(f.replies, add...)
	sortPosts(f.replies)
	if len(tail) > 0 {
		t.replies = append(t.replies, tail...)
		sortPosts(t.replies)
		s.trimTailLocked(t)
	}
}

// proveFocusLocked closes the gap on a proof from raw replies reaching up to
// newest (a down page, or the pages of the focus): one the tail holds, or
// nothing newer (more=false) — the latter also needs the tail not to start
// past newest (replies that arrived while the page was in flight may have
// pushed its first one out). Either only while the tail is live. closed:
// no gap is left.
func (s *Server) proveFocusLocked(t *thread, raw []model.Post, done bool, newest Cursor) (closed bool) {
	f := t.focus
	if !f.gap {
		return true
	}
	if !t.loaded || t.stale {
		return false
	}
	joins := slices.ContainsFunc(raw, func(p model.Post) bool { return indexOf(t.replies, p.ID) >= 0 })
	if !joins && done && (len(t.replies) == 0 || t.replies[0].CreateAt <= newest.CreateAt) {
		joins = true
	}
	f.gap = !joins
	return joins
}

// slideFocusLocked keeps the segment at ≤ ThreadMaxReplies: past it the far
// edge is let go of — the up edge (releaseUp) when newer replies came, the
// down edge when older ones did — and that edge's cursor is its new first
// (last) reply, so it can be loaded again. Letting go of the down edge
// opens a closed gap (a new one: the generation moves).
func (s *Server) slideFocusLocked(t *thread, releaseUp bool) {
	f := t.focus
	n := len(f.replies)
	if n <= ThreadMaxReplies {
		return
	}
	if releaseUp {
		f.replies = slices.Clone(f.replies[n-ThreadMaxReplies:])
		f.up, f.upDone = cursorOf(f.replies[0]), false
		return
	}
	f.replies = slices.Clone(f.replies[:ThreadMaxReplies])
	f.down, f.downDone = cursorOf(f.replies[ThreadMaxReplies-1]), false
	if !f.gap {
		f.gap = true
		s.bumpFocusLocked(f)
	}
}

// absorbLocked moves replies the tail let go of into the segment it joins
// (the gap closed): the down cursor follows, the up edge slides.
func (s *Server) absorbLocked(t *thread, replies []model.Post) {
	f := t.focus
	if len(replies) == 0 {
		return
	}
	for _, p := range replies {
		if indexOf(f.replies, p.ID) < 0 {
			f.replies = append(f.replies, p)
		}
	}
	sortPosts(f.replies)
	if last := f.replies[len(f.replies)-1]; last.CreateAt >= f.down.CreateAt {
		f.down = cursorOf(last)
	}
	s.slideFocusLocked(t, true)
	s.touchFocusLocked(f)
}

// trimTailLocked keeps a focused thread's tail at ≤ ThreadPage: the oldest
// go — into the segment if the gap is closed, else into the gap.
func (s *Server) trimTailLocked(t *thread) {
	n := len(t.replies)
	if n <= ThreadPage {
		return
	}
	out := slices.Clone(t.replies[:n-ThreadPage])
	t.replies = slices.Delete(t.replies, 0, n-ThreadPage)
	t.complete = false
	if !t.focus.gap {
		s.absorbLocked(t, out)
	}
}

// setTailLocked installs the latest page (merged: SetThreadPage's merge;
// raw: the page's replies; more: older ones exist) as a focused thread's
// tail. With the gap closed the old tail joins the segment where the new
// one does not reach. The gap closes if the page reaches the segment (a
// reply it holds, or the whole thread in it); a closed one opens if it
// does not reach the old tail either. Segment replies the page holds, or
// newer than its first reply that it lacks (gone), leave the segment.
func (s *Server) setTailLocked(t *thread, merged, raw []model.Post, more bool) {
	f := t.focus
	old := t.replies
	t.replies, t.complete, t.capped = merged, !more, false
	has := func(list []model.Post) bool {
		return slices.ContainsFunc(raw, func(p model.Post) bool { return indexOf(list, p.ID) >= 0 })
	}
	proof := !more || has(f.replies)
	if !f.gap {
		proof = proof || has(old)
		var join []model.Post
		for _, p := range old {
			if indexOf(merged, p.ID) < 0 && (len(merged) == 0 || p.CreateAt < merged[0].CreateAt) {
				join = append(join, p)
			}
		}
		s.absorbLocked(t, join)
	}
	switch {
	case proof:
		f.gap = false
		if len(merged) > 0 {
			f.replies = slices.DeleteFunc(f.replies, func(p model.Post) bool {
				return indexOf(merged, p.ID) >= 0 || p.CreateAt > merged[0].CreateAt
			})
		}
	case !f.gap:
		f.gap = true
		s.bumpFocusLocked(f)
	}
	s.trimTailLocked(t)
	s.touchFocusLocked(f)
}

// staleFocusLocked: the event stream was lost. The focus is to be reread;
// a closed gap opens (the tail, joined to the segment up to now, moves into
// it: what comes next may have a hole before it); the generation moves.
func (s *Server) staleFocusLocked(t *thread) {
	f := t.focus
	f.stale = true
	if !f.gap {
		tail := t.replies
		t.replies, t.complete = nil, false
		s.absorbLocked(t, tail) // data moved: rev moves there
		f.gap, f.downDone = true, false
	}
	s.bumpFocusLocked(f)
}

// BeginFocusLoad starts loading a page into rootID's focus: older (up) from
// its up edge, newer (down) from its down edge into the gap. false: nothing
// to load that way (no focus, the first reply reached, no gap).
func (s *Server) BeginFocusLoad(rootID string, newer bool) (ThreadFocusOp, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	if t == nil || t.focus == nil || rootID != s.openThread || t.rootDeleted {
		return ThreadFocusOp{}, false
	}
	f := t.focus
	op := ThreadFocusOp{Root: rootID, Channel: t.channelID, Epoch: s.threadEpoch, Gen: f.gen, CRT: s.crtLocked(), Cursor: f.up}
	switch {
	case newer && !f.gap, !newer && f.upDone:
		return ThreadFocusOp{}, false
	case newer:
		op.Cursor = f.down
	}
	return op, true
}

// AppendFocus applies a page begun with op: older (direction=up) or newer
// (direction=down, into the gap) — only onto the edge it was begun from, in
// the same generation. The edge's raw cursor moves to the page's last raw
// reply; the window slides; a newer page closes the gap on a proof
// (proveFocusLocked). closed: no gap is left; progressed: the edge moved
// or learned it is the thread's end.
func (s *Server) AppendFocus(op ThreadFocusOp, newer bool, page model.PostList) (closed, progressed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.focusThreadLocked(op)
	if !ok || t.focus == nil {
		return false, false
	}
	f := t.focus
	edge, done := &f.up, &f.upDone
	if newer {
		edge, done = &f.down, &f.downDone
	}
	if op.Cursor != *edge || (newer && !f.gap) {
		return !f.gap, false
	}
	raw := threadReplies(op.Root, page)
	more := hasNext(page)
	s.placeFocusLocked(t, raw)
	if len(raw) > 0 {
		if newer {
			*edge = cursorOf(raw[len(raw)-1])
		} else {
			*edge = cursorOf(raw[0])
		}
	}
	progressed = *edge != op.Cursor || *done == more
	*done = !more
	if newer {
		closed = s.proveFocusLocked(t, raw, !more, *edge)
	}
	s.slideFocusLocked(t, newer)
	if progressed || closed {
		s.touchFocusLocked(f)
	}
	return !f.gap, progressed || closed
}

// FocusStale reports that rootID's focus waits for a reread.
func (s *Server) FocusStale(rootID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	return t != nil && t.focus != nil && t.focus.stale && rootID == s.openThread
}

// BeginFocusRevalidate starts a reread of rootID's stale focus: its range
// and the update_at of every reply held, as of now.
func (s *Server) BeginFocusRevalidate(rootID string) (FocusRevalOp, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	if t == nil || t.focus == nil || !t.focus.stale || rootID != s.openThread || t.rootDeleted {
		return FocusRevalOp{}, false
	}
	f := t.focus
	if len(f.replies) == 0 { // nothing left to be wrong (no data applied: rev stays)
		f.stale = false
		return FocusRevalOp{}, false
	}
	op := FocusRevalOp{
		ThreadFocusOp: ThreadFocusOp{Root: rootID, Channel: t.channelID, Epoch: s.threadEpoch, Gen: f.gen, CRT: s.crtLocked()},
		Low:           cursorOf(f.replies[0]), High: cursorOf(f.replies[len(f.replies)-1]),
		Held: make(map[string]int64, len(f.replies)),
	}
	for _, p := range f.replies {
		op.Held[p.ID] = p.UpdateAt
	}
	return op, true
}

// ApplyFocusRevalidation applies a reread r begun with op, as the channel's
// segment does (rereadLocked): replies read refresh or fill the range;
// only a covered reread removes those missing, and then the focus is no
// longer stale. Dropped (applied=false) in another generation or epoch —
// a reconnect during it: the worker begins it again.
func (s *Server) ApplyFocusRevalidation(op FocusRevalOp, r Reread) (applied bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.focusThreadLocked(op.ThreadFocusOp)
	if !ok || t.focus == nil {
		return false
	}
	f := t.focus
	f.replies = s.rereadLocked(f.replies, op.Low, op.High, op.Held, r, []Cursor{op.High},
		func(p model.Post) bool { return p.RootID == op.Root && s.liveReplyLocked(p) },
		func(p model.Post) bool { return indexOf(t.replies, p.ID) < 0 })
	s.slideFocusLocked(t, true) // replies read into the range count too
	if r.Covered {
		f.stale = false
	}
	s.touchFocusLocked(f)
	return true
}

// focusViewLocked renders t's focus; replies: the segment then the tail,
// each reply once.
func focusViewLocked(t *thread) (v *ThreadFocusView, replies []model.Post) {
	f := t.focus
	replies = make([]model.Post, 0, len(f.replies)+len(t.replies))
	replies = append(replies, f.replies...)
	for _, p := range t.replies {
		if indexOf(f.replies, p.ID) < 0 {
			replies = append(replies, p)
		}
	}
	v = &ThreadFocusView{TargetID: f.target, HasOlder: !f.upDone, HasNewer: f.gap, Rev: f.rev,
		Gap: HistGap{Open: f.gap, Gen: f.gen, Stale: f.stale}}
	if f.gap && len(t.replies) > 0 {
		v.Gap.BeforeID = t.replies[0].ID
	}
	return v, replies
}
