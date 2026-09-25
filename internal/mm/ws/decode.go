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
