package state

import (
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

type Pending struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	RootID    string `json:"root_id,omitempty"`
	Message   string `json:"message"`
	CreateAt  int64  `json:"create_at"`
	Failed    bool   `json:"failed,omitempty"`
	// Files are the post's attachments while it is being sent (FileView.ID
	// is the attachment id, Staged is set).
	Files []FileView `json:"files,omitempty"`
}

// seenSet remembers recent post ids so a post delivered twice (REST
// response + WS echo, a replayed event) changes counters only once.
type seenSet struct {
	ids  map[string]struct{}
	ring []string
	next int
}

func newSeenSet(n int) seenSet { return seenSet{ids: map[string]struct{}{}, ring: make([]string, n)} }

func (s *seenSet) has(id string) bool { _, ok := s.ids[id]; return ok }

func (s *seenSet) add(id string) {
	if s.has(id) {
		return
	}
	if old := s.ring[s.next]; old != "" {
		delete(s.ids, old)
	}
	s.ring[s.next] = id
	s.ids[id] = struct{}{}
	s.next = (s.next + 1) % len(s.ring)
}

// keep: does p belong in a channel feed?
func keep(p model.Post, crt bool) bool {
	return p.OriginalID == "" && p.DeleteAt == 0 && (!crt || p.RootID == "")
}

func indexOf(posts []model.Post, id string) int {
	return slices.IndexFunc(posts, func(p model.Post) bool { return p.ID == id })
}

func sortPosts(ps []model.Post) {
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].CreateAt < ps[j].CreateAt })
}

// trimWindowLocked keeps ch.Win.Posts at ≤ WindowSize. For the active
// channel with history already loaded, the trimmed (oldest) posts are not
// discarded — they are still visible above the window, so they move to the
// end of s.older instead. Not across a gap: s.older would get a hole inside
// it; they come back when the gap is loaded.
func (s *Server) trimWindowLocked(ch *Chan) {
	w := &ch.Win
	if len(w.Posts) <= WindowSize {
		return
	}
	cut := len(w.Posts) - WindowSize
	trimmed := append([]model.Post(nil), w.Posts[:cut]...)
	w.Posts = append([]model.Post(nil), w.Posts[cut:]...)
	w.Complete = false
	if ch.Info.ID == s.active && !s.olderGap && s.historyHeldLocked() {
		s.older = append(s.older, trimmed...)
		sortPosts(s.older)
	}
}

// keepNewerReplies gives p (a fresh read of a post held as local) the
// local reply count when p's own is older. The server moves a root's
// update_at to each reply's time and to each reply deletion's time
// (post_store.go Save/Delete), and so does replyGoneLocked for a deletion
// with a known time: a root whose update_at is behind the newest reply
// applied live, or behind the local copy's update_at, was read before it.
//
// A count of 0 is taken as is: every read we make carries the real count —
// pages and since= rows (skipFetchThreads=true: a COUNT subquery without
// CRT, the Threads table with it) and post_edited (GetSingle's subquery).
func keepNewerReplies(p *model.Post, local model.Post) {
	if p.UpdateAt < max(local.LastReplyAt, local.UpdateAt) {
		p.ReplyCount, p.LastReplyAt = local.ReplyCount, local.LastReplyAt
	}
}

// newerOf is what a page read of a post held as local installs: the local
// copy when it is newer (a reaction or an edit applied live since the
// read — both move update_at), else p with the local reply count if that
// is newer (keepNewerReplies).
func newerOf(p, local model.Post) model.Post {
	if local.UpdateAt > p.UpdateAt {
		if p.PendingPostID != "" && local.PendingPostID == "" {
			local.PendingPostID = p.PendingPostID
		}
		return local
	}
	keepNewerReplies(&p, local)
	return p
}

