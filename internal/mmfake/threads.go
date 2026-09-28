package mmfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// fthread exists once a root post has at least one reply
// (server/public/model/thread.go: "Thread metadata does not exist until the
// first reply to a root post"). ReplyCount/LastReplyAt/DeleteAt are not
// duplicated here — they live on the root *fpost and are read from there,
// so there is exactly one place that can drift.
type fthread struct {
	teamID       string   // "" for DM/GM, like the real server
	participants []string // by seniority; the root author only if they replied
}

// threadMembership is one user's subscription to a thread — following is
// always true once created (Task 1 has no unfollow endpoint).
type threadMembership struct {
	following      bool
	lastViewed     int64
	unreadMentions int64
}

// ThreadRead records one PUT .../threads/{id}/read/{ts} call (test API).
type ThreadRead struct {
	RootID string
	TeamID string
	TS     int64
}

func (s *Server) threadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v4/posts/{pid}/thread", s.handleAuthed(s.postThread))
	mux.HandleFunc("GET /api/v4/users/{uid}/teams/unread", s.handleAuthed(s.teamsUnread))
	mux.HandleFunc("GET /api/v4/users/{uid}/teams/{tid}/threads", s.handleAuthed(s.userThreads))
	mux.HandleFunc("PUT /api/v4/users/{uid}/teams/{tid}/threads/{rid}/read/{ts}", s.handleAuthed(s.markThreadRead))
}

// asMe rejects any {uid} that is not the caller ("me" or their own id) —
// every write/read here is scoped to the session's own user.
func asMe(w http.ResponseWriter, r *http.Request, u User) bool {
	if uid := r.PathValue("uid"); uid != "me" && uid != u.ID {
		appError(w, 403, "api.context.permissions.app_error", "not you")
		return false
	}
	return true
}

