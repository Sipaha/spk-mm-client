package state

import (
	"slices"
	"sort"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// The thread cache: the thread open in the panel plus the ThreadCacheSize-1
// most recently open ones, only in memory (never in the snapshot). Each
// holds its root and at most ThreadMaxReplies replies; a thread that is
// not open holds at most ThreadPage. So it is bounded — about 1 MB of heap
// per server — whatever the user opens. See AGENTS.md.
const (
	ThreadCacheSize  = 3
	ThreadMaxReplies = 200
	ThreadPage       = 60
	// ThreadDraftCap bounds s.threadDrafts (SetThreadDraft): the
	// least-recently-updated one is dropped past it. In memory only,
	// never in the snapshot — see AGENTS.md.
	ThreadDraftCap = 50
	// ThreadNotFound is FailThread's code for a root the server does not
	// have (404: deleted): the thread shows as RootDeleted.
	ThreadNotFound = "not_found"
)

type thread struct {
	channelID   string
	root        model.Post   // ID "" until read (or held in the feed)
	replies     []model.Post // oldest → newest, ≤ ThreadMaxReplies
	complete    bool         // replies start at the thread's first reply
	capped      bool         // ThreadMaxReplies reached: no older pages
	loaded      bool
	stale       bool // the event stream was lost: replies may be missing
	rootDeleted bool
	err         string // code of the last failed load
	// focus: the open thread showing a reply beyond those it holds
	// (threadfocus.go); replies are then its tail, ≤ ThreadPage.
	focus *threadFocus
}

// ThreadView is the thread panel: the root, its replies (oldest first) and
// our replies being sent. NewSince/GapAfter are always empty — the fields
// keep the shape of ChannelView so the UI shares its feed.
type ThreadView struct {
	RootID      string     `json:"root_id"`
	ChannelID   string     `json:"channel_id"`
	ChannelName string     `json:"channel_name"`
	TeamName    string     `json:"team_name"`
	Posts       []PostView `json:"posts"`
	HasMore     bool       `json:"has_more"`
	Capped      bool       `json:"capped"`
	Loaded      bool       `json:"loaded"`
	Syncing     bool       `json:"syncing"`
	RootDeleted bool       `json:"root_deleted"`
	Error       string     `json:"error"`
	Draft       string     `json:"draft"`
	MeID        string     `json:"me_id"`
	CRT         bool       `json:"crt"`
	NewSince    int64      `json:"new_since"`
	GapAfter    string     `json:"gap_after"`
	// Focus: the segment around a reply (OpenThreadAt), nil for a plain
	// thread. Posts then holds the root, the segment and the tail.
	Focus *ThreadFocusView `json:"focus"`
}

func (t *thread) needsFetch() bool { return !t.rootDeleted && (!t.loaded || t.stale || t.err != "") }

// OpenThread makes rootID's thread the open one (the one opened before it
// becomes recent) and moves it to the front of the cache; the least recent
// beyond ThreadCacheSize is let go of. ok=false: channelID is not known.
// needFetch: the thread has to be (re)read — never loaded, stale since a
// gap, or its last load failed. epoch is what its pages are applied with.
func (s *Server) OpenThread(channelID, rootID string) (epoch uint64, needFetch bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openThreadLocked(channelID, rootID)
}

func (s *Server) openThreadLocked(channelID, rootID string) (epoch uint64, needFetch bool, ok bool) {
	if s.chans[channelID] == nil || rootID == "" {
		return s.threadEpoch, false, false
	}
	t := s.threads[rootID]
	if t != nil && t.channelID != channelID {
		s.dropThreadLocked(rootID)
		t = nil
	}
	if s.openThread != "" && s.openThread != rootID {
		s.trimThreadLocked(s.threads[s.openThread])
	}
	if s.openThread != rootID {
		s.openSeq++
		s.openRead = threadRead{opening: s.openSeq} // a new opening
		s.bumpFocusLocked(nil)                      // a focus begun before is not wanted
	}
	if t == nil {
		t = &thread{channelID: channelID}
		if p, ok := s.findPostLocked(rootID); ok && p.RootID == "" && p.ChannelID == channelID {
			t.root = p // shown while the page loads
		}
		s.threads[rootID] = t
	}
	s.openThread = rootID
	s.touchThreadLocked(rootID)
	return s.threadEpoch, t.needsFetch(), true
}

// touchThreadLocked moves id to the most recent end of the LRU and lets go
// of the least recent threads beyond ThreadCacheSize.
func (s *Server) touchThreadLocked(id string) {
	s.threadLRU = slices.DeleteFunc(s.threadLRU, func(x string) bool { return x == id })
	s.threadLRU = append(s.threadLRU, id)
	for len(s.threadLRU) > ThreadCacheSize {
		old := s.threadLRU[0]
		s.threadLRU = slices.Delete(s.threadLRU, 0, 1)
		delete(s.threads, old)
	}
}

// dropThreadLocked lets go of one thread entirely.
func (s *Server) dropThreadLocked(id string) {
	delete(s.threads, id)
	s.threadLRU = slices.DeleteFunc(s.threadLRU, func(x string) bool { return x == id })
	if s.openThread == id {
		s.openThread, s.openRead = "", threadRead{}
	}
}

// dropThreadDraftLocked forgets rootID's draft, if any (SetThreadDraft,
// forgetThreadsLocked).
func (s *Server) dropThreadDraftLocked(rootID string) {
	if _, ok := s.threadDrafts[rootID]; !ok {
		return
	}
	delete(s.threadDrafts, rootID)
	s.threadDraftOrder = slices.DeleteFunc(s.threadDraftOrder, func(x string) bool { return x == rootID })
}

// trimThreadLocked keeps only the last page of a thread that is no longer
// open, in a fresh slice (the old backing array goes); its focus ends.
func (s *Server) trimThreadLocked(t *thread) {
	if t == nil {
		return
	}
	s.dropFocusLocked(t)
	if n := len(t.replies); n > ThreadPage {
		t.replies = slices.Clone(t.replies[n-ThreadPage:])
		t.complete = false
	}
	t.capped = false
}

// CloseThread closes the panel: the thread becomes recent, trimmed to its
// last page.
func (s *Server) CloseThread() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openThread == "" {
		return
	}
	s.trimThreadLocked(s.threads[s.openThread])
	s.openThread, s.openRead = "", threadRead{}
	s.bumpFocusLocked(nil) // a focus begun is not wanted either
}

