package state

import (
	"sort"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

type FileView struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
}

type ReactionView struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

type PostView struct {
	ID          string             `json:"id"`
	UserID      string             `json:"user_id"`
	Author      string             `json:"author"`
	RootID      string             `json:"root_id,omitempty"`
	Message     string             `json:"message"`
	CreateAt    int64              `json:"create_at"`
	EditAt      int64              `json:"edit_at,omitempty"`
	ReplyCount  int64              `json:"reply_count,omitempty"`
	System      bool               `json:"system,omitempty"`
	Bot         bool               `json:"bot,omitempty"`
	Pending     bool               `json:"pending,omitempty"`
	Failed      bool               `json:"failed,omitempty"`
	Attachments []model.Attachment `json:"attachments,omitempty"`
	Files       []FileView         `json:"files,omitempty"`
	Reactions   []ReactionView     `json:"reactions,omitempty"`
}

type ChannelView struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Header   string     `json:"header"`
	Purpose  string     `json:"purpose"`
	TeamName string     `json:"team_name"`
	Posts    []PostView `json:"posts"`
	NewSince int64      `json:"new_since"`
	HasMore  bool       `json:"has_more"`
	Loaded   bool       `json:"loaded"`
	Syncing  bool       `json:"syncing"`
	GapAfter string     `json:"gap_after"`
	Draft    string     `json:"draft"`
	MeID     string     `json:"me_id"`
	CRT      bool       `json:"crt"`
	Muted    bool       `json:"muted"`
}

// ChannelView renders a channel for the UI: browsed history, the window
// and our pending posts, oldest first.
func (s *Server) ChannelView(channelID string) (ChannelView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.chans[channelID]
	if ch == nil {
		return ChannelView{}, false
	}
	v := ChannelView{
		ID: ch.Info.ID, Name: s.channelNameLocked(ch), Type: ch.Info.Type, Header: ch.Info.Header, Purpose: ch.Info.Purpose,
		TeamName: s.teamNameLocked(ch), Loaded: ch.Win.Loaded, Syncing: !ch.Win.Loaded || ch.Win.Stale,
		GapAfter: ch.Win.GapAfter, Draft: s.drafts[channelID], MeID: s.me.ID, CRT: s.crtLocked(), Muted: ch.Member.Muted(),
		Posts: []PostView{},
	}
	var posts []model.Post
	if channelID == s.active {
		v.NewSince = s.newSince
		posts = append(posts, s.older...)
	}
	posts = append(posts, ch.Win.Posts...)
	if channelID == s.active && (s.olderComplete || len(s.older) > 0) {
		v.HasMore = !s.olderComplete
	} else {
		v.HasMore = ch.Win.Loaded && !ch.Win.Complete
	}
	for _, p := range posts {
		v.Posts = append(v.Posts, s.postViewLocked(p))
	}
	pend := append([]Pending(nil), s.pending[channelID]...)
	sort.SliceStable(pend, func(i, j int) bool { return pend[i].CreateAt < pend[j].CreateAt })
	for _, p := range pend {
		v.Posts = append(v.Posts, PostView{ID: p.ID, UserID: s.me.ID, Author: s.displayNameLocked(s.me.ID),
			RootID: p.RootID, Message: p.Message, CreateAt: p.CreateAt, Pending: !p.Failed, Failed: p.Failed})
	}
	return v, true
}

func (s *Server) teamNameLocked(ch *Chan) string {
	id := ch.Info.TeamID
	if id == "" {
		id = s.nav.TeamID
	}
	for _, t := range s.teams {
		if t.ID == id {
			return t.Name
		}
	}
	return ""
}

func (s *Server) postViewLocked(p model.Post) PostView {
	v := PostView{ID: p.ID, UserID: p.UserID, RootID: p.RootID, Message: p.Message, CreateAt: p.CreateAt,
		EditAt: p.EditAt, ReplyCount: p.ReplyCount, System: p.IsSystem(), Attachments: p.Props.Attachments,
		Bot: bool(p.Props.FromBot) || bool(p.Props.FromWebhook)}
	v.Author = s.displayNameLocked(p.UserID)
	if bool(p.Props.FromWebhook) && p.Props.OverrideUsername != "" {
		v.Author = string(p.Props.OverrideUsername)
	}
	if u, ok := s.users[p.UserID]; ok && u.IsBot {
		v.Bot = true
	}
	if p.Metadata != nil {
		for _, f := range p.Metadata.Files {
			v.Files = append(v.Files, FileView{Name: f.Name, Size: f.Size, Mime: f.MimeType})
		}
		idx := map[string]int{}
		for _, r := range p.Metadata.Reactions {
			i, ok := idx[r.EmojiName]
			if !ok {
				i = len(v.Reactions)
				idx[r.EmojiName] = i
				v.Reactions = append(v.Reactions, ReactionView{Emoji: r.EmojiName})
			}
			v.Reactions[i].Count++
			v.Reactions[i].Mine = v.Reactions[i].Mine || r.UserID == s.me.ID
		}
	}
	return v
}
