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
	teams      []model.Team
	channels   map[string]*model.Channel
	members    map[string]map[string]*model.ChannelMember // channel → user → member
	posts      map[string][]*fpost                        // channel → by CreateAt; includes deleted + history rows
	byID       map[string]*fpost
	pending    map[string]string // pending_post_id → post id
	prefs      map[string][]model.Preference
	status     map[string]string
	events     []RecordedEvent
	lastMs     int64
	sinceLimit int
	failPosts  int

	files      map[string]*ffile
	emoji      map[string]*femoji // by id
	pictures   map[string]*picture
	pictureSeq int
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
	s.publishLocked("preferences_changed", map[string]any{"preferences": string(b)}, wsBroadcast{UserID: u.ID}, []string{u.ID}, nil)
	writeJSON(w, 200, map[string]string{"status": "OK"})
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

func (s *Server) usersByIDs(w http.ResponseWriter, r *http.Request, _ User) {
	var ids []string
	_ = json.NewDecoder(r.Body).Decode(&ids)
	out := []model.User{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if u, ok := s.userByID(id); ok {
			mu := userJSON(u)
			if pic := s.chat.pictures[id]; pic != nil {
				mu.LastPictureUpdate = pic.at
			}
			out = append(out, mu)
		}
	}
	writeJSON(w, 200, out)
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

type apiErr struct {
	status int
	id     string
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
	now := s.nowLocked()
	p := &fpost{Post: model.Post{ID: newID(), ChannelID: c.ID, UserID: userID, RootID: in.RootID,
		Message: in.Message, PendingPostID: in.PendingPostID, CreateAt: now, UpdateAt: now}}
	if len(in.FileIDs) > 0 {
		p.FileIDs = append([]string(nil), in.FileIDs...)
		p.Metadata = &model.PostMetadata{Files: s.fileInfosLocked(in.FileIDs)}
	}
	root := in.RootID == ""
	c.LastPostAt = now
	c.TotalMsgCount++
	if root {
		c.TotalMsgCountRoot++
		c.LastRootPostAt = now
	} else if r := s.chat.byID[in.RootID]; r != nil {
		r.ReplyCount++
		r.LastReplyAt = now
		r.UpdateAt = now
	}
	p.mentions = s.mentionsLocked(c, userID, in.Message)
	for uid, m := range s.chat.members[c.ID] {
		if uid == userID {
			m.MsgCount, m.MsgCountRoot, m.LastViewedAt = c.TotalMsgCount, c.TotalMsgCountRoot, now
			continue
		}
		if slices.Contains(p.mentions, uid) {
			m.MentionCount++
			if root {
				m.MentionCountRoot++
			}
		}
	}
	s.insertPostLocked(p)
	if in.PendingPostID != "" {
		s.chat.pending[in.PendingPostID] = p.ID
	}
	b, _ := json.Marshal(p.Post)
	sender, _ := s.userByID(userID)
	s.publishLocked("posted", map[string]any{
		"post": string(b), "channel_type": c.Type, "channel_display_name": c.DisplayName,
		"channel_name": c.Name, "sender_name": "@" + sender.Username, "team_id": c.TeamID, "set_online": true,
	}, wsBroadcast{ChannelID: c.ID}, s.memberIDsLocked(c.ID), p.mentions)
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
	s.publishLocked("post_edited", map[string]any{"post": string(b)}, wsBroadcast{ChannelID: p.ChannelID}, s.memberIDsLocked(p.ChannelID), nil)
	return p.Post, nil
}

func (s *Server) deletePostLocked(userID, postID string) *apiErr {
	p := s.chat.byID[postID]
	if p == nil || p.DeleteAt != 0 || p.OriginalID != "" {
		return &apiErr{404, "app.post.get.app_error"}
	}
	if userID != "" && p.UserID != userID {
		return &apiErr{403, "api.context.permissions.app_error"}
	}
	now := s.nowLocked()
	for _, q := range s.chat.posts[p.ChannelID] {
		if q.ID == p.ID || q.RootID == p.ID {
			q.DeleteAt, q.UpdateAt = now, now
		}
	}
	b, _ := json.Marshal(p.Post)
	s.publishLocked("post_deleted", map[string]any{"post": string(b)}, wsBroadcast{ChannelID: p.ChannelID}, s.memberIDsLocked(p.ChannelID), nil)
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
	e := s.deletePostLocked(u.ID, r.PathValue("pid"))
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
			wsBroadcast{UserID: u.ID}, []string{u.ID}, nil)
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
	}, wsBroadcast{UserID: u.ID, ChannelID: c.ID, TeamID: c.TeamID}, []string{u.ID}, nil)
	writeJSON(w, 200, out)
}

// publishLocked records the event and delivers it over WebSocket (ws.go).
func (s *Server) publishLocked(name string, data map[string]any, b wsBroadcast, to []string, mentions []string) {
	s.chat.events = append(s.chat.events, RecordedEvent{Name: name, To: to})
	s.deliverLocked(name, data, b, to, mentions)
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
	if e := s.deletePostLocked("", postID); e != nil {
		panic("mmfake: DeleteAs: " + e.id)
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
	s.publishLocked("status_change", map[string]any{"status": status, "user_id": id}, wsBroadcast{UserID: id}, []string{id}, nil)
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
			wsBroadcast{UserID: uid, ChannelID: id}, []string{uid}, nil)
	}
}

func (s *Server) Events() []RecordedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RecordedEvent(nil), s.chat.events...)
}