// ThreadRootOf maps a held reply's id to its root's (a reply has no thread
// of its own; the server refuses a reply to a reply); any other id is
// returned as is.
func (s *Server) ThreadRootOf(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.findPostLocked(id); ok && p.RootID != "" {
		return p.RootID
	}
	return id
}

// RedirectThread handles a thread opened as fromID that turned out to be a reply's
// (its page's order[0] has a root_id). The entry goes; if it was open, the
// root's thread opens instead. ThreadView(fromID) then shows the root's
// thread (its RootID tells the UI the real root) — only the last redirect
// is remembered. ok=false: fromID is not held (nothing to redirect).
func (s *Server) RedirectThread(fromID, toID string) (epoch uint64, need, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[fromID]
	if t == nil || toID == "" || toID == fromID {
		return s.threadEpoch, false, false
	}
	open := s.openThread == fromID
	s.dropThreadLocked(fromID)
	s.threadRedirect = [2]string{fromID, toID}
	if !open {
		return s.threadEpoch, false, true
	}
	return s.openThreadLocked(t.channelID, toID)
}

func (s *Server) OpenThreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openThread
}

// ThreadHeld reports whether rootID is a thread this server's cache holds
// (open or one of the recent ones — OpenThread/RedirectThread put it
// there; an evicted or never-opened root is not held even if it is a real
// post). When channelID is not empty, the thread must also belong to it —
// attachments and SendReply know the channel and check both; a
// channel-less caller (SaveThreadDraft) checks the root alone.
func (s *Server) ThreadHeld(channelID, rootID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	if t == nil {
		return false
	}
	return channelID == "" || t.channelID == channelID
}

// ThreadFetch is what a thread load needs, read together: the CRT mode,
// the epoch to apply the page with, and whether rootID is still held and
// needs a read at all.
func (s *Server) ThreadFetch(rootID string) (crt bool, epoch uint64, need bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	return s.crtLocked(), s.threadEpoch, t != nil && t.needsFetch()
}

// threadPageLocked splits a thread page into its root (ID "" if absent) and
// its live replies, oldest first; hasNext: older replies exist.
func (s *Server) threadPageLocked(rootID string, l model.PostList) (root model.Post, replies []model.Post, hasNext bool) {
	for _, id := range l.Order {
		p, ok := l.Posts[id]
		switch {
		case !ok:
		case id == rootID:
			root = p
		case p.RootID == rootID && p.DeleteAt == 0 && p.OriginalID == "" && !s.gone.has(p.ID):
			replies = append(replies, p)
		}
	}
	sortReplies(replies)
	return root, replies, l.HasNext != nil && *l.HasNext
}

