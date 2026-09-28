package ws

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
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

func TestDecodeEmoji(t *testing.T) {
	e, err := DecodeEmoji(ev("emoji_added", `{"emoji":"{\"id\":\"e1\",\"name\":\"parrot\",\"creator_id\":\"u1\"}"}`, Broadcast{}))
	require.NoError(t, err)
	assert.Equal(t, model.Emoji{ID: "e1", Name: "parrot", CreatorID: "u1"}, e)
}

func TestDecodeThreadUpdated(t *testing.T) {
	tu, err := DecodeThreadUpdated(ev("thread_updated",
		`{"thread":"{\"id\":\"r1\",\"reply_count\":3,\"unread_mentions\":1,\"unread_replies\":2}","previous_unread_mentions":0,"previous_unread_replies":1}`,
		Broadcast{TeamID: "t1", UserID: "u1"}))
	require.NoError(t, err)
	assert.Equal(t, "r1", tu.Thread.PostID)
	assert.Equal(t, int64(3), tu.Thread.ReplyCount)
	assert.Equal(t, int64(1), tu.Thread.UnreadMentions)
	assert.Equal(t, int64(0), tu.PreviousUnreadMentions)
	assert.Equal(t, int64(1), tu.PreviousUnreadReplies)
	assert.Equal(t, "t1", tu.TeamID)
}

func TestDecodeThreadUpdatedRejectsGarbage(t *testing.T) {
	_, err := DecodeThreadUpdated(ev("thread_updated", `{"thread":"not json"}`, Broadcast{}))
	assert.Error(t, err)
}

func TestDecodeThreadReadChanged(t *testing.T) {
	rc, err := DecodeThreadReadChanged(ev("thread_read_changed",
		`{"thread_id":"r1","timestamp":100,"unread_mentions":0,"unread_replies":0,"previous_unread_mentions":2,"previous_unread_replies":3,"channel_id":"c1"}`,
		Broadcast{TeamID: "t1"}))
	require.NoError(t, err)
	assert.Equal(t, "r1", rc.ThreadID)
	assert.Equal(t, int64(100), rc.Timestamp)
	assert.Equal(t, "c1", rc.ChannelID)
	assert.Equal(t, "t1", rc.TeamID)
	assert.Equal(t, int64(2), rc.PreviousUnreadMentions)
	assert.Equal(t, int64(3), rc.PreviousUnreadReplies)

	// "all threads read" — no thread_id.
	all, err := DecodeThreadReadChanged(ev("thread_read_changed", `{"timestamp":5}`, Broadcast{TeamID: "t1"}))
	require.NoError(t, err)
	assert.Equal(t, "", all.ThreadID)
}