func (s *Server) upsertLocked(ch *Chan, p model.Post) {
	if i := indexOf(ch.Win.Posts, p.ID); i >= 0 {
		if p.UpdateAt >= ch.Win.Posts[i].UpdateAt {
			keepNewerReplies(&p, ch.Win.Posts[i])
			// An update that doesn't carry pending_post_id (e.g. a plain
			// edit echo) must not erase the value the confirmation set —
			// the frontend keys its feed row by it across the pending ->
			// confirmed swap.
			if p.PendingPostID == "" {
				p.PendingPostID = ch.Win.Posts[i].PendingPostID
			}
			ch.Win.Posts[i] = p
		}
	} else if j := indexOf(s.older, p.ID); ch.Info.ID == s.active && j >= 0 {
		// Held above the window already (a late echo of an old post): one
		// copy only.
		s.older[j] = newerOf(p, s.older[j])
		return
	} else {
		ch.Win.Posts = append(ch.Win.Posts, p)
		sortPosts(ch.Win.Posts)
		s.trimWindowLocked(ch)
	}
	s.dirty.posts[ch.Info.ID] = true
}

// removeLocked deletes the post d and its replies (they die with the
// root). A deleted reply lowers its root's count — see replyGoneLocked.
func (s *Server) removeLocked(ch *Chan, d model.Post) bool {
	gone := func(p model.Post) bool { return p.ID == d.ID || p.RootID == d.ID }
	n := len(ch.Win.Posts)
	ch.Win.Posts = slices.DeleteFunc(ch.Win.Posts, gone)
	changed := len(ch.Win.Posts) != n
	if ch.Info.ID == s.active {
		m := len(s.older)
		s.older = slices.DeleteFunc(s.older, gone)
		changed = changed || len(s.older) != m
	}
	if changed {
		s.dirty.posts[ch.Info.ID] = true
	}
	s.threadRemovedLocked(d)
	return s.replyGoneLocked(ch, d) || changed
}

// replyGoneLocked lowers the root's reply count for the deleted reply d,
// once per id however many paths report it (REST DeletePost, post_deleted,
// a since= row) — the gone ring — and never below 0. A deleted root goes
// into the ring too, so its late echo or a page read before the deletion
// does not bring it back (applyNewPostLocked, SetWindow, the thread cache).
//
// Only a since= row carries the deletion time (d.DeleteAt): the server
// moves the root's update_at to it, so a root read at or after it already
// has the lower count and is left alone; one lowered here takes that
// update_at too, so a page read before the deletion cannot raise the count
// back (keepNewerReplies). post_deleted carries the post as read before the
// deletion (delete_at 0, app/post.go DeletePost), and our own DeletePost
// knows no time: those always lower the count, once.
func (s *Server) replyGoneLocked(ch *Chan, d model.Post) bool {
	if s.gone.has(d.ID) {
		return false
	}
	s.gone.add(d.ID)
	if d.RootID == "" {
		return false
	}
	return s.eachRootLocked(ch, d.RootID, func(r *model.Post) bool {
		if r.ReplyCount == 0 || (d.DeleteAt != 0 && r.UpdateAt >= d.DeleteAt) {
			return false
		}
		r.ReplyCount--
		r.UpdateAt = max(r.UpdateAt, d.DeleteAt)
		return true
	})
}

// eachRootLocked applies f to every copy of the post rootID held for ch —
// the window, for the open channel the loaded history, and the thread
// cache — and reports whether f changed any.
func (s *Server) eachRootLocked(ch *Chan, rootID string, f func(*model.Post) bool) bool {
	changed := false
	if i := indexOf(ch.Win.Posts, rootID); i >= 0 && f(&ch.Win.Posts[i]) {
		s.dirty.posts[ch.Info.ID] = true
		changed = true
	}
	if ch.Info.ID == s.active {
		if i := indexOf(s.older, rootID); i >= 0 && f(&s.older[i]) {
			changed = true
		}
	}
	if t := s.threads[rootID]; t != nil && t.root.ID == rootID && f(&t.root) {
		changed = true
	}
	return changed
}

// replyPosted updates root for its new reply p (first delivery only: a
// repeat — REST response vs WS echo, a replayed event — may carry a total
// a deletion has lowered since). The reply's own reply_count is the
// thread's total at its moment (the server fills it in), taken unless an
// even newer reply was applied already; without it (0), a reply newer
// than LastReplyAt adds one.
func replyPosted(root *model.Post, p model.Post) bool {
	switch {
	case p.ReplyCount > 0 && p.CreateAt >= root.LastReplyAt:
		root.ReplyCount = p.ReplyCount
	case p.ReplyCount == 0 && p.CreateAt > root.LastReplyAt:
		root.ReplyCount++
	default:
		return false
	}
	root.LastReplyAt = p.CreateAt
	return true
}