// SetThreadPage installs the latest page of a thread (GET …/thread,
// direction=up). Like SetWindow: replies newer than the page arrived over
// WS while it was in flight and are kept (the page was read no later than
// its root's update_at/last_reply_at, which the server moves to every
// reply); anything older the page did not reach is dropped. A page of
// another epoch (ResetThreads, MarkStale) or of a thread let go of is
// ignored.
func (s *Server) SetThreadPage(rootID string, epoch uint64, l model.PostList) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	if t == nil || epoch != s.threadEpoch || t.rootDeleted {
		return
	}
	root, page, hasNext := s.threadPageLocked(rootID, l)
	if s.gone.has(rootID) || root.DeleteAt != 0 {
		s.rootGoneLocked(t)
		return
	}
	t.loaded, t.stale, t.err = true, false, ""
	cut := int64(0)
	if root.ID != "" {
		cut = max(root.UpdateAt, root.LastReplyAt)
		switch {
		case t.root.ID != rootID:
			t.root = root
		case t.root.UpdateAt > root.UpdateAt: // edited or reacted to live since the read
		default:
			keepNewerReplies(&root, t.root)
			t.root = root
		}
	}
	for _, p := range page {
		cut = max(cut, p.CreateAt)
	}
	merged := page
	for _, p := range t.replies {
		if i := indexOf(merged, p.ID); i >= 0 {
			if p.UpdateAt > merged[i].UpdateAt { // an edit or reaction applied live
				merged[i] = p
			}
		} else if p.CreateAt > cut {
			merged = append(merged, p)
			if t.root.ID != "" {
				replyPosted(&t.root, p)
			}
		}
	}
	sortReplies(merged)
	if t.focus != nil {
		s.setTailLocked(t, merged, threadReplies(rootID, l), hasNext)
	} else {
		t.replies, t.complete, t.capped = merged, !hasNext, false
		s.capThreadLocked(t)
	}
	if rootID != s.openThread { // closed while the page was in flight
		s.trimThreadLocked(t)
	}
}

// AppendOlderReplies adds a page of older replies (fromCreateAt/fromPost)
// above a loaded thread, up to ThreadMaxReplies; then the thread is capped
// and loads nothing older. before is the page's cursor (fromPost): the page
// applies only while that reply is still the oldest held — not after the
// thread was evicted and read again, or once a twin request landed. Pages
// of another epoch are ignored too.
func (s *Server) AppendOlderReplies(rootID string, epoch uint64, before string, l model.PostList) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	if t == nil || epoch != s.threadEpoch || !t.loaded || t.capped || t.rootDeleted || t.focus != nil ||
		len(t.replies) == 0 || t.replies[0].ID != before {
		return
	}
	_, page, hasNext := s.threadPageLocked(rootID, l)
	var add []model.Post
	for _, p := range page {
		if indexOf(t.replies, p.ID) < 0 {
			add = append(add, p)
		}
	}
	t.replies = append(add, t.replies...)
	sortReplies(t.replies)
	t.complete = !hasNext
	s.capThreadLocked(t)
	if rootID != s.openThread {
		s.trimThreadLocked(t)
	}
}

// ThreadHasMore reports whether older replies of a held thread can be loaded.
func (s *Server) ThreadHasMore(rootID string) (more, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	if t == nil {
		return false, false
	}
	return t.loaded && !t.complete && !t.capped && t.focus == nil, true
}

// capThreadLocked keeps the newest ThreadMaxReplies; a thread at the cap
// with older replies left is capped (nothing older is loaded).
func (s *Server) capThreadLocked(t *thread) {
	n := len(t.replies)
	switch {
	case n > ThreadMaxReplies:
		t.replies = slices.Clone(t.replies[n-ThreadMaxReplies:])
		t.capped, t.complete = true, false
	case n == ThreadMaxReplies && !t.complete:
		t.capped = true
	}
}

// FailThread records a failed load: ThreadNotFound marks the root deleted,
// any other code is shown (the UI retries by opening the thread again).
func (s *Server) FailThread(rootID string, epoch uint64, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	if t == nil || epoch != s.threadEpoch {
		return
	}
	if code == ThreadNotFound {
		s.rootGoneLocked(t)
		return
	}
	t.err = code
}

