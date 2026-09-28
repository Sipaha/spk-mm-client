package mmfake

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

type fpost struct {
	model.Post
	mentions []string // user ids the server counted as mentioned
}

type chatData struct {
	teams       []model.Team
	channels    map[string]*model.Channel
	members     map[string]map[string]*model.ChannelMember // channel → user → member
	posts       map[string][]*fpost                        // channel → by CreateAt; includes deleted + history rows
	byID        map[string]*fpost
	pending     map[string]string // pending_post_id → post id
	prefs       map[string][]model.Preference
	status      map[string]string
	events      []RecordedEvent
	lastMs      int64
	sinceLimit  int
	failPosts   int
	failUploads int

	files      map[string]*ffile
	emoji      map[string]*femoji // by id
	pictures   map[string]*picture
	pictureSeq int
	usersSince []int64 // since= of every POST /users/ids that had one

	// Threads (Task 1 threads plan). crtMode is the fake's CollapsedThreads
	// config, mutable at runtime via SetCollapsedThreads (Options.CRT only
	// seeds its initial value): "disabled"|"default_on"|"default_off"|"always_on".
	crtMode       string
	threads       map[string]*fthread                     // root id → thread (exists once it has a reply)
	threadMembers map[string]map[string]*threadMembership // root id → user → membership
	threadReads   []ThreadRead                            // every PUT .../threads/{id}/read/{ts} call
	threadTries   int                                     // those calls, 404s included
}

type RecordedEvent struct {
	Name string
	To   []string
}