// SetWindow installs the latest page of a channel. Posts already in the
// window that are newer than the page arrived over WS while the request was
// in flight — they are kept. History loaded above the old window of the
// active channel follows windowReplacedLocked: kept, behind a gap when the
// new page no longer reaches the old window's first post (a catch-up
// overflow reloads only the latest page).
//
// gen is FetchMode's generation when the page was requested: a page from
// before a ResetWindows is dropped.
func (s *Server) SetWindow(channelID string, page []model.Post, complete bool, syncedAt int64, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil || gen != s.winGen {
		return
	}
	crt := s.crtLocked()
	var merged []model.Post
	var newest int64
	for _, p := range page {
		if keep(p, crt) && !s.gone.has(p.ID) {
			if i := indexOf(ch.Win.Posts, p.ID); i >= 0 {
				p = newerOf(p, ch.Win.Posts[i])
			} else if i := indexOf(s.older, p.ID); channelID == s.active && i >= 0 {
				p = newerOf(p, s.older[i])
			}
			merged = append(merged, p)
		}
		newest = max(newest, p.CreateAt)
	}
	for _, p := range ch.Win.Posts {
		if p.CreateAt > newest && indexOf(merged, p.ID) < 0 {
			merged = append(merged, p)
		}
	}
	sortPosts(merged)
	if channelID == s.active && s.historyHeldLocked() {
		s.windowReplacedLocked(ch.Win, merged)
	}
	ch.Win = Window{Posts: merged, Loaded: true, Complete: complete, SyncedAt: syncedAt}
	s.trimWindowLocked(ch)
	s.dirty.posts[channelID] = true
}

// MergeSince applies a posts?since= response (edits, deletions, new posts)
// and marks the window caught up. gen: as in SetWindow. Every copy held is
// updated first (the window and, for the open channel, the history); then a
// new post goes to the window by the window's own rule — one inside the
// history's gap does not fill it and is left to the gap's loading.
func (s *Server) MergeSince(channelID string, posts []model.Post, syncedAt int64, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	// Only a loaded window is caught up: since= rows alone would pass for
	// the channel's latest page.
	if ch == nil || gen != s.winGen || !ch.Win.Loaded {
		return
	}
	crt := s.crtLocked()
	var oldest int64
	switch {
	case len(ch.Win.Posts) > 0 && !ch.Win.Complete:
		oldest = ch.Win.Posts[0].CreateAt
	case len(ch.Win.Posts) == 0 && channelID == s.active && s.olderGap:
		// An empty window behind the history's gap has no first post to
		// bound it: a row created before the window's sync point is an edit
		// of a post inside the gap. It is left to the gap's loading — in the
		// window it would pass for an intersection proof (joinsWindowLocked).
		// Only rows created since go in (new posts); a new one left out by
		// clock skew (SyncedAt is our clock) comes with the gap's loading.
		oldest = ch.Win.SyncedAt
	}
	for _, p := range posts {
		switch {
		case p.OriginalID != "":
			continue // edit-history row
		case p.DeleteAt > 0:
			s.removeLocked(ch, p)
		case !keep(p, crt) || s.gone.has(p.ID):
			continue
		default:
			held := false
			if channelID == s.active {
				if i := indexOf(s.older, p.ID); i >= 0 {
					s.older[i] = newerOf(p, s.older[i])
					held = true
				}
			}
			if indexOf(ch.Win.Posts, p.ID) >= 0 || (!held && p.CreateAt >= oldest) {
				s.upsertLocked(ch, p)
			}
		}
	}
	ch.Win.Loaded, ch.Win.Stale, ch.Win.GapAfter = true, false, ""
	ch.Win.SyncedAt = max(ch.Win.SyncedAt, syncedAt)
	s.dirty.posts[channelID] = true
}

// OldestPostID is the oldest post the channel shows. (LoadOlder continues
// from BeginLoadOlder's raw cursor instead.)
func (s *Server) OldestPostID(channelID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if channelID == s.active && len(s.older) > 0 {
		return s.older[0].ID
	}
	if ch := s.chans[channelID]; ch != nil && len(ch.Win.Posts) > 0 {
		return ch.Win.Posts[0].ID
	}
	return ""
}

