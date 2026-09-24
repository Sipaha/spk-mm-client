package mmfake

import (
	"fmt"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

func defaultNotify() map[string]string {
	return map[string]string{"desktop": "default", "mark_unread": "all", "ignore_channel_mentions": "default"}
}

func (s *Server) seed() {
	o := s.opts
	s.chat = chatData{
		teams:      []model.Team{{ID: "t-fake", Name: "fake", DisplayName: "Fake Team"}},
		channels:   map[string]*model.Channel{},
		members:    map[string]map[string]*model.ChannelMember{},
		posts:      map[string][]*fpost{},
		byID:       map[string]*fpost{},
		pending:    map[string]string{},
		prefs:      map[string][]model.Preference{},
		status:     map[string]string{},
		sinceLimit: o.SinceLimit,
	}
	if s.chat.sinceLimit <= 0 {
		s.chat.sinceLimit = 1000
	}
	seedPosts := o.SeedPosts
	if seedPosts == 0 {
		seedPosts = 150
	}
	seedPosts = max(seedPosts, 0)
	base := time.Now().Add(-time.Duration(seedPosts+o.ExtraChannels*20+10) * time.Minute).UnixMilli()
	s.chat.lastMs = base
	add := func(id, typ, display, name string, users ...string) {
		s.chat.channels[id] = &model.Channel{ID: id, Type: typ, DisplayName: display, Name: name, CreateAt: base}
		if typ == model.ChannelOpen || typ == model.ChannelPrivate {
			s.chat.channels[id].TeamID = "t-fake"
		}
		s.chat.members[id] = map[string]*model.ChannelMember{}
		for _, u := range users {
			s.chat.members[id][u] = &model.ChannelMember{ChannelID: id, UserID: u, NotifyProps: defaultNotify()}
		}
	}
	add("c-town", model.ChannelOpen, "Town Square", "town-square", "u-alice", "u-bob", "u-carol")
	add("c-offtopic", model.ChannelOpen, "Off-Topic", "off-topic", "u-alice", "u-bob")
	add("c-secret", model.ChannelPrivate, "Secret", "secret", "u-alice")
	add("c-dm-bob", model.ChannelDirect, "", "u-alice__u-bob", "u-alice", "u-bob")
	add("c-gm", model.ChannelGroup, "alice, bob, carol", "gm-alice-bob-carol", "u-alice", "u-bob", "u-carol")
	for i := 1; i <= o.ExtraChannels; i++ {
		add(fmt.Sprintf("c-load-%03d", i), model.ChannelOpen, fmt.Sprintf("Load %03d", i), fmt.Sprintf("load-%03d", i), "u-alice", "u-bob")
	}
	authors := []string{"u-bob", "u-carol"}
	for i := 1; i <= seedPosts; i++ {
		s.seedPostLocked("c-town", authors[i%2], fmt.Sprintf("Message #%d", i))
	}
	s.seedPostLocked("c-offtopic", "u-bob", "Welcome to off-topic")
	s.seedPostLocked("c-dm-bob", "u-bob", "Hi Alice, this is Bob")
	for i := 1; i <= o.ExtraChannels; i++ {
		for j := 1; j <= 20; j++ {
			s.seedPostLocked(fmt.Sprintf("c-load-%03d", i), "u-bob", fmt.Sprintf("Load message %d with **some** markdown and a [link](https://example.com)", j))
		}
	}
	for _, m := range s.allMembersLocked() {
		c := s.chat.channels[m.ChannelID]
		m.MsgCount, m.MsgCountRoot, m.LastViewedAt = c.TotalMsgCount, c.TotalMsgCountRoot, s.chat.lastMs
	}
	s.chat.prefs["u-alice"] = []model.Preference{
		{UserID: "u-alice", Category: "direct_channel_show", Name: "u-bob", Value: "true"},
		{UserID: "u-alice", Category: "group_channel_show", Name: "c-gm", Value: "true"},
	}
}

// seedPostLocked appends a post one minute after the previous seed post,
// without mentions or events (the seed is history the client finds on login).
func (s *Server) seedPostLocked(channelID, userID, msg string) {
	s.chat.lastMs += int64(time.Minute / time.Millisecond)
	c := s.chat.channels[channelID]
	p := &fpost{Post: model.Post{ID: newID(), ChannelID: channelID, UserID: userID, Message: msg, CreateAt: s.chat.lastMs, UpdateAt: s.chat.lastMs}}
	c.LastPostAt, c.LastRootPostAt = s.chat.lastMs, s.chat.lastMs
	c.TotalMsgCount++
	c.TotalMsgCountRoot++
	s.insertPostLocked(p)
}

func (s *Server) allMembersLocked() []*model.ChannelMember {
	var out []*model.ChannelMember
	for _, byUser := range s.chat.members {
		for _, m := range byUser {
			out = append(out, m)
		}
	}
	return out
}
