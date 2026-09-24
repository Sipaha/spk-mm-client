package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostPropsTolerateOddTypes(t *testing.T) {
	raw := `{"id":"p1","message":"hi","props":{
		"from_bot":"true","from_webhook":true,"override_username":42,
		"attachments":[{"title":"T","text":"body","fields":[{"title":"n","value":7,"short":"true"}]}],
		"unknown":{"x":[1,2]}}}`
	var p Post
	require.NoError(t, json.Unmarshal([]byte(raw), &p))
	assert.True(t, bool(p.Props.FromBot))
	assert.True(t, bool(p.Props.FromWebhook))
	assert.Equal(t, "42", string(p.Props.OverrideUsername))
	require.Len(t, p.Props.Attachments, 1)
	assert.Equal(t, "7", string(p.Props.Attachments[0].Fields[0].Value))
	assert.True(t, bool(p.Props.Attachments[0].Fields[0].Short))
}

func TestBrokenAttachmentsDoNotDropThePost(t *testing.T) {
	var p Post
	require.NoError(t, json.Unmarshal([]byte(`{"id":"p1","props":{"attachments":"garbage"}}`), &p))
	assert.Equal(t, "p1", p.ID)
	assert.Nil(t, p.Props.Attachments)
}

func TestPropsRoundTrip(t *testing.T) {
	p := Post{ID: "p1", Props: PostProps{FromBot: true, OverrideUsername: "bot"}}
	b, err := json.Marshal(p)
	require.NoError(t, err)
	var q Post
	require.NoError(t, json.Unmarshal(b, &q))
	assert.Equal(t, p.Props, q.Props)
}

func TestPostListAscending(t *testing.T) {
	l := PostList{
		Order: []string{"c", "b", "a", "missing"},
		Posts: map[string]Post{"a": {ID: "a", CreateAt: 1}, "b": {ID: "b", CreateAt: 2}, "c": {ID: "c", CreateAt: 3}},
	}
	got := l.Ascending()
	require.Len(t, got, 3)
	assert.Equal(t, []string{"a", "b", "c"}, []string{got[0].ID, got[1].ID, got[2].ID})
}

func TestPostListAscendingSortsByCreateAtNotOrder(t *testing.T) {
	// since-responses list order by CreateAt DESC but posts may be absent from order (roots of replies)
	l := PostList{Order: []string{"a", "b"}, Posts: map[string]Post{"a": {ID: "a", CreateAt: 5}, "b": {ID: "b", CreateAt: 9}}}
	got := l.Ascending()
	assert.Equal(t, "a", got[0].ID)
	assert.Equal(t, "b", got[1].ID)
}

func TestDMPartnerAndMuted(t *testing.T) {
	c := Channel{Type: ChannelDirect, Name: "u1__u2"}
	assert.Equal(t, "u2", c.DMPartner("u1"))
	assert.Equal(t, "u1", c.DMPartner("u2"))
	assert.Equal(t, "u1", Channel{Type: ChannelDirect, Name: "u1__u1"}.DMPartner("u1"))
	assert.Equal(t, "", Channel{Type: ChannelOpen, Name: "town-square"}.DMPartner("u1"))
	assert.True(t, ChannelMember{NotifyProps: map[string]string{"mark_unread": "mention"}}.Muted())
	assert.False(t, ChannelMember{}.Muted())
}