// FetchMode is what a post fetch is requested with: the CRT mode and the
// window generation, read together. The generation is handed back to
// SetWindow/MergeSince (history operations capture it in their HistOp),
// which drop a page requested before a
// ResetWindows — it may be of the other mode, or land on a window that is
// no longer there as Loaded. (The same pattern guards the thread cache
// against ResetThreads.)
func (s *Server) FetchMode() (crt bool, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.crtLocked(), s.winGen
}

// ResetWindows forgets every post window (CRT was toggled: the windows hold
// the wrong kind of posts). Channels are refetched by the worker; pages
// already in flight are dropped (FetchMode).
func (s *Server) ResetWindows() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.winGen++
	for id, ch := range s.chans {
		ch.Win = Window{}
		s.dirty.posts[id] = true
	}
	s.resetHistoryLocked()
}

// markStale marks w stale as of liveUntil: posts after GapAfter may be
// missing until the worker catches the window up via since=. If w was
// already stale, SyncedAt is left alone — the gap already started earlier
// and liveUntil must not move it forward again. Shared by MarkStale (the
// event stream was lost) and Restore (every window comes back from a
// snapshot stale, caught up to live/at).
func (w *Window) markStale(liveUntil int64) {
	if !w.Stale {
		w.SyncedAt = max(w.SyncedAt, liveUntil)
	}
	w.Stale = true
	w.GapAfter = ""
	if n := len(w.Posts); n > 0 {
		w.GapAfter = w.Posts[n-1].ID
	}
}

// MarkStale records that the event stream was lost. Every loaded window may
// miss posts after its last one; it stays readable and is caught up by the worker.
// A LoadNewer begun before cannot close the history's gap (gapGen): its
// proof may predate what the window missed.
func (s *Server) MarkStale(liveUntil int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gapGen++
	s.markThreadsStaleLocked()
	for id, ch := range s.chans {
		if !ch.Win.Loaded || ch.Win.Stale {
			continue
		}
		ch.Win.markStale(liveUntil)
		s.dirty.posts[id] = true
	}
}

type SyncItem struct {
	ChannelID string
	Loaded    bool
	SyncedAt  int64
	Priority  int // lower first
}

const recentWindow = 7 * 24 * time.Hour

// priorityLocked: open channel → DMs and mentions → unread → recently
// active → the rest (spec «Предзагрузка постов»).
func (s *Server) priorityLocked(c *Chan) int {
	if c.Info.ID == s.active {
		return 0
	}
	u, m := s.unreadLocked(c)
	switch {
	case m > 0 || (u && isDirect(c)):
		return 1
	case u:
		return 2
	case s.now().UnixMilli()-c.Info.LastPostAt < recentWindow.Milliseconds():
		return 3
	}
	return 4
}

func (s *Server) syncItemLocked(c *Chan) (SyncItem, bool) {
	if c.Info.DeleteAt != 0 || (c.Win.Loaded && !c.Win.Stale) {
		return SyncItem{}, false
	}
	return SyncItem{ChannelID: c.Info.ID, Loaded: c.Win.Loaded, SyncedAt: c.Win.SyncedAt, Priority: s.priorityLocked(c)}, true
}

func (s *Server) SyncItems() []SyncItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []SyncItem
	for _, c := range s.chans {
		if it, ok := s.syncItemLocked(c); ok {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return s.chans[out[i].ChannelID].Info.LastPostAt > s.chans[out[j].ChannelID].Info.LastPostAt
	})
	return out
}

func (s *Server) SyncItemFor(channelID string) (SyncItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chans[channelID]
	if c == nil {
		return SyncItem{}, false
	}
	return s.syncItemLocked(c)
}

// ---- pending (optimistic) posts ----

// AddPending shows a post being sent, with its attachments' local files.
func (s *Server) AddPending(channelID, rootID, message string, files ...FileView) Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	id := s.me.ID + ":" + strconv.FormatInt(now, 10)
	for n := 1; s.pendingIndexLocked(channelID, id) >= 0; n++ {
		id = s.me.ID + ":" + strconv.FormatInt(now, 10) + "-" + strconv.Itoa(n)
	}
	p := Pending{ID: id, ChannelID: channelID, RootID: rootID, Message: message, CreateAt: now, Files: slices.Clone(files)}
	s.pending[channelID] = append(s.pending[channelID], p)
	return p
}