// rootGoneLocked: the root was deleted — its replies die with it. That is
// final: nothing is left to load.
func (s *Server) rootGoneLocked(t *thread) {
	s.dropFocusLocked(t)
	t.rootDeleted, t.loaded, t.stale, t.err = true, true, false, ""
	t.root, t.replies = model.Post{}, nil
	t.complete, t.capped = true, false
}

// MarkThreadStale marks rootID's cached thread stale, so the next check of
// needsFetch (loadThread) re-reads it — without bumping the whole cache's
// epoch (markThreadsStaleLocked, a connection gap). Used when a write
// hints the root might be gone without saying so for certain (CreatePost's
// api.post.create_post.root_id.app_error also covers an unrelated store
// error and, defensively, "root is itself a reply" — mm-10.11
// server/channels/app/post.go:296,305): the reread's own 404 (→
// FailThread(ThreadNotFound)) or success is the actual verdict, never
// this call by itself. ok=false: rootID is not held (nothing to recheck).
func (s *Server) MarkThreadStale(rootID string) (ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	if t == nil {
		return false
	}
	t.stale = true
	if t.focus != nil {
		s.bumpFocusLocked(t.focus)
	}
	return true
}

// OldestReply is the cursor for the next older page: the oldest reply held.
func (s *Server) OldestReply(rootID string) (id string, createAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.threads[rootID]; t != nil && len(t.replies) > 0 {
		return t.replies[0].ID, t.replies[0].CreateAt
	}
	return "", 0
}

// ResetThreads forgets every thread (CRT switched, or the worker stops) and
// drops pages in flight (the epoch moves). The open thread stays open as an
// empty entry to be read again; with no panel open nothing is left.
func (s *Server) ResetThreads() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threadEpoch++
	s.bumpFocusLocked(nil)
	open := s.threads[s.openThread]
	clear(s.threads)
	s.threadLRU = nil
	if open == nil {
		s.openThread, s.openRead = "", threadRead{}
		return
	}
	s.threads[s.openThread] = &thread{channelID: open.channelID, root: open.root}
	s.threadLRU = []string{s.openThread}
}

// markThreadsStaleLocked: the event stream was lost. Every thread may miss
// replies; pages in flight may predate the gap and are dropped.
func (s *Server) markThreadsStaleLocked() {
	s.threadEpoch++
	for _, t := range s.threads {
		t.stale = true
		if t.focus != nil {
			s.staleFocusLocked(t)
		}
	}
}

// forgetThreadsLocked lets go of the threads of a channel we left, and any
// drafts of their roots (a draft of a root the cache no longer holds — LRU
// eviction, not a channel leave — is left in place: ThreadDraftCap bounds
// it either way).
func (s *Server) forgetThreadsLocked(channelID string) {
	for id, t := range s.threads {
		if t.channelID == channelID {
			s.dropThreadDraftLocked(id)
			// The thread's reply composer: leaving the channel is the one
			// case (of "a thread's composer becomes unreachable") this
			// cache release covers — a thread merely evicted from the LRU
			// while the channel stays open does not (docs/backlog.md).
			s.forgotten = append(s.forgotten, ComposerKey{Channel: channelID, Root: id})
			s.dropThreadLocked(id)
		}
	}
}

// ---- server read state (CRT) ----

// threadRead is the server read state of the open thread, for this
// opening only (a new opening starts over).
type threadRead struct {
	opening  uint64 // which opening (openSeq): a late result of another is ignored
	ts       int64  // ts of the last read sent: replies up to it are read (0: none yet)
	noFollow bool   // the read answered 404: we do not follow the thread
}

// ThreadReadReq is one read to send: PUT …/teams/{Team}/threads/{root}/read/{TS};
// Opening goes back with its result (ThreadReadDone, ThreadNotFollowing).
type ThreadReadReq struct {
	Team    string
	TS      int64
	Opening uint64
}

