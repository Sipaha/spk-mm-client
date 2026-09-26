package mmfake

import (
	"fmt"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
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
		files:      map[string]*ffile{},
		emoji:      map[string]*femoji{},
		pictures:   map[string]*picture{},
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
	welcome := s.seedPostLocked("c-offtopic", "u-bob", "Welcome to off-topic")
	react := func(user, emoji string) model.Reaction {
		return model.Reaction{UserID: user, PostID: welcome.ID, EmojiName: emoji, CreateAt: welcome.CreateAt}
	}
	welcome.Metadata = &model.PostMetadata{Reactions: []model.Reaction{
		react("u-bob", "+1"), react("u-carol", "+1"), react("u-carol", "tada"), react("u-bob", "partyparrot"),
	}}
	img := seedImages()
	s.seedFilePostLocked("c-offtopic", "u-bob", "Build screenshot",
		s.newFileLocked("f-build", "c-offtopic", "build.png", "image/png", img["build.png"]))
	s.seedFilePostLocked("c-offtopic", "u-carol", "Two diagrams",
		s.newFileLocked("f-diag1", "c-offtopic", "flow.png", "image/png", img["flow.png"]),
		s.newFileLocked("f-diag2", "c-offtopic", "arch.png", "image/png", img["arch.png"]))
	s.seedFilePostLocked("c-offtopic", "u-bob", "Server log attached",
		s.newFileLocked("f-log", "c-offtopic", "server.log", "text/plain", logText(60)))
	s.seedFilePostLocked("c-offtopic", "u-carol", "Spec draft",
		s.newFileLocked("f-spec", "c-offtopic", "spec.pdf", "application/pdf", []byte("%PDF-1.4\n% fake pdf for spk-mattermost tests\n")))
	s.seedFilePostLocked("c-offtopic", "u-bob", "Full log, for the viewer's ?full=1",
		s.newFileLocked("f-biglog", "c-offtopic", "big.log", "text/plain", bigLogText(22000))) // ~1.2 MiB: over TextFullLimit (1 MiB)
	s.chat.emoji["e-parrot"] = &femoji{e: model.Emoji{ID: "e-parrot", Name: "partyparrot", CreatorID: "u-bob"}, png: img["emoji"]}
	for i, u := range o.Users {
		at := base + int64(i)
		if u.Username == "carol" {
			at = -at // a generated default picture: the server stores -now
		}
		s.chat.pictures[u.ID] = &picture{at: at, png: img[fmt.Sprintf("avatar%d", i%len(palette))]}
	}
	s.chat.status["u-alice"], s.chat.status["u-bob"], s.chat.status["u-carol"] = "online", "online", "away"
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
func (s *Server) seedPostLocked(channelID, userID, msg string) *fpost {
	s.chat.lastMs += int64(time.Minute / time.Millisecond)
	c := s.chat.channels[channelID]
	p := &fpost{Post: model.Post{ID: newID(), ChannelID: channelID, UserID: userID, Message: msg, CreateAt: s.chat.lastMs, UpdateAt: s.chat.lastMs}}
	c.LastPostAt, c.LastRootPostAt = s.chat.lastMs, s.chat.lastMs
	c.TotalMsgCount++
	c.TotalMsgCountRoot++
	s.insertPostLocked(p)
	return p
}

// seedFilePostLocked is seedPostLocked with attached files.
func (s *Server) seedFilePostLocked(channelID, userID, msg string, files ...*ffile) *fpost {
	p := s.seedPostLocked(channelID, userID, msg)
	for _, f := range files {
		p.FileIDs = append(p.FileIDs, f.info.ID)
	}
	p.Metadata = &model.PostMetadata{Files: s.fileInfosLocked(p.FileIDs)}
	return p
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
