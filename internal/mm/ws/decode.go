package ws

import (
	"encoding/json"
	"errors"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// Str returns a string field of the event data ("" if absent or not a string).
func (e Event) Str(key string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(e.Data, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[key], &s) != nil {
		return ""
	}
	return s
}

// embedded decodes data[key], a JSON document encoded as a string (Mattermost
// double-encodes posts, reactions, channels, members and preferences).
func embedded(e Event, key string, out any) error {
	s := e.Str(key)
	if s == "" {
		return errors.New("ws: missing " + key)
	}
	return json.Unmarshal([]byte(s), out)
}

type Posted struct {
	Post        model.Post
	ChannelType string
	ChannelName string
	TeamID      string
	SenderName  string
	Mentions    []string // contains our id only if we are mentioned
	Followers   []string // contains our id only if we follow the thread
}

func DecodePosted(e Event) (Posted, error) {
	var out Posted
	if err := embedded(e, "post", &out.Post); err != nil {
		return Posted{}, err
	}
	out.ChannelType, out.ChannelName = e.Str("channel_type"), e.Str("channel_display_name")
	out.TeamID, out.SenderName = e.Str("team_id"), e.Str("sender_name")
	if m := e.Str("mentions"); m != "" {
		_ = json.Unmarshal([]byte(m), &out.Mentions)
	}
	if f := e.Str("followers"); f != "" {
		_ = json.Unmarshal([]byte(f), &out.Followers)
	}
	return out, nil
}

func DecodePost(e Event) (model.Post, error) {
	var p model.Post
	return p, embedded(e, "post", &p)
}

func DecodeReaction(e Event) (model.Reaction, error) {
	var r model.Reaction
	return r, embedded(e, "reaction", &r)
}

func DecodeChannelTimes(e Event) (map[string]int64, error) {
	var d struct {
		ChannelTimes map[string]int64 `json:"channel_times"`
	}
	err := json.Unmarshal(e.Data, &d)
	return d.ChannelTimes, err
}

func DecodePostUnread(e Event) (model.ChannelUnreadAt, error) {
	var u model.ChannelUnreadAt
	err := json.Unmarshal(e.Data, &u)
	u.ChannelID, u.TeamID = e.Broadcast.ChannelID, e.Broadcast.TeamID
	return u, err
}

func DecodeChannel(e Event) (model.Channel, error) {
	var c model.Channel
	return c, embedded(e, "channel", &c)
}

func DecodeMember(e Event) (model.ChannelMember, error) {
	var m model.ChannelMember
	return m, embedded(e, "channelMember", &m)
}

func DecodePreferences(e Event) ([]model.Preference, error) {
	var p []model.Preference
	return p, embedded(e, "preferences", &p)
}

// DecodeUser decodes user_updated, which carries the user as an object, not a string.
func DecodeUser(e Event) (model.User, error) {
	var d struct {
		User model.User `json:"user"`
	}
	err := json.Unmarshal(e.Data, &d)
	return d.User, err
}

// DecodeEmoji decodes emoji_added (data.emoji is a JSON string).
func DecodeEmoji(e Event) (model.Emoji, error) {
	var out model.Emoji
	return out, embedded(e, "emoji", &out)
}

// ThreadUpdated is thread_updated: sent to one subscriber at a time
// (broadcast.user_id), TeamID is the channel's team ("" for DM/GM).
type ThreadUpdated struct {
	Thread                 model.ThreadResponse
	PreviousUnreadMentions int64
	PreviousUnreadReplies  int64
	TeamID                 string
}

// DecodeThreadUpdated decodes thread_updated (data.thread is a JSON string).
func DecodeThreadUpdated(e Event) (ThreadUpdated, error) {
	var out ThreadUpdated
	if err := embedded(e, "thread", &out.Thread); err != nil {
		return ThreadUpdated{}, err
	}
	var d struct {
		PreviousUnreadMentions int64 `json:"previous_unread_mentions"`
		PreviousUnreadReplies  int64 `json:"previous_unread_replies"`
	}
	_ = json.Unmarshal(e.Data, &d)
	out.PreviousUnreadMentions, out.PreviousUnreadReplies = d.PreviousUnreadMentions, d.PreviousUnreadReplies
	out.TeamID = e.Broadcast.TeamID
	return out, nil
}

// ThreadReadChanged is thread_read_changed. ThreadID is "" when every
// thread of a channel or team was marked read at once (no per-thread id).
type ThreadReadChanged struct {
	ThreadID       string
	Timestamp      int64
	UnreadMentions int64
	UnreadReplies  int64
	ChannelID      string
	TeamID         string
}

func DecodeThreadReadChanged(e Event) (ThreadReadChanged, error) {
	var d struct {
		ThreadID       string `json:"thread_id"`
		Timestamp      int64  `json:"timestamp"`
		UnreadMentions int64  `json:"unread_mentions"`
		UnreadReplies  int64  `json:"unread_replies"`
		ChannelID      string `json:"channel_id"`
	}
	err := json.Unmarshal(e.Data, &d)
	return ThreadReadChanged{
		ThreadID: d.ThreadID, Timestamp: d.Timestamp, UnreadMentions: d.UnreadMentions,
		UnreadReplies: d.UnreadReplies, ChannelID: d.ChannelID, TeamID: e.Broadcast.TeamID,
	}, err
}
