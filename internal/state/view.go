package state

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// FileView is what the feed needs to show a file: previews come from
// /media/ by id; width/height fix the image box before it loads. Staged: a
// file of a post being sent — ID is the attachment id and its picture
// comes from /media/<srv>/staged/<id>. State/Sent/Error are the live
// upload progress of a staged file (mirroring attach.Attachment: staged |
// uploading | uploaded | failed, bytes sent so far, the error code of a
// failed upload) — set only while Staged, refreshed in place by
// RefreshPendingProgress as the upload goes.
type FileView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Ext        string `json:"ext,omitempty"`
	Size       int64  `json:"size"`
	Mime       string `json:"mime"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	HasPreview bool   `json:"has_preview,omitempty"`
	Staged     bool   `json:"staged,omitempty"`
	State      string `json:"state,omitempty"`
	Sent       int64  `json:"sent,omitempty"`
	Error      string `json:"error,omitempty"`
}

type ReactionView struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

type PostView struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Author string `json:"author"`
	// RealAuthor: the account's name when Author is a webhook's
	// override_username (for a tooltip); "" otherwise.
	RealAuthor string `json:"real_author,omitempty"`
	// Icon: the author's picture is overridden by a webhook — "post" (the
	// picture is /media/<srv>/posticon/<post id>; the generic webhook icon
	// while it loads or when it fails), ":name:" (an emoji) or "webhook"
	// (the generic webhook icon); "" = the account's avatar.
	Icon string `json:"icon,omitempty"`
	// IconVersion: with Icon "post", what its picture is (a hash of the
	// icon URL) — the UI's ?v= on /media/…/posticon/<id>, so an edit that
	// changes the icon is not served from the old picture's cache.
	IconVersion string `json:"icon_version,omitempty"`
	// Webhook: from_webhook — never grouped with its neighbours (webapp
	// areConsecutivePostsBySameUser).
	Webhook       bool               `json:"webhook,omitempty"`
	Avatar        string             `json:"avatar,omitempty"` // picture version; "" = profile not loaded
	Status        string             `json:"status,omitempty"` // presence of the author; "" for bots
	RootID        string             `json:"root_id,omitempty"`
	Message       string             `json:"message"`
	CreateAt      int64              `json:"create_at"`
	EditAt        int64              `json:"edit_at,omitempty"`
	ReplyCount    int64              `json:"reply_count,omitempty"`
	LastReplyAt   int64              `json:"last_reply_at,omitempty"`
	System        bool               `json:"system,omitempty"`
	Bot           bool               `json:"bot,omitempty"`
	Pending       bool               `json:"pending,omitempty"`
	Failed        bool               `json:"failed,omitempty"`
	PendingPostID string             `json:"pending_post_id,omitempty"`
	Attachments   []model.Attachment `json:"attachments,omitempty"`
	Files         []FileView         `json:"files,omitempty"`
	Reactions     []ReactionView     `json:"reactions,omitempty"`
	// Saved: a flagged_post preference for this post — see SetPostSaved.
	Saved bool `json:"saved,omitempty"`
	// RootAuthor/RootSnippet: a reply's context line in the channel feed
	// without CRT ("reply to <author>: <snippet>") — the root's author and
	// its text, collapsed and cut to rootSnippetRunes. Empty when the root
	// is not held (the UI then says "reply in a thread").
	RootAuthor  string `json:"root_author,omitempty"`
	RootSnippet string `json:"root_snippet,omitempty"`
}

type ChannelView struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Header   string     `json:"header"`
	Purpose  string     `json:"purpose"`
	TeamName string     `json:"team_name"`
	TeamID   string     `json:"team_id"`
	Posts    []PostView `json:"posts"`
	NewSince int64      `json:"new_since"`
	HasMore  bool       `json:"has_more"`
	Loaded   bool       `json:"loaded"`
	Syncing  bool       `json:"syncing"`
	GapAfter string     `json:"gap_after"`
	Draft    string     `json:"draft"`
	MeID     string     `json:"me_id"`
	// MeAvatar: my own picture version (avatarLocked(MeID) — "" if not
	// loaded yet), the same field PostView uses for a post's author,
	// exposed here too so the UI can show my real avatar somewhere I'm not
	// a post's author (e.g. the "You" row of the reaction-chip "who
	// reacted" modal — Reactions/ReactorsModal).
	MeAvatar string `json:"me_avatar"`
	CRT      bool   `json:"crt"`
	Muted    bool   `json:"muted"`
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
		TeamName: s.teamNameLocked(ch), TeamID: ch.Info.TeamID, Loaded: ch.Win.Loaded, Syncing: !ch.Win.Loaded || ch.Win.Stale,
		GapAfter: ch.Win.GapAfter, Draft: s.drafts[channelID], MeID: s.me.ID, MeAvatar: s.avatarLocked(s.me.ID), CRT: s.crtLocked(), Muted: ch.Member.Muted(),
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
	// Without CRT replies sit in the feed and carry their root's context,
	// looked up in what is shown (history + window).
	var roots map[string]int
	rootContext := func(pv *PostView) {
		if v.CRT || pv.RootID == "" {
			return
		}
		if roots == nil {
			roots = make(map[string]int, len(posts))
			for i, p := range posts {
				roots[p.ID] = i
			}
		}
		if i, ok := roots[pv.RootID]; ok {
			pv.RootAuthor, pv.RootSnippet = s.authorLocked(posts[i]), rootSnippet(posts[i])
		} else if t := s.threads[pv.RootID]; t != nil && t.root.ID == pv.RootID {
			pv.RootAuthor, pv.RootSnippet = s.authorLocked(t.root), rootSnippet(t.root)
		}
	}
	for _, p := range posts {
		pv := s.postViewLocked(p)
		rootContext(&pv)
		v.Posts = append(v.Posts, pv)
	}
	pend := append([]Pending(nil), s.pending[channelID]...)
	sort.SliceStable(pend, func(i, j int) bool { return pend[i].CreateAt < pend[j].CreateAt })
	for _, p := range pend {
		if v.CRT && p.RootID != "" {
			continue // a reply being sent shows in its thread only
		}
		pv := s.pendingViewLocked(p)
		rootContext(&pv)
		v.Posts = append(v.Posts, pv)
	}
	return v, true
}

// pendingViewLocked shows a post being sent (the feed and the thread panel).
func (s *Server) pendingViewLocked(p Pending) PostView {
	return PostView{ID: p.ID, UserID: s.me.ID, Author: s.displayNameLocked(s.me.ID),
		Avatar: s.avatarLocked(s.me.ID), Status: s.presenceLocked(s.me.ID),
		RootID: p.RootID, Message: p.Message, CreateAt: p.CreateAt, Pending: !p.Failed, Failed: p.Failed,
		// Keys the feed row across confirmation: the eventual real post
		// echoes this same id back as its own PendingPostID.
		PendingPostID: p.ID, Files: p.Files}
}

// rootSnippetRunes bounds PostView.RootSnippet.
const rootSnippetRunes = 80

// rootSnippet is a root's text for a reply's context line: whitespace
// collapsed, cut to rootSnippetRunes (with "…"); no text — the first
// file's name.
func rootSnippet(p model.Post) string {
	text := strings.Join(strings.Fields(p.Message), " ")
	if text == "" {
		if p.Metadata != nil && len(p.Metadata.Files) > 0 {
			return p.Metadata.Files[0].Name
		}
		return ""
	}
	if r := []rune(text); len(r) > rootSnippetRunes {
		return string(r[:rootSnippetRunes-1]) + "…"
	}
	return text
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

// authorLocked is the name shown for p's author: a webhook's
// override_username when the server allows it (webapp post/user_profile.tsx).
func (s *Server) authorLocked(p model.Post) string {
	if name, ok := s.overrideNameLocked(p); ok {
		return name
	}
	return s.displayNameLocked(p.UserID)
}

func (s *Server) overrideNameLocked(p model.Post) (string, bool) {
	if bool(p.Props.FromWebhook) && p.Props.OverrideUsername != "" && s.cfg.PostUsernameOverride {
		return string(p.Props.OverrideUsername), true
	}
	return "", false
}

// iconOverrideLocked is PostView.Icon: webapp post_profile_picture — a
// webhook post (not a system one), not asking for the account's picture
// (use_user_icon), on a server with EnablePostIconOverride. An emoji icon
// wins: the server also rewrites override_icon_url to that emoji's
// picture, which the UI draws itself. Without either: "webhook", the UI's
// generic webhook icon (the webapp's DEFAULT_WEBHOOK_LOGO) — never the
// account's picture, which would read as if its owner had written the post.
func (s *Server) iconOverrideLocked(p model.Post) string {
	if !bool(p.Props.FromWebhook) || p.IsSystem() || bool(p.Props.UseUserIcon) || !s.cfg.PostIconOverride {
		return ""
	}
	if name := strings.Trim(p.Props.OverrideIconEmoji, ":"); emojiNameRe.MatchString(name) {
		return ":" + name + ":"
	}
	if p.Props.OverrideIconURL != "" {
		return "post"
	}
	return "webhook"
}

var emojiNameRe = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,64}$`)