func (s *Server) pendingIndexLocked(channelID, id string) int {
	return slices.IndexFunc(s.pending[channelID], func(p Pending) bool { return p.ID == id })
}

func (s *Server) FailPending(channelID, id string) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.pendingIndexLocked(channelID, id); i >= 0 {
		s.pending[channelID][i].Failed = true
	}
	return Change{Channels: []string{channelID}, Threads: s.pendingThreadsLocked(channelID, id)}
}

// RetryPending takes a failed post back to sending; one still being sent
// is left alone (a second send would race the first). The Change carries
// Threads the same way FailPending/DropPending do — only when the root is
// still held by the thread cache — so a retried reply refreshes the panel
// too, not just the channel feed.
func (s *Server) RetryPending(channelID, id string) (Pending, Change, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.pendingIndexLocked(channelID, id)
	if i < 0 || !s.pending[channelID][i].Failed {
		return Pending{}, Change{}, false
	}
	s.pending[channelID][i].Failed = false
	ch := Change{Channels: []string{channelID}, Threads: s.pendingThreadsLocked(channelID, id)}
	return s.pending[channelID][i], ch, true
}

func (s *Server) DropPending(channelID, id string) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	threads := s.pendingThreadsLocked(channelID, id)
	s.dropPendingLocked(channelID, id)
	return Change{Channels: []string{channelID}, Threads: threads}
}

func (s *Server) dropPendingLocked(channelID, id string) {
	if id == "" {
		return
	}
	s.pending[channelID] = slices.DeleteFunc(s.pending[channelID], func(p Pending) bool {
		if p.ID != id {
			return false
		}
		s.releaseLocked(p)
		return true
	})
}

// releaseLocked records the attachments of a pending post that is gone.
func (s *Server) releaseLocked(p Pending) {
	for _, f := range p.Files {
		s.released = append(s.released, f.ID)
	}
}

// TakeReleased gives the attachment ids of pending posts dropped since the
// last call — confirmed by the server, discarded, or gone with their
// channel — so their files can be let go of.
func (s *Server) TakeReleased() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.released
	s.released = nil
	return out
}

// ComposerKey names a message composer: a channel's own (Root "") or one
// of its threads' reply composer (attach.Store's key, minus the server id
// — the caller already knows that).
type ComposerKey struct {
	Channel string
	Root    string
}

// TakeForgottenComposers gives the composers (channel or thread) left
// since the last call — the channel was left, taking its held threads
// with it (forgetChannelLocked/forgetThreadsLocked) — so any attachment
// still staged there (never sent, so never part of a Pending's Files —
// those are covered by TakeReleased instead) can be let go of too. A
// thread merely evicted from the LRU cache while its channel stays open
// is not covered (docs/backlog.md).
func (s *Server) TakeForgottenComposers() []ComposerKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.forgotten
	s.forgotten = nil
	return out
}

// FileProgress is a staged file's live upload state, fed to
// RefreshPendingProgress by the caller (api.Service, which holds the
// attachment store) without this package importing internal/attach.
type FileProgress struct {
	State string
	Sent  int64
	Error string
}

// RefreshPendingProgress applies fresh upload progress to the staged Files
// of channelID's pending posts: get is called with each file's id (the
// attachment id) and, when it reports ok, its State/Sent/Error replace
// what is shown. It reports whether anything actually changed — the
// caller only needs to tell the UI then. Pending posts are not persisted
// (they do not survive a restart), so this never marks anything dirty.
//
// A changed post's Files is replaced wholesale with a fresh clone rather
// than edited element by element: ChannelView hands out p.Files by its
// existing slice header (never deep-cloned — Task 7), so a caller reading
// an earlier snapshot without the lock must keep seeing that snapshot
// unchanged underneath it. Only the Pending's Files field (a slice header,
// touched solely under s.mu, like every other field here) is replaced.
func (s *Server) RefreshPendingProgress(channelID string, get func(id string) (FileProgress, bool)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	list := s.pending[channelID]
	for i := range list {
		var next []FileView // cloned lazily, only once something actually changes
		for j, fv := range list[i].Files {
			fp, ok := get(fv.ID)
			if !ok || (fv.State == fp.State && fv.Sent == fp.Sent && fv.Error == fp.Error) {
				continue
			}
			if next == nil {
				next = slices.Clone(list[i].Files)
			}
			next[j].State, next[j].Sent, next[j].Error = fp.State, fp.Sent, fp.Error
			changed = true
		}
		if next != nil {
			list[i].Files = next
		}
	}
	return changed
}