type wsBroadcast struct {
	UserID    string `json:"user_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	TeamID    string `json:"team_id,omitempty"`
}

// nowLocked returns strictly increasing ms so ordering by time is total.
func (s *Server) nowLocked() int64 {
	ms := time.Now().UnixMilli()
	if ms <= s.chat.lastMs {
		ms = s.chat.lastMs + 1
	}
	s.chat.lastMs = ms
	return ms
}

func (s *Server) chatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v4/users/me/teams", s.handleAuthed(s.myTeams))
	mux.HandleFunc("GET /api/v4/users/me/channels", s.handleAuthed(s.myChannels))
	mux.HandleFunc("GET /api/v4/users/me/channel_members", s.handleAuthed(s.myMembers))
	mux.HandleFunc("GET /api/v4/users/me/teams/{tid}/channels/categories", s.handleAuthed(s.categories))
	mux.HandleFunc("GET /api/v4/users/me/preferences", s.handleAuthed(s.getPrefs))
	mux.HandleFunc("PUT /api/v4/users/me/preferences", s.handleAuthed(s.putPrefs))
	mux.HandleFunc("POST /api/v4/users/me/preferences/delete", s.handleAuthed(s.deletePrefs))
	mux.HandleFunc("GET /api/v4/users/me/status", s.handleAuthed(s.getStatus))
	mux.HandleFunc("POST /api/v4/users/ids", s.handleAuthed(s.usersByIDs))
	mux.HandleFunc("GET /api/v4/channels/{cid}/posts", s.handleAuthed(s.channelPosts))
	mux.HandleFunc("POST /api/v4/posts", s.handleAuthed(s.createPost))
	mux.HandleFunc("PUT /api/v4/posts/{pid}/patch", s.handleAuthed(s.patchPost))
	mux.HandleFunc("DELETE /api/v4/posts/{pid}", s.handleAuthed(s.deletePost))
	mux.HandleFunc("POST /api/v4/channels/members/me/view", s.handleAuthed(s.viewChannel))
	mux.HandleFunc("POST /api/v4/users/me/posts/{pid}/set_unread", s.handleAuthed(s.setUnread))
}

func (s *Server) isMemberLocked(channelID, userID string) bool {
	return s.chat.members[channelID][userID] != nil
}

func (s *Server) myTeams(w http.ResponseWriter, _ *http.Request, _ User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, 200, s.chat.teams)
}

func (s *Server) myChannels(w http.ResponseWriter, _ *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.Channel{}
	for _, id := range s.sortedChannelIDsLocked() {
		if s.isMemberLocked(id, u.ID) && s.chat.channels[id].DeleteAt == 0 {
			out = append(out, *s.chat.channels[id])
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) sortedChannelIDsLocked() []string {
	ids := make([]string, 0, len(s.chat.channels))
	for id := range s.chat.channels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Server) myMembers(w http.ResponseWriter, r *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := []model.ChannelMember{}
	for _, id := range s.sortedChannelIDsLocked() {
		if m := s.chat.members[id][u.ID]; m != nil {
			all = append(all, *m)
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per <= 0 {
		per = 60
	}
	start := min(page*per, len(all))
	writeJSON(w, 200, all[start:min(start+per, len(all))])
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request, u User) {
	tid := r.PathValue("tid")
	s.mu.Lock()
	defer s.mu.Unlock()
	var chans, dms []string
	for _, id := range s.sortedChannelIDsLocked() {
		c := s.chat.channels[id]
		if !s.isMemberLocked(id, u.ID) || c.DeleteAt != 0 {
			continue
		}
		switch {
		case c.Type == model.ChannelDirect || c.Type == model.ChannelGroup:
			dms = append(dms, id)
		case c.TeamID == tid:
			chans = append(chans, id)
		}
	}
	mk := func(typ, name, sorting string, ids []string) model.SidebarCategory {
		if ids == nil {
			ids = []string{}
		}
		return model.SidebarCategory{ID: typ + "_" + u.ID + "_" + tid, TeamID: tid, Type: typ, DisplayName: name, Sorting: sorting, ChannelIDs: ids}
	}
	cats := []model.SidebarCategory{
		mk("favorites", "Favorites", "", nil),
		mk("channels", "Channels", "alpha", chans),
		mk("direct_messages", "Direct Messages", "recent", dms),
	}
	out := model.OrderedCategories{Categories: cats}
	for _, c := range cats {
		out.Order = append(out.Order, c.ID)
	}
	writeJSON(w, 200, out)
}

func (s *Server) getPrefs(w http.ResponseWriter, _ *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]model.Preference{}, s.chat.prefs[u.ID]...)
	writeJSON(w, 200, out)
}

func (s *Server) putPrefs(w http.ResponseWriter, r *http.Request, u User) {
	var in []model.Preference
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		appError(w, 400, "api.preference.bad_body", err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range in {
		p.UserID = u.ID
		s.upsertPrefLocked(p)
	}
	b, _ := json.Marshal(in)
	s.publishLocked("preferences_changed", map[string]any{"preferences": string(b)}, wsBroadcast{UserID: u.ID}, []string{u.ID}, nil, nil)
	writeJSON(w, 200, map[string]string{"status": "OK"})
}

func (s *Server) deletePrefs(w http.ResponseWriter, r *http.Request, u User) {
	var in []model.Preference
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		appError(w, 400, "api.preference.bad_body", err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range in {
		s.deletePrefLocked(u.ID, p.Category, p.Name)
	}
	b, _ := json.Marshal(in)
	s.publishLocked("preferences_deleted", map[string]any{"preferences": string(b)}, wsBroadcast{UserID: u.ID}, []string{u.ID}, nil, nil)
	writeJSON(w, 200, map[string]string{"status": "OK"})
}

func (s *Server) deletePrefLocked(userID, category, name string) {
	list := s.chat.prefs[userID]
	for i := range list {
		if list[i].Category == category && list[i].Name == name {
			s.chat.prefs[userID] = slices.Delete(list, i, i+1)
			return
		}
	}
}

func (s *Server) upsertPrefLocked(p model.Preference) {
	list := s.chat.prefs[p.UserID]
	for i := range list {
		if list[i].Category == p.Category && list[i].Name == p.Name {
			list[i] = p
			return
		}
	}
	s.chat.prefs[p.UserID] = append(list, p)
}

func (s *Server) getStatus(w http.ResponseWriter, _ *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.chat.status[u.ID]
	if st == "" {
		st = "online"
	}
	writeJSON(w, 200, model.Status{UserID: u.ID, Status: st})
}

// usersByIDs: ?since=ms keeps only users updated after it (update_at >
// since), as the real server does. A fake user's update_at is the time of
// its picture — the only profile change the fake makes.
func (s *Server) usersByIDs(w http.ResponseWriter, r *http.Request, _ User) {
	var ids []string
	_ = json.NewDecoder(r.Body).Decode(&ids)
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	out := []model.User{}
	s.mu.Lock()
	defer s.mu.Unlock()
	if since > 0 {
		s.chat.usersSince = append(s.chat.usersSince, since)
	}
	for _, id := range ids {
		if u, ok := s.userByID(id); ok {
			mu := s.userWithPictureLocked(u)
			if mu.UpdateAt > since {
				out = append(out, mu)
			}
		}
	}
	writeJSON(w, 200, out)
}

// userWithPictureLocked is the user's JSON with its picture version and the
// matching update_at.
func (s *Server) userWithPictureLocked(u User) model.User {
	mu := userJSON(u)
	if pic := s.chat.pictures[u.ID]; pic != nil {
		mu.LastPictureUpdate = pic.at
		mu.UpdateAt = max(pic.at, -pic.at)
	}
	return mu
}

// UsersSince lists the since= of every POST /users/ids that carried one.
func (s *Server) UsersSince() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.chat.usersSince...)
}

func visible(p *fpost, crt bool) bool {
	return p.DeleteAt == 0 && p.OriginalID == "" && (!crt || p.RootID == "")
}

func (s *Server) channelPosts(w http.ResponseWriter, r *http.Request, u User) {
	cid := r.PathValue("cid")
	q := r.URL.Query()
	crt := q.Get("collapsedThreads") == "true"
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isMemberLocked(cid, u.ID) {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	list := model.PostList{Order: []string{}, Posts: map[string]model.Post{}}
	all := s.chat.posts[cid]
	if since, _ := strconv.ParseInt(q.Get("since"), 10, 64); since > 0 {
		for i := len(all) - 1; i >= 0 && len(list.Order) < s.chat.sinceLimit; i-- {
			p := all[i]
			if p.UpdateAt <= since || (crt && p.RootID != "") {
				continue
			}
			list.Order = append(list.Order, p.ID)
			list.Posts[p.ID] = p.Post
		}
		writeJSON(w, 200, list)
		return
	}
	var vis []*fpost
	for _, p := range all {
		if visible(p, crt) {
			vis = append(vis, p)
		}
	}
	end := len(vis)
	if before := q.Get("before"); before != "" {
		end = slices.IndexFunc(vis, func(p *fpost) bool { return p.ID == before })
		if end < 0 {
			end = 0
		}
	}
	per, _ := strconv.Atoi(q.Get("per_page"))
	if per <= 0 {
		per = 60
	}
	per = min(per, 200)
	start := max(0, end-per)
	for i := end - 1; i >= start; i-- {
		list.Order = append(list.Order, vis[i].ID)
		list.Posts[vis[i].ID] = vis[i].Post
	}
	if start > 0 {
		list.PrevPostID = vis[start-1].ID
	}
	writeJSON(w, 200, list)
}

var mentionRe = regexp.MustCompile(`@([a-zA-Z0-9][a-zA-Z0-9._-]*)`)

// mentionsLocked mirrors the server: @username, @all/@channel/@here, and
// every DM message mentions the other member. The author is never mentioned.
func (s *Server) mentionsLocked(c *model.Channel, authorID, msg string) []string {
	set := map[string]bool{}
	for uid := range s.chat.members[c.ID] {
		if uid != authorID && c.Type == model.ChannelDirect {
			set[uid] = true
		}
	}
	for _, m := range mentionRe.FindAllStringSubmatch(msg, -1) {
		name := strings.ToLower(strings.TrimRight(m[1], "."))
		switch name {
		case "all", "channel", "here":
			for uid := range s.chat.members[c.ID] {
				set[uid] = true
			}
		default:
			for _, u := range s.opts.Users {
				if strings.ToLower(u.Username) == name && s.chat.members[c.ID][u.ID] != nil {
					set[u.ID] = true
				}
			}
		}
	}
	delete(set, authorID)
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (s *Server) memberIDsLocked(channelID string) []string {
	ids := make([]string, 0, len(s.chat.members[channelID]))
	for id := range s.chat.members[channelID] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Server) insertPostLocked(p *fpost) {
	list := append(s.chat.posts[p.ChannelID], p)
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreateAt < list[j].CreateAt })
	s.chat.posts[p.ChannelID] = list
	s.chat.byID[p.ID] = p
}

// trimHistoryLocked drops the oldest posts of a channel beyond
// Options.KeepPosts (soak runs). Only posts created at run time trigger it,
// so the seed stays whole until the channel gets a new post. A dropped root
// takes its thread record and subscriptions with it (a reply to it is then
// refused like one to a deleted root).
func (s *Server) trimHistoryLocked(channelID string) {
	keep := s.opts.KeepPosts
	list := s.chat.posts[channelID]
	if keep <= 0 || len(list) <= keep {
		return
	}
	cut := len(list) - keep
	for _, p := range list[:cut] {
		delete(s.chat.byID, p.ID)
		if p.PendingPostID != "" {
			delete(s.chat.pending, p.PendingPostID)
		}
		delete(s.chat.threads, p.ID)
		delete(s.chat.threadMembers, p.ID)
	}
	s.chat.posts[channelID] = append([]*fpost(nil), list[cut:]...)
}

type apiErr struct {
	status int
	id     string
}

// attachFilesLocked mirrors attachFileIDsToPost (app/post.go): ids that do
// not exist, belong to another channel, were uploaded by another user, or
// are already attached to a post are silently dropped; duplicates are
// removed; survivors get post_id stamped on their FileInfo.
func (s *Server) attachFilesLocked(postID, channelID, userID string, ids []string) []model.FileInfo {
	seen := make(map[string]bool, len(ids))
	var out []model.FileInfo
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		f := s.chat.files[id]
		if f == nil || f.channelID != channelID || f.info.UserID != userID || f.info.PostID != "" {
			continue
		}
		f.info.PostID = postID
		out = append(out, f.info)
	}
	return out
}

func (s *Server) createPostLocked(userID string, in model.Post) (model.Post, *apiErr) {
	c := s.chat.channels[in.ChannelID]
	if c == nil || !s.isMemberLocked(c.ID, userID) {
		return model.Post{}, &apiErr{403, "api.context.permissions.app_error"}
	}
	if in.PendingPostID != "" {
		if id, ok := s.chat.pending[in.PendingPostID]; ok {
			return s.chat.byID[id].Post, nil
		}
	}
	// Mirrors app/post.go:280-305 — three different outcomes, not one
	// (fix round 1: this used to collapse all three into 400 root_id):
	// missing/deleted root -> 400 root_id.app_error; root in a different
	// channel -> 500 channel_root_id.app_error; root is itself a reply ->
	// 400 root_id.app_error (reply-to-reply is rejected outright).
	var root *fpost
	if in.RootID != "" {
		root = s.chat.byID[in.RootID]
		if root == nil || root.DeleteAt != 0 {
			return model.Post{}, &apiErr{400, "api.post.create_post.root_id.app_error"}
		}
		if root.ChannelID != c.ID {
			return model.Post{}, &apiErr{500, "api.post.create_post.channel_root_id.app_error"}
		}
		if root.RootID != "" {
			return model.Post{}, &apiErr{400, "api.post.create_post.root_id.app_error"}
		}
	}
	now := s.nowLocked()
	p := &fpost{Post: model.Post{ID: newID(), ChannelID: c.ID, UserID: userID, RootID: in.RootID,
		Message: in.Message, Props: in.Props, PendingPostID: in.PendingPostID, CreateAt: now, UpdateAt: now}}
	if len(in.FileIDs) > 0 {
		if files := s.attachFilesLocked(p.ID, c.ID, userID, in.FileIDs); len(files) > 0 {
			for _, fi := range files {
				p.FileIDs = append(p.FileIDs, fi.ID)
			}
			p.Metadata = &model.PostMetadata{Files: files}
		}
	}
	isRoot := root == nil
	c.LastPostAt = now
	c.TotalMsgCount++
	if isRoot {
		c.TotalMsgCountRoot++
		c.LastRootPostAt = now
	} else {
		root.ReplyCount++
		root.LastReplyAt = now
		root.UpdateAt = now
		// post_store.go:276-292 (populateReplyCount): the reply itself
		// carries the thread's current reply_count, not just the root.
		p.ReplyCount = root.ReplyCount
	}
	p.mentions = s.mentionsLocked(c, userID, in.Message)
	for uid, m := range s.chat.members[c.ID] {
		if uid == userID {
			m.MsgCount, m.MsgCountRoot, m.LastViewedAt = c.TotalMsgCount, c.TotalMsgCountRoot, now
			continue
		}
		if slices.Contains(p.mentions, uid) {
			m.MentionCount++
			if isRoot {
				m.MentionCountRoot++
			}
		}
	}
	s.insertPostLocked(p)
	if in.PendingPostID != "" {
		s.chat.pending[in.PendingPostID] = p.ID
	}
	var followers []string
	var notifyThread func()
	if !isRoot {
		followers, notifyThread = s.subscribeReplyLocked(c, root, p, now)
	}
	// After the thread bookkeeping: a root trimmed by this very reply must
	// not get its thread record back.
	s.trimHistoryLocked(c.ID)
	b, _ := json.Marshal(p.Post)
	sender, _ := s.userByID(userID)
	s.publishLocked("posted", map[string]any{
		"post": string(b), "channel_type": c.Type, "channel_display_name": c.DisplayName,
		"channel_name": c.Name, "sender_name": "@" + sender.Username, "team_id": c.TeamID, "set_online": true,
	}, wsBroadcast{ChannelID: c.ID}, s.memberIDsLocked(c.ID), p.mentions, followers)
	if notifyThread != nil {
		notifyThread()
	}
	return p.Post, nil
}

func (s *Server) editPostLocked(userID, postID, msg string) (model.Post, *apiErr) {
	p := s.chat.byID[postID]
	if p == nil || p.DeleteAt != 0 || p.OriginalID != "" {
		return model.Post{}, &apiErr{404, "app.post.get.app_error"}
	}
	if p.UserID != userID {
		return model.Post{}, &apiErr{403, "api.context.permissions.app_error"}
	}
	now := s.nowLocked()
	hist := &fpost{Post: p.Post}
	hist.ID, hist.OriginalID, hist.DeleteAt, hist.UpdateAt = newID(), p.ID, now, now
	s.insertPostLocked(hist)
	p.Message, p.EditAt, p.UpdateAt = msg, now, now
	s.chat.channels[p.ChannelID].LastPostAt = now
	b, _ := json.Marshal(p.Post)
	s.publishLocked("post_edited", map[string]any{"post": string(b)}, wsBroadcast{ChannelID: p.ChannelID}, s.memberIDsLocked(p.ChannelID), nil, nil)
	return p.Post, nil
}

func (s *Server) deletePostLocked(userID, postID string, broadcast bool) *apiErr {
	p := s.chat.byID[postID]
	if p == nil || p.DeleteAt != 0 || p.OriginalID != "" {
		return &apiErr{404, "app.post.get.app_error"}
	}
	if userID != "" && p.UserID != userID {
		return &apiErr{403, "api.context.permissions.app_error"}
	}
	// post_deleted carries the post as read before the deletion
	// (app/post.go DeletePost: GetSingle, then Store.Delete, then
	// CleanUpAfterPostDeletion marshals that copy): delete_at 0, update_at
	// its own last update — never the deletion time.
	b, _ := json.Marshal(p.Post)
	now := s.nowLocked()
	if p.RootID == "" {
		// Deleting a root: one event for the root only, replies are marked
		// deleted silently (post_store.go:972-1007) — the client cascades.
		for _, q := range s.chat.posts[p.ChannelID] {
			if q.ID == p.ID || q.RootID == p.ID {
				q.DeleteAt, q.UpdateAt = now, now
			}
		}
	} else {
		p.DeleteAt, p.UpdateAt = now, now
		s.deleteReplyEffectsLocked(p, now)
	}
	if broadcast {
		s.publishLocked("post_deleted", map[string]any{"post": string(b)}, wsBroadcast{ChannelID: p.ChannelID}, s.memberIDsLocked(p.ChannelID), nil, nil)
	}
	return nil
}

func writeAPIErr(w http.ResponseWriter, e *apiErr) { appError(w, e.status, e.id, e.id) }

func (s *Server) createPost(w http.ResponseWriter, r *http.Request, u User) {
	var in model.Post
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		appError(w, 400, "api.post.bad_body", err.Error())
		return
	}
	s.mu.Lock()
	if s.chat.failPosts > 0 {
		s.chat.failPosts--
		s.mu.Unlock()
		appError(w, 500, "app.post.save.app_error", "injected failure")
		return
	}
	p, e := s.createPostLocked(u.ID, in)
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) patchPost(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	s.mu.Lock()
	p, e := s.editPostLocked(u.ID, r.PathValue("pid"), in.Message)
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) deletePost(w http.ResponseWriter, r *http.Request, u User) {
	s.mu.Lock()
	e := s.deletePostLocked(u.ID, r.PathValue("pid"), true)
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "OK"})
}

func (s *Server) viewChannel(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		ChannelID string `json:"channel_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.chat.members[in.ChannelID][u.ID]
	if m == nil {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	c := s.chat.channels[in.ChannelID]
	now := s.nowLocked()
	hadUnread := m.MsgCount < c.TotalMsgCount || m.MentionCount > 0
	m.MsgCount, m.MsgCountRoot, m.MentionCount, m.MentionCountRoot, m.UrgentMentionCount, m.LastViewedAt =
		c.TotalMsgCount, c.TotalMsgCountRoot, 0, 0, 0, now
	if hadUnread {
		s.publishLocked("multiple_channels_viewed", map[string]any{"channel_times": map[string]int64{c.ID: now}},
			wsBroadcast{UserID: u.ID}, []string{u.ID}, nil, nil)
	}
	writeJSON(w, 200, map[string]any{"status": "OK", "last_viewed_at_times": map[string]int64{c.ID: now}})
}

func (s *Server) setUnread(w http.ResponseWriter, r *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.chat.byID[r.PathValue("pid")]
	if p == nil {
		appError(w, 404, "app.post.get.app_error", "not found")
		return
	}
	m := s.chat.members[p.ChannelID][u.ID]
	if m == nil {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	var msgs, roots, ment, mentRoot int64
	for _, q := range s.chat.posts[p.ChannelID] {
		if q.OriginalID != "" {
			continue
		}
		if q.CreateAt < p.CreateAt {
			msgs++
			if q.RootID == "" {
				roots++
			}
		} else if slices.Contains(q.mentions, u.ID) {
			ment++
			if q.RootID == "" {
				mentRoot++
			}
		}
	}
	m.MsgCount, m.MsgCountRoot, m.MentionCount, m.MentionCountRoot, m.LastViewedAt = msgs, roots, ment, mentRoot, p.CreateAt-1
	c := s.chat.channels[p.ChannelID]
	out := model.ChannelUnreadAt{TeamID: c.TeamID, ChannelID: c.ID, MsgCount: msgs, MsgCountRoot: roots,
		MentionCount: ment, MentionCountRoot: mentRoot, LastViewedAt: m.LastViewedAt}
	s.publishLocked("post_unread", map[string]any{
		"msg_count": msgs, "msg_count_root": roots, "mention_count": ment, "mention_count_root": mentRoot,
		"urgent_mention_count": 0, "last_viewed_at": m.LastViewedAt, "post_id": p.ID,
	}, wsBroadcast{UserID: u.ID, ChannelID: c.ID, TeamID: c.TeamID}, []string{u.ID}, nil, nil)
	writeJSON(w, 200, out)
}

// publishLocked records the event and delivers it over WebSocket (ws.go).
// mentions/followers are per-recipient: a connection only sees itself in
// them (deliverLocked), never the whole list — mirrors the real server.
func (s *Server) publishLocked(name string, data map[string]any, b wsBroadcast, to []string, mentions, followers []string) {
	if s.opts.KeepPosts <= 0 { // a capped (soak) fake keeps no event log
		s.chat.events = append(s.chat.events, RecordedEvent{Name: name, To: to})
	}
	s.deliverLocked(name, data, b, to, mentions, followers)
}

// ---- test controls ----

func (s *Server) userIDByName(username string) string {
	for _, u := range s.opts.Users {
		if u.Username == username {
			return u.ID
		}
	}
	panic("mmfake: unknown user " + username)
}

func (s *Server) PostAs(channelID, username, message string) model.Post {
	return s.ReplyAs(channelID, "", username, message)
}

func (s *Server) ReplyAs(channelID, rootID, username, message string) model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.createPostLocked(s.userIDByName(username), model.Post{ChannelID: channelID, RootID: rootID, Message: message})
	if e != nil {
		panic("mmfake: PostAs: " + e.id)
	}
	return p
}

func (s *Server) EditAs(postID, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.chat.byID[postID]
	if p == nil {
		panic("mmfake: EditAs: no post " + postID)
	}
	if _, e := s.editPostLocked(p.UserID, postID, message); e != nil {
		panic("mmfake: EditAs: " + e.id)
	}
}

func (s *Server) DeleteAs(postID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.deletePostLocked("", postID, true); e != nil {
		panic("mmfake: DeleteAs: " + e.id)
	}
}

// DeleteAsQuiet deletes postID in the store like DeleteAs, but without a
// post_deleted broadcast: while the client's WS connection stays up, the
// deletion is invisible to it — no live update — until the client itself
// asks (CreatePost's root_id.app_error, or a thread reload's 404/GET).
// (A since= resync would still see the store's own UpdateAt change; this
// only suppresses the event, not the store mutation — don't combine it
// with DropConnections in the same test.) Test-only: exercises a client
// code path that must discover a deletion on its own, not one a WS event
// already told it about.
func (s *Server) DeleteAsQuiet(postID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.deletePostLocked("", postID, false); e != nil {
		panic("mmfake: DeleteAsQuiet: " + e.id)
	}
}

// FailPosts makes the next n POST /posts fail with 500 (send-failure tests).
func (s *Server) FailPosts(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chat.failPosts = n
}

func (s *Server) Member(channelID, username string) model.ChannelMember {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.chat.members[channelID][s.userIDByName(username)]; m != nil {
		return *m
	}
	return model.ChannelMember{}
}

func (s *Server) Channel(channelID string) model.Channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.chat.channels[channelID]; c != nil {
		return *c
	}
	return model.Channel{}
}

