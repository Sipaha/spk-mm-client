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
// channel with history already loaded (s.older non-empty), the trimmed
// (oldest) posts are not discarded — they are still visible above the
// window, so they move to the end of s.older instead.
func (s *Server) trimWindowLocked(ch *Chan) {
	w := &ch.Win
	if len(w.Posts) <= WindowSize {
		return
	}
	cut := len(w.Posts) - WindowSize
	trimmed := append([]model.Post(nil), w.Posts[:cut]...)
	w.Posts = append([]model.Post(nil), w.Posts[cut:]...)
	w.Complete = false
	if ch.Info.ID == s.active && len(s.older) > 0 {
		s.older = append(s.older, trimmed...)
		sortPosts(s.older)
	}
}

// keepNewerReplies gives p (a fresh read of a post held as local) the
// local reply count when p's own is older: the server moves a root's
// update_at to each reply's time, so a root whose update_at is behind the
// newest reply applied live was read before it.
func keepNewerReplies(p *model.Post, local model.Post) {
	if p.UpdateAt < local.LastReplyAt {
		p.ReplyCount, p.LastReplyAt = local.ReplyCount, local.LastReplyAt
	}
}

// keepRepliesOnUpdate is keepNewerReplies for a single post's update (an
// edit echo, a since= row): one without a reply count keeps the local one.
func keepRepliesOnUpdate(p *model.Post, local model.Post) {
	if p.ReplyCount == 0 {
		p.ReplyCount, p.LastReplyAt = local.ReplyCount, local.LastReplyAt
	}
	keepNewerReplies(p, local)
}

func (s *Server) upsertLocked(ch *Chan, p model.Post) {
	if i := indexOf(ch.Win.Posts, p.ID); i >= 0 {
		if p.UpdateAt >= ch.Win.Posts[i].UpdateAt {
			keepRepliesOnUpdate(&p, ch.Win.Posts[i])
			// An update that doesn't carry pending_post_id (e.g. a plain
			// edit echo) must not erase the value the confirmation set —
			// the frontend keys its feed row by it across the pending ->
			// confirmed swap.
			if p.PendingPostID == "" {
				p.PendingPostID = ch.Win.Posts[i].PendingPostID
			}
			ch.Win.Posts[i] = p
		}
	} else {
		ch.Win.Posts = append(ch.Win.Posts, p)
		sortPosts(ch.Win.Posts)
		s.trimWindowLocked(ch)
	}
	s.dirty.posts[ch.Info.ID] = true
}

// removeLocked deletes the post d and its replies (they die with the
// root). A deleted reply lowers its root's count — see replyGoneLocked.
// d.RootID may be unknown ("") and d.UpdateAt 0 (not the deletion time).
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
	return s.replyGoneLocked(ch, d) || changed
}

// replyGoneLocked lowers the root's reply count for the deleted reply d,
// once per id however many paths report it (REST DeletePost, post_deleted,
// a since= page) — the gone ring — and never below 0. A root read at or
// after the deletion (the server moves its update_at to the deletion time)
// already has the lower count and is left alone.
func (s *Server) replyGoneLocked(ch *Chan, d model.Post) bool {
	if d.RootID == "" || s.gone.has(d.ID) {
		return false
	}
	s.gone.add(d.ID)
	return s.eachRootLocked(ch, d.RootID, func(r *model.Post) bool {
		if r.ReplyCount == 0 || (d.UpdateAt != 0 && r.UpdateAt >= d.UpdateAt) {
			return false
		}
		r.ReplyCount--
		return true
	})
}

// eachRootLocked applies f to every copy of the post rootID held for ch —
// the window and, for the open channel, the loaded history — and reports
// whether f changed any.
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
	return changed
}