// PendingAttachments gives the attachment ids of every pending post.
func (s *Server) PendingAttachments() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, l := range s.pending {
		for _, p := range l {
			for _, f := range p.Files {
				out = append(out, f.ID)
			}
		}
	}
	return out
}

// ---- posts from the user's own actions ----

// PostCreated applies the REST response of CreatePost (the WS echo may come
// before or after it; both paths go through applyNewPostLocked).
func (s *Server) PostCreated(p model.Post) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[p.ChannelID]
	if ch == nil {
		return Change{}
	}
	s.applyNewPostLocked(ch, p, nil)
	return Change{Sidebar: true, Badge: true, Channels: []string{p.ChannelID}, Threads: s.threadsOfLocked(p)}
}

// applyNewPostLocked is the single path for a created post (WS posted or
// REST create). It updates the window and, once per post id, the counters.
// isNew: the post id was not seen before; bumped: the counters changed (a
// new post inside the post-bootstrap guard is new but not bumped).
func (s *Server) applyNewPostLocked(ch *Chan, p model.Post, mentions []string) (isNew, bumped bool) {
	crt := s.crtLocked()
	// Window/seen membership never comes from a page fetch (SetWindow,
	// MergeSince) — only this function records ids in seen. So a post
	// already displayed via a REST page but not yet seen here is still
	// new: its counters and reply-count bump have not happened yet.
	if s.gone.has(p.ID) {
		// Deleted already (our DeletePost, post_deleted): a late REST
		// response or echo must not bring it or its count back.
		return false, false
	}
	isNew = !s.seen.has(p.ID)
	s.seen.add(p.ID)
	s.dropPendingLocked(ch.Info.ID, p.PendingPostID)
	root := p.RootID == ""
	switch {
	case keep(p, crt):
		if ch.Win.Loaded {
			s.upsertLocked(ch, p)
		}
	}
	s.threadPostedLocked(p)
	if !root && isNew {
		s.eachRootLocked(ch, p.RootID, func(r *model.Post) bool { return replyPosted(r, p) })
	}
	if !isNew {
		return false, false
	}
	if g, ok := s.guard[ch.Info.ID]; ok && p.CreateAt <= g {
		return true, false
	}
	ch.Info.TotalMsgCount++
	ch.Info.LastPostAt = max(ch.Info.LastPostAt, p.CreateAt)
	if root {
		ch.Info.TotalMsgCountRoot++
		ch.Info.LastRootPostAt = max(ch.Info.LastRootPostAt, p.CreateAt)
	}
	if p.UserID == s.me.ID {
		// The server marks the channel viewed for our post, except a
		// reply under CRT (app/post.go, isCRTReply): that one is read in
		// its thread, and unread roots stay unread.
		if root || !crt {
			ch.Member.MsgCount, ch.Member.MsgCountRoot = ch.Info.TotalMsgCount, ch.Info.TotalMsgCountRoot
			ch.Member.LastViewedAt = max(ch.Member.LastViewedAt, p.CreateAt)
		}
	} else if slices.Contains(mentions, s.me.ID) {
		ch.Member.MentionCount++
		if root {
			ch.Member.MentionCountRoot++
		}
	}
	s.dirty.chans[ch.Info.ID] = true
	return true, true
}

// ApplyPostUpdate applies an edited post (REST patch response or event).
func (s *Server) ApplyPostUpdate(p model.Post) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updatePostLocked(p)
}

// updatePostLocked applies an edit to every copy held: the window, the
// open channel's history, the thread cache.
func (s *Server) updatePostLocked(p model.Post) Change {
	ch := s.chans[p.ChannelID]
	if ch == nil {
		return Change{}
	}
	var c Change
	if s.threadUpdatedLocked(p) {
		c.Threads = s.threadsOfLocked(p)
	}
	changed := false
	if indexOf(ch.Win.Posts, p.ID) >= 0 {
		s.upsertLocked(ch, p)
		changed = true
	}
	if ch.Info.ID == s.active {
		if i := indexOf(s.older, p.ID); i >= 0 && p.UpdateAt >= s.older[i].UpdateAt {
			keepNewerReplies(&p, s.older[i])
			s.older[i] = p
			changed = true
		}
	}
	if changed {
		c.Channels = []string{p.ChannelID}
	}
	return c
}