func (s *Server) VisiblePosts(channelID string) []model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Post
	for _, p := range s.chat.posts[channelID] {
		if visible(p, false) {
			out = append(out, p.Post)
		}
	}
	return out
}

func (s *Server) SetStatus(username, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.userIDByName(username)
	s.chat.status[id] = status
	s.publishLocked("status_change", map[string]any{"status": status, "user_id": id}, wsBroadcast{UserID: id}, []string{id}, nil, nil)
}

// AddChannel creates an open team channel with the given members and tells
// each of them (user_added), like a server-side "add to channel".
func (s *Server) AddChannel(id, display string, usernames ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	s.chat.channels[id] = &model.Channel{ID: id, TeamID: s.chat.teams[0].ID, Type: model.ChannelOpen,
		DisplayName: display, Name: strings.ToLower(strings.ReplaceAll(display, " ", "-")), CreateAt: now}
	s.chat.members[id] = map[string]*model.ChannelMember{}
	for _, name := range usernames {
		uid := s.userIDByName(name)
		s.chat.members[id][uid] = &model.ChannelMember{ChannelID: id, UserID: uid, LastViewedAt: now,
			NotifyProps: map[string]string{"desktop": "default", "mark_unread": "all"}}
		s.publishLocked("user_added", map[string]any{"user_id": uid, "team_id": s.chat.teams[0].ID},
			wsBroadcast{UserID: uid, ChannelID: id}, []string{uid}, nil, nil)
	}
}

func (s *Server) Events() []RecordedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RecordedEvent(nil), s.chat.events...)
}