// ThreadReadTarget is the read to send for rootID, if one should be now
// (PUT …/teams/{team}/threads/{root}/read/{ts}): CRT is on (without it the
// channel view reads threads), rootID is open in the panel over its channel
// and loaded (what is read must be on screen), the window is focused (the
// api layer focuses only the server on screen), we follow the thread (no
// 404 this opening) and a reply by someone else held is newer than the last
// read — or none was sent yet this opening. Team: the channel's, a DM/GM's
// the current one. TS: the newest held reply's create_at — never our clock
// when there are replies, so a clock running ahead cannot mark replies not
// shown yet read; our clock only for a thread without replies.
func (s *Server) ThreadReadTarget(rootID string) (ThreadReadReq, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rootID == "" || rootID != s.openThread || !s.focused || s.openRead.noFollow || !s.crtLocked() {
		return ThreadReadReq{}, false
	}
	t := s.threads[rootID]
	if t == nil || !t.loaded || t.rootDeleted || t.channelID != s.active {
		return ThreadReadReq{}, false
	}
	ch := s.chans[t.channelID]
	if ch == nil {
		return ThreadReadReq{}, false
	}
	team := ch.Info.TeamID
	if isDirect(ch) || team == "" {
		team = s.navTeamLocked()
	}
	var newest, others int64
	t.eachReply(func(p *model.Post) {
		newest = max(newest, p.CreateAt)
		if p.UserID != s.me.ID {
			others = max(others, p.CreateAt)
		}
	})
	if team == "" || (s.openRead.ts > 0 && others <= s.openRead.ts) {
		return ThreadReadReq{}, false
	}
	if newest == 0 {
		newest = s.now().UnixMilli()
	}
	return ThreadReadReq{Team: team, TS: newest, Opening: s.openRead.opening}, true
}

// ThreadReadDone records a read of rootID sent with ts in opening.
func (s *Server) ThreadReadDone(rootID string, opening uint64, ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openReadLocked(rootID, opening) {
		s.openRead.ts = max(s.openRead.ts, ts)
	}
}

// ThreadNotFollowing records a read answered 404 (no thread membership):
// no more reads this opening until our own reply in the thread or a
// thread_updated about it (both mean we follow it).
func (s *Server) ThreadNotFollowing(rootID string, opening uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openReadLocked(rootID, opening) {
		s.openRead.noFollow = true
	}
}

// openReadLocked: rootID is open in the same opening a result belongs to.
func (s *Server) openReadLocked(rootID string, opening uint64) bool {
	return rootID != "" && rootID == s.openThread && opening == s.openRead.opening
}

// ---- live updates (s.mu held) ----

// threadPostedLocked puts a new or repeated reply p into its cached thread
// (loaded or in flight — SetThreadPage keeps what is newer than its page).
// At the cap the oldest goes; a reply older than a partial thread's
// oldest is left to paging.
func (s *Server) threadPostedLocked(p model.Post) {
	t := s.threads[p.RootID]
	if p.RootID == "" || t == nil || t.rootDeleted || p.DeleteAt != 0 {
		return
	}
	if q := t.reply(p.ID); q != nil {
		if p.UpdateAt >= q.UpdateAt {
			if p.PendingPostID == "" {
				p.PendingPostID = q.PendingPostID
			}
			*q = p
		}
		return
	}
	if !t.complete && len(t.replies) > 0 && cmpReplies(p, t.replies[0]) < 0 {
		return
	}
	if t.focus != nil { // the tail: its oldest go (trimTailLocked)
		t.replies = append(t.replies, p)
		sortReplies(t.replies)
		s.trimTailLocked(t)
		return
	}
	// The open thread holds up to the cap; a recent one stays at its last
	// page (older replies are read again when it is scrolled).
	limit := ThreadPage
	if p.RootID == s.openThread {
		limit = ThreadMaxReplies
	}
	if len(t.replies) >= limit { // room made in place: the array does not grow
		t.replies = slices.Delete(t.replies, 0, len(t.replies)-limit+1)
		t.complete = false
		t.capped = limit == ThreadMaxReplies
	}
	t.replies = append(t.replies, p)
	sortReplies(t.replies)
}

// threadUpdatedLocked applies an edit to the cached copies of p.
func (s *Server) threadUpdatedLocked(p model.Post) bool {
	changed := false
	if t := s.threads[p.ID]; t != nil && t.root.ID == p.ID && p.UpdateAt >= t.root.UpdateAt {
		q := p
		keepNewerReplies(&q, t.root)
		t.root = q
		changed = true
	}
	if t := s.threads[p.RootID]; p.RootID != "" && t != nil {
		if q := t.reply(p.ID); q != nil && p.UpdateAt >= q.UpdateAt {
			if p.PendingPostID == "" {
				p.PendingPostID = q.PendingPostID
			}
			*q = p
			changed = true
		}
	}
	return changed
}

