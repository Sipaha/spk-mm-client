package state

import (
	"slices"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

// Change tells the API layer which UI views to refresh.
type Change struct {
	Sidebar  bool
	Badge    bool
	Channels []string
}

func (c *Change) Merge(o Change) {
	c.Sidebar = c.Sidebar || o.Sidebar
	c.Badge = c.Badge || o.Badge
	for _, id := range o.Channels {
		if !slices.Contains(c.Channels, id) {
			c.Channels = append(c.Channels, id)
		}
	}
}

func (c Change) Empty() bool { return !c.Sidebar && !c.Badge && len(c.Channels) == 0 }

// NotifyCandidate carries everything notify.Decide needs, copied under lock.
type NotifyCandidate struct {
	Post        model.Post
	Channel     model.Channel
	ChannelName string
	Member      model.ChannelMember
	Me          model.User
	Status      model.Status
	CRT         bool
	Focused     bool
	Active      string
	Mentions    []string
	Followers   []string
	SenderName  string
}

// Effects is what the worker must do after an event, besides refreshing
// the views in Change.
type Effects struct {
	Change
	NeedMeta  bool              // channels/memberships/categories changed server-side
	NeedUsers []string          // profiles to fetch
	ShowDM    *model.Preference // DM/GM got a message while closed: save the "show" preference
	View      string            // open + focused channel got a post: mark it viewed
	Notify    *NotifyCandidate
	Resync    bool // CRT toggled: every window holds the wrong kind of posts
}

func (s *Server) ApplyEvent(ev ws.Event) Effects {
	s.mu.Lock()
	defer s.mu.Unlock()
	var eff Effects
	switch ev.Type {
	case "posted":
		s.onPostedLocked(ev, &eff)
	case "post_edited":
		if p, err := ws.DecodePost(ev); err == nil && s.updatePostLocked(p) {
			eff.Channels = []string{p.ChannelID}
		}
	case "post_deleted":
		if p, err := ws.DecodePost(ev); err == nil {
			if ch := s.chans[p.ChannelID]; ch != nil && s.removeLocked(ch, p.ID) {
				eff.Channels = []string{p.ChannelID}
			}
		}
	case "reaction_added", "reaction_removed":
		add := ev.Type == "reaction_added"
		if r, err := ws.DecodeReaction(ev); err == nil && !s.staleEchoLocked(r, add) && s.reactLocked(ev.Broadcast.ChannelID, r, add) {
			eff.Channels = []string{ev.Broadcast.ChannelID}
		}
	case "multiple_channels_viewed":
		times, _ := ws.DecodeChannelTimes(ev)
		for id, at := range times {
			if ch := s.chans[id]; ch != nil {
				s.markReadLocked(ch, at)
			}
		}
		eff.Sidebar, eff.Badge = true, true
	case "post_unread":
		if u, err := ws.DecodePostUnread(ev); err == nil {
			s.setUnreadLocked(u)
			eff.Sidebar, eff.Badge = true, true
		}
	case "channel_updated":
		if c, err := ws.DecodeChannel(ev); err == nil {
			if ch := s.chans[c.ID]; ch != nil {
				ch.Info = c
				s.dirty.chans[c.ID] = true
				eff.Sidebar, eff.Badge, eff.Channels = true, true, []string{c.ID}
			}
		}
	case "channel_deleted":
		id := ev.Str("channel_id")
		if ch := s.chans[id]; ch != nil {
			ch.Info.DeleteAt = s.now().UnixMilli()
			s.dirty.chans[id] = true
			eff.Sidebar, eff.Badge, eff.Channels = true, true, []string{id}
		}
	case "user_removed":
		// The copy addressed to the removed user carries channel_id in data.
		if ev.Broadcast.UserID == s.me.ID {
			if id := ev.Str("channel_id"); s.chans[id] != nil {
				delete(s.chans, id)
				s.forgetChannelLocked(id)
				eff.Sidebar, eff.Badge, eff.Channels = true, true, []string{id}
			}
		}
	case "user_added":
		eff.NeedMeta = ev.Str("user_id") == s.me.ID
	case "direct_added", "group_added", "channel_created", "channel_restored", "channel_converted",
		"sidebar_category_created", "sidebar_category_updated", "sidebar_category_deleted", "sidebar_category_order_updated":
		eff.NeedMeta = true
	case "channel_member_updated":
		if m, err := ws.DecodeMember(ev); err == nil && m.UserID == s.me.ID {
			if ch := s.chans[m.ChannelID]; ch != nil {
				ch.Member = m
				s.dirty.chans[m.ChannelID] = true
				eff.Sidebar, eff.Badge = true, true
			}
		}
	case "preferences_changed", "preferences_deleted":
		if prefs, err := ws.DecodePreferences(ev); err == nil {
			wasCRT := s.crtLocked()
			for _, p := range prefs {
				if ev.Type == "preferences_changed" {
					s.prefs[prefKey{p.Category, p.Name}] = p.Value
				} else {
					delete(s.prefs, prefKey{p.Category, p.Name})
				}
				// flagged_post's name is a post id: a Save/Unsave from another
				// device must repaint that post's channel, the same way a
				// reaction does -- every other preference only needs the
				// Sidebar/Badge refresh below.
				if p.Category == flaggedPostCategory {
					if ch, ok := s.channelOfPostLocked(p.Name); ok && !slices.Contains(eff.Channels, ch) {
						eff.Channels = append(eff.Channels, ch)
					}
				}
			}
			s.dirty.meta = true
			eff.Sidebar, eff.Badge = true, true
			eff.Resync = wasCRT != s.crtLocked()
		}
	case "user_updated":
		if u, err := ws.DecodeUser(ev); err == nil && u.ID != "" {
			if u.ID == s.me.ID {
				if u.NotifyProps == nil { // sanitized broadcast copy
					u.NotifyProps = s.me.NotifyProps
				}
				s.me = u
				s.dirty.meta = true
			}
			s.users[u.ID] = u
			s.dirty.users[u.ID] = true
			eff.Sidebar = true
		}
	case "emoji_added":
		if e, err := ws.DecodeEmoji(ev); err == nil {
			s.addEmojiLocked(e)
		}
	case "status_change":
		// Only our own arrives (the server sends it to that user alone);
		// the rest is polled — see StatusTargets.
		uid, st := ev.Str("user_id"), ev.Str("status")
		if uid == s.me.ID {
			s.status.Status = st
		}
		if uid != "" && s.presence[uid] != st {
			s.presence[uid] = st
			eff.Change = s.presenceChangeLocked()
		}
	}
	return eff
}

func (s *Server) onPostedLocked(ev ws.Event, eff *Effects) {
	d, err := ws.DecodePosted(ev)
	if err != nil {
		return
	}
	p := d.Post
	ch := s.chans[p.ChannelID]
	if ch == nil {
		// A DM/GM or channel we were just added to: keep the post for
		// TakeOrphans after the metadata refresh adds the channel.
		eff.NeedMeta = true
		if len(s.orphans) == maxOrphans {
			s.orphans = slices.Delete(s.orphans, 0, 1)
		}
		s.orphans = append(s.orphans, orphan{channelID: p.ChannelID, ev: ev})
		return
	}
	// Notification follows "new to us" (the seen-set), not the counter
	// bump: a post inside the post-bootstrap guard is already counted by
	// the REST read, yet the user has not been told about it.
	isNew, _ := s.applyNewPostLocked(ch, p, d.Mentions)
	eff.Sidebar, eff.Badge, eff.Channels = true, true, []string{ch.Info.ID}
	if _, ok := s.users[p.UserID]; !ok && p.UserID != "" {
		eff.NeedUsers = []string{p.UserID}
	}
	if p.UserID == s.me.ID {
		return
	}
	if isDirect(ch) {
		pref := model.Preference{UserID: s.me.ID, Category: "group_channel_show", Name: ch.Info.ID, Value: "true"}
		if ch.Info.IsDM() {
			pref.Category, pref.Name = "direct_channel_show", ch.Info.DMPartner(s.me.ID)
		}
		if s.prefs[prefKey{pref.Category, pref.Name}] != "true" {
			s.prefs[prefKey{pref.Category, pref.Name}] = "true"
			s.dirty.meta = true
			eff.ShowDM = &pref
		}
	}
	if !isNew {
		return
	}
	crt := s.crtLocked()
	if ch.Info.ID == s.active && s.focused && s.suppressView != ch.Info.ID && (!crt || p.RootID == "") {
		eff.View = ch.Info.ID
	}
	eff.Notify = &NotifyCandidate{
		Post: p, Channel: ch.Info, ChannelName: s.channelNameLocked(ch), Member: ch.Member, Me: s.me,
		Status: s.status, CRT: crt, Focused: s.focused, Active: s.active,
		Mentions: d.Mentions, Followers: d.Followers, SenderName: s.displayNameLocked(p.UserID),
	}
	if eff.Notify.SenderName == "" {
		eff.Notify.SenderName = trimAt(d.SenderName)
	}
}

type orphan struct {
	channelID string
	ev        ws.Event
}

func trimAt(s string) string {
	if len(s) > 0 && s[0] == '@' {
		return s[1:]
	}
	return s
}

// maxOrphans bounds the posted events kept for channels not known yet.
const maxOrphans = 100

// TakeOrphans hands out the posted events that arrived for channels we did
// not know yet and that a metadata refresh has since brought in; events for
// channels still unknown are dropped. The caller applies them right after
// the Bootstrap, while its guard keeps them out of the counters the REST
// read already includes: they insert and notify, they do not count twice.
func (s *Server) TakeOrphans() []ws.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ws.Event
	for _, o := range s.orphans {
		if s.chans[o.channelID] != nil {
			out = append(out, o.ev)
		}
	}
	s.orphans = nil
	return out
}
