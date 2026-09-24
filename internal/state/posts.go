package state

import (
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

type Pending struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	RootID    string `json:"root_id,omitempty"`
	Message   string `json:"message"`
	CreateAt  int64  `json:"create_at"`
	Failed    bool   `json:"failed,omitempty"`
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

func (s *Server) upsertLocked(ch *Chan, p model.Post) {
	if i := indexOf(ch.Win.Posts, p.ID); i >= 0 {
		if p.UpdateAt >= ch.Win.Posts[i].UpdateAt {
			if p.ReplyCount == 0 {
				p.ReplyCount, p.LastReplyAt = ch.Win.Posts[i].ReplyCount, ch.Win.Posts[i].LastReplyAt
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

// removeLocked deletes a post and its replies (they die with the root).
func (s *Server) removeLocked(ch *Chan, id string) bool {
	gone := func(p model.Post) bool { return p.ID == id || p.RootID == id }
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
	return changed
}

// SetWindow installs the latest page of a channel. Posts already in the
// window that are newer than the page arrived over WS while the request was
// in flight — they are kept.
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
			s.removeLocked(ch, p.ID)
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

// MarkStale records that the event stream was lost. Every loaded window may
// miss posts after its last one; it stays readable and is caught up by the worker.
func (s *Server) MarkStale(liveUntil int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.chans {
		if !ch.Win.Loaded || ch.Win.Stale {
			continue
		}
		ch.Win.SyncedAt = max(ch.Win.SyncedAt, liveUntil)
		ch.Win.Stale = true
		ch.Win.GapAfter = ""
		if n := len(ch.Win.Posts); n > 0 {
			ch.Win.GapAfter = ch.Win.Posts[n-1].ID
		}
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

func (s *Server) AddPending(channelID, rootID, message string) Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	id := s.me.ID + ":" + strconv.FormatInt(now, 10)
	for n := 1; s.pendingIndexLocked(channelID, id) >= 0; n++ {
		id = s.me.ID + ":" + strconv.FormatInt(now, 10) + "-" + strconv.Itoa(n)
	}
	p := Pending{ID: id, ChannelID: channelID, RootID: rootID, Message: message, CreateAt: now}
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

func (s *Server) RetryPending(channelID, id string) (Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.pendingIndexLocked(channelID, id)
	if i < 0 {
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
	s.pending[channelID] = slices.DeleteFunc(s.pending[channelID], func(p Pending) bool { return p.ID == id })
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
func (s *Server) applyNewPostLocked(ch *Chan, p model.Post, mentions []string) (bumped bool) {
	crt := s.crtLocked()
	// Window/seen membership never comes from a page fetch (SetWindow,
	// MergeSince) — only this function records ids in seen. So a post
	// already displayed via a REST page but not yet seen here is still
	// new: its counters and reply-count bump have not happened yet.
	isNew := !s.seen.has(p.ID)
	s.seen.add(p.ID)
	s.dropPendingLocked(ch.Info.ID, p.PendingPostID)
	root := p.RootID == ""
	switch {
	case keep(p, crt):
		if ch.Win.Loaded {
			s.upsertLocked(ch, p)
		}
	case crt && !root && isNew:
		// The root may already have this reply's count from a REST fetch
		// (its ReplyCount/LastReplyAt fields came straight from the
		// server); only bump if this reply is not already reflected there.
		if i := indexOf(ch.Win.Posts, p.RootID); i >= 0 && p.CreateAt > ch.Win.Posts[i].LastReplyAt {
			ch.Win.Posts[i].ReplyCount++
			ch.Win.Posts[i].LastReplyAt = p.CreateAt
			s.dirty.posts[ch.Info.ID] = true
		}
	}
	if !isNew {
		return false
	}
	if g, ok := s.guard[ch.Info.ID]; ok && p.CreateAt <= g {
		return false
	}
	ch.Info.TotalMsgCount++
	ch.Info.LastPostAt = max(ch.Info.LastPostAt, p.CreateAt)
	if root {
		ch.Info.TotalMsgCountRoot++
		ch.Info.LastRootPostAt = max(ch.Info.LastRootPostAt, p.CreateAt)
	}
	if p.UserID == s.me.ID {
		ch.Member.MsgCount, ch.Member.MsgCountRoot = ch.Info.TotalMsgCount, ch.Info.TotalMsgCountRoot
		ch.Member.LastViewedAt = max(ch.Member.LastViewedAt, p.CreateAt)
	} else if slices.Contains(mentions, s.me.ID) {
		ch.Member.MentionCount++
		if root {
			ch.Member.MentionCountRoot++
		}
	}
	s.dirty.chans[ch.Info.ID] = true
	return true
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
			s.older[i] = p
			changed = true
		}
	}
	return changed
}

func (s *Server) RemovePost(postID string) Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.chans {
		if s.removeLocked(ch, postID) {
			return Change{Channels: []string{id}}
		}
	}
	return Change{}
}

func (s *Server) FindPost(postID string) (model.Post, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
