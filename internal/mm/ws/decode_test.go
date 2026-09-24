package ws

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ev(typ, data string, b Broadcast) Event {
	return Event{Type: typ, Data: json.RawMessage(data), Broadcast: b}
}

func TestDecoders(t *testing.T) {
	p, err := DecodePosted(ev("posted", `{"post":"{\"id\":\"p1\",\"message\":\"m\",\"channel_id\":\"c1\"}","channel_type":"D","sender_name":"@bob","team_id":"","mentions":"[\"u1\"]","followers":"[\"u1\"]"}`, Broadcast{ChannelID: "c1"}))
	require.NoError(t, err)
	assert.Equal(t, "p1", p.Post.ID)
	assert.Equal(t, "D", p.ChannelType)
	assert.Equal(t, []string{"u1"}, p.Mentions)
	assert.Equal(t, []string{"u1"}, p.Followers)

	post, err := DecodePost(ev("post_edited", `{"post":"{\"id\":\"p2\",\"edit_at\":5}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, int64(5), post.EditAt)

	r, err := DecodeReaction(ev("reaction_added", `{"reaction":"{\"user_id\":\"u2\",\"post_id\":\"p1\",\"emoji_name\":\"+1\"}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, "+1", r.EmojiName)

	times, err := DecodeChannelTimes(ev("multiple_channels_viewed", `{"channel_times":{"c1":10,"c2":20}}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, int64(20), times["c2"])

	u, err := DecodePostUnread(ev("post_unread", `{"msg_count":3,"mention_count":1,"last_viewed_at":7,"post_id":"p1"}`, Broadcast{ChannelID: "c1", TeamID: "t1"}))
	require.NoError(t, err)
	assert.Equal(t, "c1", u.ChannelID)
	assert.Equal(t, int64(3), u.MsgCount)

	ch, err := DecodeChannel(ev("channel_updated", `{"channel":"{\"id\":\"c1\",\"display_name\":\"New\"}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, "New", ch.DisplayName)

	m, err := DecodeMember(ev("channel_member_updated", `{"channelMember":"{\"channel_id\":\"c1\",\"notify_props\":{\"mark_unread\":\"mention\"}}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.True(t, m.Muted())

	prefs, err := DecodePreferences(ev("preferences_changed", `{"preferences":"[{\"category\":\"a\",\"name\":\"b\",\"value\":\"c\"}]"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, "c", prefs[0].Value)

	usr, err := DecodeUser(ev("user_updated", `{"user":{"id":"u2","username":"bob"}}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, "bob", usr.Username)

	assert.Equal(t, "c9", ev("user_removed", `{"channel_id":"c9"}`, Broadcast{}).Str("channel_id"))
	assert.Equal(t, "", ev("x", `{"n":1}`, Broadcast{}).Str("n"))
}

func TestDecodePostedRejectsGarbage(t *testing.T) {
	_, err := DecodePosted(ev("posted", `{"post":"not json"}`, Broadcast{}))
	assert.Error(t, err)
}