// repliesLocked lists a thread's non-deleted, non-history replies, oldest
// first by CreateAt (nowLocked() guarantees no ties within one fake run).
func (s *Server) repliesLocked(rootID string) []*fpost {
	root := s.chat.byID[rootID]
	if root == nil {
		return nil
	}
	var out []*fpost
	for _, p := range s.chat.posts[root.ChannelID] {
		if p.RootID == rootID && p.DeleteAt == 0 && p.OriginalID == "" {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreateAt != out[j].CreateAt {
			return out[i].CreateAt < out[j].CreateAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *Server) unreadRepliesLocked(rootID string, since int64) int64 {
	var n int64
	for _, p := range s.repliesLocked(rootID) {
		if p.CreateAt > since {
			n++
		}
	}
	return n
}

// unreadRepliesExcludingLocked is unreadRepliesLocked without excludeID —
// used to snapshot "previous" unread counts before the reply that triggers
// the snapshot has landed in the thread.
func (s *Server) unreadRepliesExcludingLocked(rootID string, since int64, excludeID string) int64 {
	var n int64
	for _, p := range s.repliesLocked(rootID) {
		if p.ID == excludeID {
			continue
		}
		if p.CreateAt > since {
			n++
		}
	}
	return n
}

func (s *Server) participantUsersLocked(ids []string) []model.User {
	out := make([]model.User, 0, len(ids))
	for _, id := range ids {
		if u, ok := s.userByID(id); ok {
			out = append(out, s.userWithPictureLocked(u))
		}
	}
	return out
}

// prefLocked reports a preference's value and whether the row exists at
// all — crtForLocked needs to tell "no preference" (fall back to the
// config default) apart from "preference present but not \"on\"".
func (s *Server) prefLocked(userID, category, name string) (string, bool) {
	for _, p := range s.chat.prefs[userID] {
		if p.Category == category && p.Name == name {
			return p.Value, true
		}
	}
	return "", false
}

// crtForLocked mirrors IsCRTEnabledForUser (app/channel.go:2883-2897)
// exactly: disabled -> never, always_on -> always; default_on/default_off
// set the *default* (fix round 1: this used to treat default_on and
// default_off asymmetrically — "off" vs. "on" — instead of matching the
// real server, where the config only sets the default and, once a
// preference row exists at all, enabled is decided solely by whether its
// value is exactly "on", the same test for both modes).
func (s *Server) crtForLocked(userID string) bool {
	switch s.chat.crtMode {
	case "always_on":
		return true
	case "default_on", "default_off":
		enabled := s.chat.crtMode == "default_on"
		if v, ok := s.prefLocked(userID, "display_settings", "collapsed_reply_threads"); ok {
			enabled = v == "on"
		}
		return enabled
	default: // "disabled" or unset
		return false
	}
}

// threadResponseLocked builds ThreadResponse as userID sees it: reply_count/
// last_reply_at come straight off the root post (the single source of
// truth), unread_replies/unread_mentions off that user's own membership.
func (s *Server) threadResponseLocked(rootID, userID string) model.ThreadResponse {
	root := s.chat.byID[rootID]
	if root == nil {
		return model.ThreadResponse{PostID: rootID}
	}
	m := s.chat.threadMembers[rootID][userID]
	var lastViewed, unreadMentions int64
	if m != nil {
		lastViewed, unreadMentions = m.lastViewed, m.unreadMentions
	}
	post := root.Post
	out := model.ThreadResponse{
		PostID:         rootID,
		ReplyCount:     root.ReplyCount,
		LastReplyAt:    root.LastReplyAt,
		LastViewedAt:   lastViewed,
		Post:           &post,
		UnreadMentions: unreadMentions,
		UnreadReplies:  s.unreadRepliesLocked(rootID, lastViewed),
		DeleteAt:       root.DeleteAt,
	}
	if th := s.chat.threads[rootID]; th != nil {
		out.Participants = s.participantUsersLocked(th.participants)
	}
	return out
}

// subscribeReplyLocked updates the thread record and subscriptions for a
// reply that has already been inserted into s.chat.posts, and returns the
// post's followers (every subscriber but the replier, for the "posted"
// event) plus a notify func the caller runs after publishing "posted" — the
// real server sends the reply's own posted event before any per-follower
// thread_updated (notification.go:660-850). Auto-subscribes root author,
// replier and anyone this reply mentions (app/post.go:438-446,
// notification.go:227-320).
func (s *Server) subscribeReplyLocked(c *model.Channel, root, reply *fpost, now int64) (followers []string, notify func()) {
	th := s.chat.threads[root.ID]
	if th == nil {
		th = &fthread{teamID: c.TeamID}
		s.chat.threads[root.ID] = th
		s.chat.threadMembers[root.ID] = map[string]*threadMembership{}
	}
	members := s.chat.threadMembers[root.ID]

	type prev struct{ mentions, replies int64 }
	prevOf := make(map[string]prev, len(members))
	for uid, m := range members {
		prevOf[uid] = prev{mentions: m.unreadMentions, replies: s.unreadRepliesExcludingLocked(root.ID, m.lastViewed, reply.ID)}
	}

	ensure := func(uid string) *threadMembership {
		m := members[uid]
		if m == nil {
			m = &threadMembership{following: true}
			members[uid] = m
		}
		return m
	}
	ensure(root.UserID)
	ensure(reply.UserID)
	for _, uid := range reply.mentions {
		m := ensure(uid)
		if uid != reply.UserID {
			m.unreadMentions++
		}
	}

	if !slices.Contains(th.participants, reply.UserID) {
		th.participants = append(th.participants, reply.UserID)
	}
	// The commenter reads their own reply instantly (app/post.go:67-77,
	// notification.go:801-823) — no unread left for it.
	members[reply.UserID].lastViewed = now
	members[reply.UserID].unreadMentions = 0

	for uid := range members {
		if uid != reply.UserID {
			followers = append(followers, uid)
		}
	}
	sort.Strings(followers)

	notify = func() {
		for uid := range members {
			if !s.crtForLocked(uid) {
				continue
			}
			before := prevOf[uid]
			payload, _ := json.Marshal(s.threadResponseLocked(root.ID, uid))
			s.publishLocked("thread_updated", map[string]any{
				"thread":                   string(payload),
				"previous_unread_mentions": before.mentions,
				"previous_unread_replies":  before.replies,
			}, wsBroadcast{UserID: uid, TeamID: c.TeamID}, []string{uid}, nil, nil)
		}
	}
	return followers, notify
}

// deleteReplyEffectsLocked mirrors updateThreadAfterReplyDeletion: deleting
// one reply decrements the root's reply_count and any subscriber's
// unread_mentions the reply had contributed. No separate event — the
// post_deleted the caller already publishes covers it (§3 of the spike).
func (s *Server) deleteReplyEffectsLocked(p *fpost, now int64) {
	root := s.chat.byID[p.RootID]
	if root == nil {
		return
	}
	root.ReplyCount = max(root.ReplyCount-1, 0)
	// post_store.go Delete: the root's update_at moves to the deletion
	// time; updateThreadAfterReplyDeletion recomputes last_reply_at from
	// the replies left.
	root.UpdateAt = now
	root.LastReplyAt = 0
	for _, r := range s.repliesLocked(root.ID) {
		root.LastReplyAt = max(root.LastReplyAt, r.CreateAt)
	}
	for uid, m := range s.chat.threadMembers[p.RootID] {
		if slices.Contains(p.mentions, uid) {
			m.unreadMentions = max(m.unreadMentions-1, 0)
		}
	}
}

// postThread is GET /api/v4/posts/{pid}/thread.
func (s *Server) postThread(w http.ResponseWriter, r *http.Request, u User) {
	pid := r.PathValue("pid")
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()

	root := s.chat.byID[pid]
	if root == nil || root.DeleteAt != 0 || root.OriginalID != "" {
		appError(w, 404, "app.post.get.app_error", "not found")
		return
	}
	if !s.isMemberLocked(root.ChannelID, u.ID) {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	// A reply's id is answered like the server: under CRT the reply as
	// order[0] and no replies (it selects RootId = id; post_store.go:575-697).

	hasPerPage := q.Get("perPage") != ""
	var perPage int
	if hasPerPage {
		n, err := strconv.Atoi(q.Get("perPage"))
		if err != nil || n > 200 {
			appError(w, 400, "api.context.invalid_param.app_error", "perPage")
			return
		}
		perPage = n
	}
	fromPost := q.Get("fromPost")
	var fromCreateAt int64
	if v := q.Get("fromCreateAt"); v != "" {
		var err error
		fromCreateAt, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			appError(w, 400, "api.context.invalid_param.app_error", "fromCreateAt")
			return
		}
	}
	if fromPost != "" && fromCreateAt == 0 {
		appError(w, 400, "api.context.invalid_param.app_error", "fromPost requires fromCreateAt")
		return
	}
	direction := q.Get("direction")
	if direction != "down" {
		direction = "up" // the client always pages up; treat "" the same way
	}

	replies := s.repliesLocked(root.ID)
	if direction == "up" {
		slices.Reverse(replies)
	}
	if fromCreateAt != 0 {
		filtered := replies[:0:0]
		for _, p := range replies {
			var keep bool
			if direction == "up" {
				keep = p.CreateAt < fromCreateAt || (p.CreateAt == fromCreateAt && fromPost != "" && p.ID < fromPost)
			} else {
				keep = p.CreateAt > fromCreateAt || (p.CreateAt == fromCreateAt && fromPost != "" && p.ID > fromPost)
			}
			if keep {
				filtered = append(filtered, p)
			}
		}
		replies = filtered
	}

	var hasNext bool
	if perPage > 0 && len(replies) > perPage {
		hasNext = true
		replies = replies[:perPage]
	}

	list := model.PostList{Order: []string{root.ID}, Posts: map[string]model.Post{root.ID: root.Post}}
	for _, p := range replies {
		list.Order = append(list.Order, p.ID)
		list.Posts[p.ID] = p.Post
	}
	// has_next is always present (post_store.go:709,894 set it
	// unconditionally) — without perPage there is no LIMIT, so it is always
	// false (the whole thread was returned), never nil. Fix round 1: this
	// used to only appear when perPage was given.
	list.HasNext = &hasNext
	writeJSON(w, 200, list)
}

// teamsUnread is GET /api/v4/users/{uid}/teams/unread.
func (s *Server) teamsUnread(w http.ResponseWriter, r *http.Request, u User) {
	if !asMe(w, r, u) {
		return
	}
	includeThreads := r.URL.Query().Get("include_collapsed_threads") == "true"
	s.mu.Lock()
	defer s.mu.Unlock()

	byTeam := map[string]*model.TeamUnread{}
	get := func(teamID string) *model.TeamUnread {
		tu := byTeam[teamID]
		if tu == nil {
			tu = &model.TeamUnread{TeamID: teamID}
			byTeam[teamID] = tu
		}
		return tu
	}
	for cid, c := range s.chat.channels {
		if c.DeleteAt != 0 || c.TeamID == "" { // DM/GM have no team
			continue
		}
		m := s.chat.members[cid][u.ID]
		if m == nil {
			continue
		}
		tu := get(c.TeamID)
		tu.MentionCount += m.MentionCount
		tu.MentionCountRoot += m.MentionCountRoot
		if !m.Muted() {
			tu.MsgCount += c.TotalMsgCount - m.MsgCount
			tu.MsgCountRoot += c.TotalMsgCountRoot - m.MsgCountRoot
		}
	}
	if includeThreads && s.chat.crtMode != "disabled" {
		for rootID, th := range s.chat.threads {
			if th.teamID == "" { // DM/GM threads are excluded here (thread_store.go ThreadTeamId IN teamIDs)
				continue
			}
			m := s.chat.threadMembers[rootID][u.ID]
			root := s.chat.byID[rootID]
			if m == nil || !m.following || root == nil || root.DeleteAt != 0 {
				continue
			}
			tu := get(th.teamID)
			if root.LastReplyAt > m.lastViewed {
				tu.ThreadCount++
			}
			tu.ThreadMentionCount += m.unreadMentions
		}
	}
	out := make([]model.TeamUnread, 0, len(byTeam))
	for _, tu := range byTeam {
		out = append(out, *tu)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TeamID < out[j].TeamID })
	writeJSON(w, 200, out)
}

// userThreads is GET /api/v4/users/{uid}/teams/{tid}/threads.
func (s *Server) userThreads(w http.ResponseWriter, r *http.Request, u User) {
	if !asMe(w, r, u) {
		return
	}
	tid := r.PathValue("tid")
	q := r.URL.Query()
	totalsOnly, threadsOnly := q.Get("totalsOnly") == "true", q.Get("threadsOnly") == "true"
	if totalsOnly && threadsOnly {
		appError(w, 400, "api.getThreadsForUser.bad_only_params", "totalsOnly and threadsOnly are mutually exclusive")
		return
	}
	excludeDirect := q.Get("excludeDirect") == "true"

	s.mu.Lock()
	defer s.mu.Unlock()
	out := model.ThreadTotals{Threads: []model.ThreadResponse{}}
	// Deterministic order: by root id (creation order isn't preserved by a map).
	roots := make([]string, 0, len(s.chat.threads))
	for rootID := range s.chat.threads {
		roots = append(roots, rootID)
	}
	sort.Strings(roots)
	for _, rootID := range roots {
		th := s.chat.threads[rootID]
		root := s.chat.byID[rootID]
		if root == nil || root.DeleteAt != 0 {
			continue
		}
		m := s.chat.threadMembers[rootID][u.ID]
		if m == nil || !m.following {
			continue
		}
		if excludeDirect {
			if th.teamID != tid {
				continue
			}
		} else if th.teamID != tid && th.teamID != "" {
			continue
		}
		out.Total++
		if root.LastReplyAt > m.lastViewed {
			out.TotalUnreadThreads++
		}
		out.TotalUnreadMentions += m.unreadMentions
		if !totalsOnly {
			out.Threads = append(out.Threads, s.threadResponseLocked(rootID, u.ID))
		}
	}
	writeJSON(w, 200, out)
}

// markThreadRead is PUT /api/v4/users/{uid}/teams/{tid}/threads/{rid}/read/{ts}.
func (s *Server) markThreadRead(w http.ResponseWriter, r *http.Request, u User) {
	if !asMe(w, r, u) {
		return
	}
	tid, rid := r.PathValue("tid"), r.PathValue("rid")
	ts, err := strconv.ParseInt(r.PathValue("ts"), 10, 64)
	if err != nil {
		appError(w, 400, "api.context.invalid_param.app_error", "ts")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.chat.threadMembers[rid][u.ID]
	if m == nil || !m.following {
		appError(w, 404, "app.user.get_thread_membership_for_user.not_found", "not subscribed")
		return
	}
	prevMentions, prevReplies := m.unreadMentions, s.unreadRepliesLocked(rid, m.lastViewed)
	m.lastViewed = ts
	m.unreadMentions = s.mentionsAfterLocked(rid, u.ID, ts)

	root := s.chat.byID[rid]
	channelID := ""
	if root != nil {
		channelID = root.ChannelID
	}
	s.chat.threadReads = append(s.chat.threadReads, ThreadRead{RootID: rid, TeamID: tid, TS: ts})
	resp := s.threadResponseLocked(rid, u.ID)
	s.publishLocked("thread_read_changed", map[string]any{
		"thread_id": rid, "timestamp": ts, "unread_mentions": m.unreadMentions, "unread_replies": resp.UnreadReplies,
		"previous_unread_mentions": prevMentions, "previous_unread_replies": prevReplies, "channel_id": channelID,
	}, wsBroadcast{UserID: u.ID, TeamID: tid}, []string{u.ID}, nil, nil)
	writeJSON(w, 200, resp)
}

func (s *Server) mentionsAfterLocked(rootID, userID string, since int64) int64 {
	var n int64
	for _, p := range s.repliesLocked(rootID) {
		if p.CreateAt > since && slices.Contains(p.mentions, userID) {
			n++
		}
	}
	return n
}

// ---- test controls ----

// SetCollapsedThreads changes the fake's CollapsedThreads config at runtime
// (Options.CRT only seeds the initial value): "disabled"|"default_on"|
// "default_off"|"always_on". A signed-in client sees it after its next
// bootstrap, like the other runtime config setters in this package.
func (s *Server) SetCollapsedThreads(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chat.crtMode = mode
}

// ThreadReads lists every PUT .../threads/{id}/read/{ts} call made so far.
func (s *Server) ThreadReads() []ThreadRead {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ThreadRead(nil), s.chat.threadReads...)
}

// Following reports whether username is subscribed to rootID's thread.
func (s *Server) Following(rootID, username string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.chat.threadMembers[rootID][s.userIDByName(username)]
	return m != nil && m.following
}

// SeedThread creates a root post by username in channelID, then `replies`
// replies alternating bob/carol as authors, and returns the root's id.
func (s *Server) SeedThread(channelID, username string, replies int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, e := s.createPostLocked(s.userIDByName(username), model.Post{ChannelID: channelID, Message: "Thread root"})
	if e != nil {
		panic("mmfake: SeedThread: " + e.id)
	}
	authors := []string{"bob", "carol"}
	for i := 0; i < replies; i++ {
		if _, e := s.createPostLocked(s.userIDByName(authors[i%2]), model.Post{
			ChannelID: channelID, RootID: root.ID, Message: fmt.Sprintf("Reply %d", i+1),
		}); e != nil {
			panic("mmfake: SeedThread: " + e.id)
		}
	}
	return root.ID
}