// RemovePost applies our own DeletePost. The post is looked up first for
// its channel and root (the feed or the thread cache); one not held is left
// to the post_deleted event, which carries both.
func (s *Server) RemovePost(postID string) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.findPostLocked(postID)
	if !ok {
		return Change{}
	}
	ch := s.chans[p.ChannelID]
	if ch == nil {
		return Change{}
	}
	c := Change{Threads: s.threadsOfLocked(p)}
	if s.removeLocked(ch, p) {
		c.Channels = []string{p.ChannelID}
	}
	return c
}

func (s *Server) FindPost(postID string) (model.Post, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findPostLocked(postID)
}

func (s *Server) findPostLocked(postID string) (model.Post, bool) {
	for _, ch := range s.chans {
		if i := indexOf(ch.Win.Posts, postID); i >= 0 {
			return ch.Win.Posts[i], true
		}
	}
	if i := indexOf(s.older, postID); i >= 0 {
		return s.older[i], true
	}
	if p, _, ok := s.threadPostLocked(postID); ok {
		return p, true
	}
	return model.Post{}, false
}

// reactLocked applies a reaction to every copy of its post. server: it is
// the server's (an event), not our optimistic click — the server moved the
// post's update_at to that moment (reaction_store.go
// updatePostForReactionsOn*), so the copy's does too: a page read before it
// then does not win over the live reaction (SetThreadPage).
func (s *Server) reactLocked(channelID string, r model.Reaction, add, server bool) bool {
	ch := s.chans[channelID]
	if ch == nil {
		return false
	}
	apply := func(p *model.Post) bool {
		if p.ID != r.PostID {
			return false
		}
		if server { // even for a reaction already shown: our own click's echo
			p.UpdateAt = max(p.UpdateAt, r.CreateAt, r.UpdateAt, r.DeleteAt)
		}
		var reacts []model.Reaction
		files := []model.FileInfo(nil)
		if p.Metadata != nil {
			reacts = append(reacts, p.Metadata.Reactions...)
			files = p.Metadata.Files
		}
		same := func(x model.Reaction) bool { return x.UserID == r.UserID && x.EmojiName == r.EmojiName }
		if add {
			if slices.IndexFunc(reacts, same) >= 0 {
				return false
			}
			reacts = append(reacts, r)
		} else {
			n := len(reacts)
			if reacts = slices.DeleteFunc(reacts, same); len(reacts) == n {
				return false
			}
		}
		p.Metadata = &model.PostMetadata{Files: files, Reactions: reacts}
		return true
	}
	// Every copy: the feed (window or history) and the thread cache.
	changed := s.reactThreadsLocked(channelID, r.PostID, apply)
	if i := indexOf(ch.Win.Posts, r.PostID); i >= 0 && apply(&ch.Win.Posts[i]) {
		s.dirty.posts[channelID] = true
		return true
	}
	if channelID == s.active {
		if i := indexOf(s.older, r.PostID); i >= 0 && apply(&s.older[i]) {
			return true
		}
	}
	return changed
}

// ---- drafts, active channel, read state ----

func (s *Server) SetDraft(channelID, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafts[channelID] == text {
		return
	}
	if text == "" {
		delete(s.drafts, channelID)
	} else {
		s.drafts[channelID] = text
	}
	if s.chans[channelID] != nil {
		s.dirty.chans[channelID] = true
	}
}