// PostIconURL is the override_icon_url of a held post whose picture the
// UI shows from /media/<srv>/posticon/<id> (PostView.Icon == "post");
// false for an unknown post or one without such a picture.
func (s *Server) PostIconURL(postID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.findPostLocked(postID)
	if !ok || s.iconOverrideLocked(p) != "post" {
		return "", false
	}
	return p.Props.OverrideIconURL, true
}

func (s *Server) postViewLocked(p model.Post) PostView {
	v := PostView{ID: p.ID, UserID: p.UserID, RootID: p.RootID, Message: p.Message, CreateAt: p.CreateAt,
		EditAt: p.EditAt, ReplyCount: p.ReplyCount, LastReplyAt: p.LastReplyAt, System: p.IsSystem(), Attachments: p.Props.Attachments,
		Bot: bool(p.Props.FromBot) || bool(p.Props.FromWebhook), Webhook: bool(p.Props.FromWebhook), PendingPostID: p.PendingPostID}
	v.Saved = s.prefs[prefKey{flaggedPostCategory, p.ID}] == "true"
	v.Author = s.authorLocked(p)
	if _, ok := s.overrideNameLocked(p); ok {
		v.RealAuthor = s.displayNameLocked(p.UserID)
	}
	v.Icon = s.iconOverrideLocked(p)
	if v.Icon == "post" {
		sum := sha256.Sum256([]byte(p.Props.OverrideIconURL))
		v.IconVersion = hex.EncodeToString(sum[:8])
	}
	if u, ok := s.users[p.UserID]; ok && u.IsBot {
		v.Bot = true
	}
	v.Avatar = s.avatarLocked(p.UserID)
	if !v.Bot {
		v.Status = s.presenceLocked(p.UserID)
	}
	if p.Metadata != nil {
		for _, f := range p.Metadata.Files {
			v.Files = append(v.Files, FileView{ID: f.ID, Name: f.Name, Ext: f.Extension, Size: f.Size, Mime: f.MimeType,
				Width: f.Width, Height: f.Height, HasPreview: f.HasPreviewImage})
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