// replyPosted updates root for its new reply p: the reply's own
// reply_count is the thread's total at its moment (the server fills it
// in), taken unless an even newer reply was applied already; without it
// (0), a reply not counted yet (new id, newer than LastReplyAt) adds one.
func replyPosted(root *model.Post, p model.Post, isNew bool) bool {
	switch {
	case p.ReplyCount > 0 && p.CreateAt >= root.LastReplyAt:
		if root.ReplyCount == p.ReplyCount && root.LastReplyAt == p.CreateAt {
			return false
		}
		root.ReplyCount = p.ReplyCount
	case p.ReplyCount == 0 && isNew && p.CreateAt > root.LastReplyAt:
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
// active channel is dropped unless the new page still reaches the old
// window's first post: a catch-up overflow reloads only the latest page, and
// keeping history across that hole would hide it; HasMore then follows the
// new window.
func (s *Server) SetWindow(channelID string, page []model.Post, complete bool, syncedAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil {
		return
	}
	crt := s.crtLocked()
	var merged []model.Post
	var newest int64
	for _, p := range page {
		if keep(p, crt) {
			if i := indexOf(ch.Win.Posts, p.ID); i >= 0 {
				keepNewerReplies(&p, ch.Win.Posts[i])
			} else if i := indexOf(s.older, p.ID); channelID == s.active && i >= 0 {
				keepNewerReplies(&p, s.older[i])
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
	if channelID == s.active && len(s.older) > 0 {
		// s.older joins the old window's first post. If the new window
		// still reaches down to it there is no hole: keep the history
		// (minus what the window now holds); otherwise drop it.
		if len(ch.Win.Posts) > 0 && len(merged) > 0 && merged[0].CreateAt <= ch.Win.Posts[0].CreateAt {
			s.older = slices.DeleteFunc(s.older, func(p model.Post) bool { return p.CreateAt >= merged[0].CreateAt })
		} else {
			s.older, s.olderComplete = nil, false
		}
	}
	ch.Win = Window{Posts: merged, Loaded: true, Complete: complete, SyncedAt: syncedAt}
	s.trimWindowLocked(ch)
	s.dirty.posts[channelID] = true
}

// MergeSince applies a posts?since= response (edits, deletions, new posts)
// and marks the window caught up.
func (s *Server) MergeSince(channelID string, posts []model.Post, syncedAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil {
		return
	}
	crt := s.crtLocked()
	var oldest int64
	if len(ch.Win.Posts) > 0 && !ch.Win.Complete {
		oldest = ch.Win.Posts[0].CreateAt
	}
	for _, p := range posts {
		switch {
		case p.OriginalID != "":
			continue // edit-history row
		case p.DeleteAt > 0:
			s.removeLocked(ch, p)
		case !keep(p, crt):
			continue
		case indexOf(ch.Win.Posts, p.ID) >= 0 || p.CreateAt >= oldest:
			s.upsertLocked(ch, p)
		}
	}
	ch.Win.Loaded, ch.Win.Stale, ch.Win.GapAfter = true, false, ""
	ch.Win.SyncedAt = max(ch.Win.SyncedAt, syncedAt)
	s.dirty.posts[channelID] = true
}

// AppendOlder adds a page of history above the window while the channel is
// open. It lives only in memory and is dropped when the user leaves.
func (s *Server) AppendOlder(channelID string, posts []model.Post, complete bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil || channelID != s.active {
		return
	}
	crt := s.crtLocked()
	var add []model.Post
	for _, p := range posts {
		if keep(p, crt) && indexOf(s.older, p.ID) < 0 && indexOf(ch.Win.Posts, p.ID) < 0 {
			add = append(add, p)
		}
	}
	s.older = append(add, s.older...)
	sortPosts(s.older)
	s.olderComplete = complete
}

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

// ResetWindows forgets every post window (CRT was toggled: the windows hold
// the wrong kind of posts). Channels are refetched by the worker.
func (s *Server) ResetWindows() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.chans {
		ch.Win = Window{}
		s.dirty.posts[id] = true
	}
	s.older, s.olderComplete = nil, false
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
func (s *Server) MarkStale(liveUntil int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return Change{Channels: []string{channelID}}
}

// RetryPending takes a failed post back to sending; one still being sent
// is left alone (a second send would race the first).
func (s *Server) RetryPending(channelID, id string) (Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.pendingIndexLocked(channelID, id)
	if i < 0 || !s.pending[channelID][i].Failed {
		return Pending{}, false
	}
	s.pending[channelID][i].Failed = false
	return s.pending[channelID][i], true
}

func (s *Server) DropPending(channelID, id string) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropPendingLocked(channelID, id)
	return Change{Channels: []string{channelID}}
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
	return Change{Sidebar: true, Badge: true, Channels: []string{p.ChannelID}}
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
	if !root {
		s.eachRootLocked(ch, p.RootID, func(r *model.Post) bool { return replyPosted(r, p, isNew) })
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
	if s.updatePostLocked(p) {
		return Change{Channels: []string{p.ChannelID}}
	}
	return Change{}
}

func (s *Server) updatePostLocked(p model.Post) bool {
	ch := s.chans[p.ChannelID]
	if ch == nil {
		return false
	}
	changed := false
	if indexOf(ch.Win.Posts, p.ID) >= 0 {
		s.upsertLocked(ch, p)
		changed = true
	}
	if ch.Info.ID == s.active {
		if i := indexOf(s.older, p.ID); i >= 0 && p.UpdateAt >= s.older[i].UpdateAt {
			keepRepliesOnUpdate(&p, s.older[i])
			s.older[i] = p
			changed = true
		}
	}
	return changed
}

// RemovePost applies our own DeletePost. The post is looked up first for
// its channel and root; one not held (a reply under CRT) is left to the
// post_deleted event, which carries both.
func (s *Server) RemovePost(postID string) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.findPostLocked(postID)
	if !ok {
		return Change{}
	}
	if ch := s.chans[p.ChannelID]; ch != nil && s.removeLocked(ch, model.Post{ID: p.ID, ChannelID: p.ChannelID, RootID: p.RootID}) {
		return Change{Channels: []string{p.ChannelID}}
	}
	return Change{}
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
	return model.Post{}, false
}

func (s *Server) reactLocked(channelID string, r model.Reaction, add bool) bool {
	ch := s.chans[channelID]
	if ch == nil {
		return false
	}
	apply := func(p *model.Post) bool {
		if p.ID != r.PostID {
			return false
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
	for i := range ch.Win.Posts {
		if apply(&ch.Win.Posts[i]) {
			s.dirty.posts[channelID] = true
			return true
		}
	}
	if channelID == s.active {
		for i := range s.older {
			if apply(&s.older[i]) {
				return true
			}
		}
	}
	return false
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

// SetActive makes channelID the open channel and returns its "new messages"
// boundary: last_viewed_at at the moment it was opened. Re-opening the same
// channel keeps the boundary.
func (s *Server) SetActive(channelID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if channelID == s.active {
		return s.newSince
	}
	s.active, s.older, s.olderComplete, s.suppressView, s.newSince = channelID, nil, false, "", 0
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
}