// SetThreadDraft is SetDraft for a reply: keyed by root id instead of
// channel, bounded to ThreadDraftCap entries (the least-recently-updated
// one is dropped), never in the snapshot. The caller (api.Service) checks
// ThreadHeld before calling — a draft for a root that is not held is
// simply not reachable through ThreadView, so this itself does not check.
func (s *Server) SetThreadDraft(rootID, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.threadDrafts[rootID] == text {
		return
	}
	if text == "" {
		s.dropThreadDraftLocked(rootID)
		return
	}
	// threadDraftOrder is least-recently-updated first: every write (new
	// or an edit of an existing draft) moves rootID to the most-recent
	// end, so eviction below drops the draft nobody has touched in the
	// longest time — not simply the first one ever created, which would
	// otherwise evict an actively-typed-in draft out from under the user
	// once 50 other threads got one.
	s.threadDraftOrder = slices.DeleteFunc(s.threadDraftOrder, func(id string) bool { return id == rootID })
	s.threadDraftOrder = append(s.threadDraftOrder, rootID)
	s.threadDrafts[rootID] = text
	for len(s.threadDraftOrder) > ThreadDraftCap {
		oldest := s.threadDraftOrder[0]
		s.threadDraftOrder = s.threadDraftOrder[1:]
		delete(s.threadDrafts, oldest)
	}
}

// SetActive makes channelID the open channel and returns its "new messages"
// boundary: last_viewed_at at the moment it was opened. Re-opening the same
// channel keeps the boundary.
func (s *Server) SetActive(channelID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if channelID == s.active {
		return s.newSince
	}
	s.resetHistoryLocked()
	s.active, s.suppressView, s.newSince = channelID, "", 0
	if ch := s.chans[channelID]; ch != nil {
		s.newSince = ch.Member.LastViewedAt
		team := ch.Info.TeamID
		if team == "" {
			team = s.nav.TeamID
		}
		if s.nav.Channel == nil {
			s.nav.Channel = map[string]string{}
		}
		s.nav.Channel[team] = channelID
		s.dirty.meta = true
	}
	return s.newSince
}

func (s *Server) Active() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *Server) SetFocused(f bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.focused = f
}

func (s *Server) Focused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.focused
}

// NeedsView reports whether the channel has something to mark read and the
// user did not just mark it unread on purpose.
func (s *Server) NeedsView(channelID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil || s.suppressView == channelID {
		return false
	}
	if s.crtLocked() {
		// Replies are read in their threads (spike §4.1 п.7).
		return ch.Info.TotalMsgCountRoot > ch.Member.MsgCountRoot || ch.Member.MentionCountRoot > 0
	}
	return ch.Info.TotalMsgCount > ch.Member.MsgCount || ch.Info.TotalMsgCountRoot > ch.Member.MsgCountRoot ||
		ch.Member.MentionCount > 0 || ch.Member.MentionCountRoot > 0
}

func (s *Server) markReadLocked(ch *Chan, at int64) {
	ch.Member.MsgCount, ch.Member.MsgCountRoot = ch.Info.TotalMsgCount, ch.Info.TotalMsgCountRoot
	ch.Member.MentionCount, ch.Member.MentionCountRoot, ch.Member.UrgentMentionCount = 0, 0, 0
	ch.Member.LastViewedAt = max(ch.Member.LastViewedAt, at)
	s.dirty.chans[ch.Info.ID] = true
}

func (s *Server) ViewedLocally(channelID string, at int64) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch := s.chans[channelID]; ch != nil {
		s.markReadLocked(ch, at)
	}
	return Change{Sidebar: true, Badge: true}
}

func (s *Server) setUnreadLocked(u model.ChannelUnreadAt) {
	ch := s.chans[u.ChannelID]
	if ch == nil {
		return
	}
	ch.Member.MsgCount, ch.Member.MsgCountRoot = u.MsgCount, u.MsgCountRoot
	ch.Member.MentionCount, ch.Member.MentionCountRoot = u.MentionCount, u.MentionCountRoot
	ch.Member.UrgentMentionCount, ch.Member.LastViewedAt = u.UrgentMentionCount, u.LastViewedAt
	s.dirty.chans[u.ChannelID] = true
}

// SetUnread applies a "mark as unread" result. If it is the open channel,
// auto-marking it read is suppressed until the user leaves it.
func (s *Server) SetUnread(u model.ChannelUnreadAt) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setUnreadLocked(u)
	if u.ChannelID == s.active {
		s.suppressView = u.ChannelID
	}
	return Change{Sidebar: true, Badge: true, Channels: []string{u.ChannelID}}
}

// ClearGuard ends the post-bootstrap window during which posted events
// already reflected in the REST counters must not bump them again.
func (s *Server) ClearGuard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guard = map[string]int64{}
	s.threadGuard = false
}
