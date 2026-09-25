// Package model holds the Mattermost wire types spk-mm-client uses, trimmed
// to the fields the client reads (see docs/research/2026-09-24-mattermost-api-facts.md).
// The same types serialize the local cache snapshot, so json tags matter.
package model

import (
	"sort"
	"strings"
)

// Channel type codes as used on the wire.
const (
	ChannelOpen    = "O"
	ChannelPrivate = "P"
	ChannelDirect  = "D"
	ChannelGroup   = "G"
)

type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	DeleteAt    int64  `json:"delete_at,omitempty"`
}

type Channel struct {
	ID                string `json:"id"`
	TeamID            string `json:"team_id"`
	Type              string `json:"type"`
	DisplayName       string `json:"display_name"`
	Name              string `json:"name"`
	Header            string `json:"header,omitempty"`
	Purpose           string `json:"purpose,omitempty"`
	CreateAt          int64  `json:"create_at"`
	UpdateAt          int64  `json:"update_at,omitempty"`
	DeleteAt          int64  `json:"delete_at,omitempty"`
	LastPostAt        int64  `json:"last_post_at"`
	LastRootPostAt    int64  `json:"last_root_post_at"`
	TotalMsgCount     int64  `json:"total_msg_count"`
	TotalMsgCountRoot int64  `json:"total_msg_count_root"`
}

func (c Channel) IsDM() bool    { return c.Type == ChannelDirect }
func (c Channel) IsGroup() bool { return c.Type == ChannelGroup }

// DMPartner returns the other user of a DM ("idA__idB"); a self-DM returns
// me; "" for non-DM channels.
func (c Channel) DMPartner(me string) string {
	if c.Type != ChannelDirect {
		return ""
	}
	a, b, ok := strings.Cut(c.Name, "__")
	if !ok {
		return ""
	}
	if a == me {
		return b
	}
	return a
}

type ChannelMember struct {
	ChannelID          string            `json:"channel_id"`
	UserID             string            `json:"user_id"`
	LastViewedAt       int64             `json:"last_viewed_at"`
	MsgCount           int64             `json:"msg_count"`
	MsgCountRoot       int64             `json:"msg_count_root"`
	MentionCount       int64             `json:"mention_count"`
	MentionCountRoot   int64             `json:"mention_count_root"`
	UrgentMentionCount int64             `json:"urgent_mention_count"`
	LastUpdateAt       int64             `json:"last_update_at,omitempty"`
	NotifyProps        map[string]string `json:"notify_props,omitempty"`
}

// Muted mirrors the webapp's isChannelMuted: mark_unread == "mention".
func (m ChannelMember) Muted() bool { return m.NotifyProps["mark_unread"] == "mention" }

type User struct {
	ID          string            `json:"id"`
	Username    string            `json:"username"`
	FirstName   string            `json:"first_name,omitempty"`
	LastName    string            `json:"last_name,omitempty"`
	Nickname    string            `json:"nickname,omitempty"`
	Locale      string            `json:"locale,omitempty"`
	IsBot       bool              `json:"is_bot,omitempty"`
	DeleteAt    int64             `json:"delete_at,omitempty"`
	UpdateAt    int64             `json:"update_at,omitempty"`
	NotifyProps map[string]string `json:"notify_props,omitempty"`
}

func (u User) FullName() string { return strings.TrimSpace(u.FirstName + " " + u.LastName) }

type Preference struct {
	UserID   string `json:"user_id"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Value    string `json:"value"`
}

type SidebarCategory struct {
	ID          string   `json:"id"`
	TeamID      string   `json:"team_id"`
	Type        string   `json:"type"` // favorites | channels | direct_messages | custom
	DisplayName string   `json:"display_name"`
	Sorting     string   `json:"sorting"` // "" | manual | recent | alpha
	SortOrder   int64    `json:"sort_order"`
	Muted       bool     `json:"muted"`
	Collapsed   bool     `json:"collapsed"`
	ChannelIDs  []string `json:"channel_ids"`
}

type OrderedCategories struct {
	Categories []SidebarCategory `json:"categories"`
	Order      []string          `json:"order"`
}

type FileInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Extension       string `json:"extension,omitempty"`
	Size            int64  `json:"size"`
	MimeType        string `json:"mime_type,omitempty"`
	HasPreviewImage bool   `json:"has_preview_image,omitempty"`
}

type Reaction struct {
	UserID    string `json:"user_id"`
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
	CreateAt  int64  `json:"create_at,omitempty"`
}

// PostMetadata keeps only files and reactions; embeds/images/emojis are
// dropped at decode time (memory: the hot layer holds ~60 posts × channels).
type PostMetadata struct {
	Files     []FileInfo `json:"files,omitempty"`
	Reactions []Reaction `json:"reactions,omitempty"`
}

type Post struct {
	ID            string        `json:"id"`
	ChannelID     string        `json:"channel_id"`
	UserID        string        `json:"user_id"`
	RootID        string        `json:"root_id,omitempty"`
	OriginalID    string        `json:"original_id,omitempty"`
	Message       string        `json:"message"`
	Type          string        `json:"type,omitempty"`
	PendingPostID string        `json:"pending_post_id,omitempty"`
	CreateAt      int64         `json:"create_at"`
	UpdateAt      int64         `json:"update_at"`
	EditAt        int64         `json:"edit_at,omitempty"`
	DeleteAt      int64         `json:"delete_at,omitempty"`
	IsPinned      bool          `json:"is_pinned,omitempty"`
	ReplyCount    int64         `json:"reply_count,omitempty"`
	LastReplyAt   int64         `json:"last_reply_at,omitempty"`
	FileIDs       []string      `json:"file_ids,omitempty"`
	Props         PostProps     `json:"props"`
	Metadata      *PostMetadata `json:"metadata,omitempty"`
}

// IsSystem reports whether the post is a system message (join/leave/header-change
// etc.), posted with type system_*.
func (p Post) IsSystem() bool { return strings.HasPrefix(p.Type, "system_") }

type PostList struct {
	Order      []string        `json:"order"`
	Posts      map[string]Post `json:"posts"`
	NextPostID string          `json:"next_post_id"`
	PrevPostID string          `json:"prev_post_id"`
}

// Ascending returns the posts listed in Order, oldest first. Posts only in
// the map (roots fetched alongside replies in since-responses) are skipped.
func (l PostList) Ascending() []Post {
	out := make([]Post, 0, len(l.Order))
	for _, id := range l.Order {
		if p, ok := l.Posts[id]; ok {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreateAt < out[j].CreateAt })
	return out
}

type ChannelUnreadAt struct {
	TeamID             string `json:"team_id"`
	ChannelID          string `json:"channel_id"`
	MsgCount           int64  `json:"msg_count"`
	MsgCountRoot       int64  `json:"msg_count_root"`
	MentionCount       int64  `json:"mention_count"`
	MentionCountRoot   int64  `json:"mention_count_root"`
	UrgentMentionCount int64  `json:"urgent_mention_count"`
	LastViewedAt       int64  `json:"last_viewed_at"`
}

// Status is the user's presence; DNDEndTime is in SECONDS (unlike every
// other Mattermost timestamp).
type Status struct {
	UserID     string `json:"user_id"`
	Status     string `json:"status"` // online | away | dnd | offline | ooo
	DNDEndTime int64  `json:"dnd_end_time,omitempty"`
}
