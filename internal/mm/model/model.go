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
	ID        string `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	Locale    string `json:"locale,omitempty"`
	IsBot     bool   `json:"is_bot,omitempty"`
	DeleteAt  int64  `json:"delete_at,omitempty"`
	UpdateAt  int64  `json:"update_at,omitempty"`
	// LastPictureUpdate versions the profile picture: it changes on every
	// upload and reset, and is negative for a generated default picture.
	LastPictureUpdate int64             `json:"last_picture_update,omitempty"`
	NotifyProps       map[string]string `json:"notify_props,omitempty"`
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
	UserID          string `json:"user_id,omitempty"`
	PostID          string `json:"post_id,omitempty"`
	Name            string `json:"name"`
	Extension       string `json:"extension,omitempty"`
	Size            int64  `json:"size"`
	MimeType        string `json:"mime_type,omitempty"`
	Width           int    `json:"width,omitempty"`
	Height          int    `json:"height,omitempty"`
	HasPreviewImage bool   `json:"has_preview_image,omitempty"`
}

type Reaction struct {
	UserID    string `json:"user_id"`
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
	CreateAt  int64  `json:"create_at,omitempty"`
	// UpdateAt/DeleteAt: set on a removal (reaction_removed carries the
	// deleted row, reaction_store.go Delete → PreUpdate).
	UpdateAt int64 `json:"update_at,omitempty"`
	DeleteAt int64 `json:"delete_at,omitempty"`
}

// Emoji is a server's custom emoji.
type Emoji struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatorID string `json:"creator_id,omitempty"`
	DeleteAt  int64  `json:"delete_at,omitempty"`
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
	// HasNext is set by GET .../thread (perPage paging, direction up/down):
	// true when older/newer replies exist beyond this page.
	HasNext *bool `json:"has_next,omitempty"`
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

// TeamUnread is one element of GET /users/me/teams/unread. The Thread*
// fields are only populated when include_collapsed_threads=true and cover
// subscribed threads of that team only — DM/GM threads (team_id "") are
// never counted here (see ThreadTotals for those).
type TeamUnread struct {
	TeamID                   string `json:"team_id"`
	MsgCount                 int64  `json:"msg_count"`
	MentionCount             int64  `json:"mention_count"`
	MentionCountRoot         int64  `json:"mention_count_root"`
	MsgCountRoot             int64  `json:"msg_count_root"`
	ThreadCount              int64  `json:"thread_count"`
	ThreadMentionCount       int64  `json:"thread_mention_count"`
	ThreadUrgentMentionCount int64  `json:"thread_urgent_mention_count"`
}

// ThreadResponse is a thread as seen by one user: GET/PUT .../threads/{id}
// (read), an element of Threads.Threads, and the JSON string carried by the
// thread_updated event's data.thread.
type ThreadResponse struct {
	PostID         string `json:"id"`
	ReplyCount     int64  `json:"reply_count"`
	LastReplyAt    int64  `json:"last_reply_at"`
	LastViewedAt   int64  `json:"last_viewed_at"`
	Participants   []User `json:"participants"`
	Post           *Post  `json:"post"`
	UnreadReplies  int64  `json:"unread_replies"`
	UnreadMentions int64  `json:"unread_mentions"`
	IsUrgent       bool   `json:"is_urgent,omitempty"`
	DeleteAt       int64  `json:"delete_at,omitempty"`
}

// ThreadTotals is GET .../threads?totalsOnly=true (Threads is empty in that
// mode; populated when the caller asks for the full list instead).
type ThreadTotals struct {
	Total                     int64            `json:"total"`
	TotalUnreadThreads        int64            `json:"total_unread_threads"`
	TotalUnreadMentions       int64            `json:"total_unread_mentions"`
	TotalUnreadUrgentMentions int64            `json:"total_unread_urgent_mentions"`
	Threads                   []ThreadResponse `json:"threads,omitempty"`
}

// PostTypeEphemeral is a post only its recipient sees, never stored by the
// server: a slash command's answer, delivered by the ephemeral_message
// event (app/post.go SendEphemeralPost).
const PostTypeEphemeral = "system_ephemeral"

// UserAutocomplete is GET /users/autocomplete with in_channel: members of
// the channel and, apart, team members outside it.
type UserAutocomplete struct {
	Users        []User `json:"users"`
	OutOfChannel []User `json:"out_of_channel,omitempty"`
}

// Command is a slash command of GET /teams/{id}/commands/autocomplete
// (built-in ones included; the server lists only auto_complete ones).
type Command struct {
	Trigger          string `json:"trigger"`
	AutoComplete     bool   `json:"auto_complete"`
	AutoCompleteDesc string `json:"auto_complete_desc,omitempty"`
	AutoCompleteHint string `json:"auto_complete_hint,omitempty"`
	DisplayName      string `json:"display_name,omitempty"`
	Description      string `json:"description,omitempty"`
	DeleteAt         int64  `json:"delete_at,omitempty"`
}

// CommandArgs is the body of POST /commands/execute.
type CommandArgs struct {
	ChannelID string `json:"channel_id"`
	TeamID    string `json:"team_id,omitempty"`
	RootID    string `json:"root_id,omitempty"`
	Command   string `json:"command"`
}

// CommandResponse is what POST /commands/execute answers. An ephemeral
// text arrives separately as an ephemeral_message event.
type CommandResponse struct {
	ResponseType string `json:"response_type,omitempty"` // in_channel | ephemeral
	Text         string `json:"text,omitempty"`
	GotoLocation string `json:"goto_location,omitempty"`
}