// threadRemovedLocked applies the deletion of d to the cache: a root marks
// its thread deleted, a reply leaves it (its root's count is lowered by
// replyGoneLocked).
func (s *Server) threadRemovedLocked(d model.Post) {
	if t := s.threads[d.ID]; t != nil {
		s.rootGoneLocked(t)
	}
	if t := s.threads[d.RootID]; d.RootID != "" && t != nil {
		gone := func(p model.Post) bool { return p.ID == d.ID }
		t.replies = slices.DeleteFunc(t.replies, gone)
		if t.focus != nil {
			t.focus.replies = slices.DeleteFunc(t.focus.replies, gone)
		}
	}
}

// threadsOfLocked: the cached threads a post event about p touches — its
// own (a root) and its root's (a reply).
func (s *Server) threadsOfLocked(p model.Post) []string {
	var out []string
	if s.threads[p.ID] != nil {
		out = append(out, p.ID)
	}
	if p.RootID != "" && s.threads[p.RootID] != nil {
		out = append(out, p.RootID)
	}
	return out
}

// threadsHoldingLocked: the cached threads that show postID.
func (s *Server) threadsHoldingLocked(postID string) []string {
	var out []string
	for id, t := range s.threads {
		if t.root.ID == postID || t.reply(postID) != nil {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// threadPostLocked finds postID in the thread cache.
func (s *Server) threadPostLocked(postID string) (model.Post, *thread, bool) {
	for _, t := range s.threads {
		if t.root.ID == postID {
			return t.root, t, true
		}
		if q := t.reply(postID); q != nil {
			return *q, t, true
		}
	}
	return model.Post{}, nil, false
}

// reactThreadsLocked applies a reaction to the cached copies of its post.
func (s *Server) reactThreadsLocked(channelID, postID string, apply func(*model.Post) bool) bool {
	changed := false
	for _, t := range s.threads {
		if t.channelID != channelID {
			continue
		}
		if t.root.ID == postID && apply(&t.root) {
			changed = true
		}
		if q := t.reply(postID); q != nil && apply(q) {
			changed = true
		}
	}
	return changed
}

// pendingThreadsLocked: the thread a pending post of channelID belongs to.
func (s *Server) pendingThreadsLocked(channelID, id string) []string {
	if i := s.pendingIndexLocked(channelID, id); i >= 0 {
		if r := s.pending[channelID][i].RootID; r != "" && s.threads[r] != nil {
			return []string{r}
		}
	}
	return nil
}

// ---- view ----

// ThreadView renders a cached thread for the panel.
func (s *Server) ThreadView(rootID string) (ThreadView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[rootID]
	if t == nil && s.threadRedirect[0] == rootID {
		rootID = s.threadRedirect[1]
		t = s.threads[rootID]
	}
	if t == nil {
		return ThreadView{}, false
	}
	v := ThreadView{
		RootID: rootID, ChannelID: t.channelID, Posts: []PostView{},
		HasMore: t.loaded && !t.complete && !t.capped, Capped: t.capped, Loaded: t.loaded,
		Syncing: (!t.loaded || t.stale) && t.err == "", RootDeleted: t.rootDeleted, Error: t.err,
		Draft: s.threadDrafts[rootID], MeID: s.me.ID, CRT: s.crtLocked(),
	}
	if ch := s.chans[t.channelID]; ch != nil {
		v.ChannelName, v.TeamName = s.channelNameLocked(ch), s.teamNameLocked(ch)
	}
	if t.root.ID != "" {
		v.Posts = append(v.Posts, s.postViewLocked(t.root))
	}
	replies := t.replies
	if t.focus != nil {
		v.Focus, replies = focusViewLocked(t)
		v.HasMore, v.Capped = false, false
	}
	replies = s.withEphemeralLocked(replies, func(p model.Post) bool { return p.RootID == rootID })
	for _, p := range replies {
		v.Posts = append(v.Posts, s.postViewLocked(p))
	}
	pend := slices.Clone(s.pending[t.channelID])
	sort.SliceStable(pend, func(i, j int) bool { return pend[i].CreateAt < pend[j].CreateAt })
	for _, p := range pend {
		if p.RootID == rootID {
			v.Posts = append(v.Posts, s.pendingViewLocked(p))
		}
	}
	return v, true
}
