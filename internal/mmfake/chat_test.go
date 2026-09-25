package mmfake

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

type authed struct {
	t   *testing.T
	s   *Server
	tok string
}

func loginAs(t *testing.T, s *Server, user string) authed {
	t.Helper()
	resp, err := http.Post(s.URL()+"/api/v4/users/login", "application/json", strings.NewReader(`{"login_id":"`+user+`","password":"secret"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	return authed{t: t, s: s, tok: resp.Header.Get("Token")}
}

func (a authed) call(method, path string, body any, out any) int {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.s.URL()+path, rd)
	req.Header.Set("Authorization", "Bearer "+a.tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		require.NoError(a.t, json.NewDecoder(resp.Body).Decode(out))
	}
	return resp.StatusCode
}

func TestSeedVisibleToAlice(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var teams []model.Team
	require.Equal(t, 200, a.call("GET", "/api/v4/users/me/teams", nil, &teams))
	assert.Equal(t, "Fake Team", teams[0].DisplayName)
	var chans []model.Channel
	a.call("GET", "/api/v4/users/me/channels", nil, &chans)
	assert.Len(t, chans, 5)
	var members []model.ChannelMember
	a.call("GET", "/api/v4/users/me/channel_members?page=0&per_page=200", nil, &members)
	assert.Len(t, members, 5)
	town := s.Channel("c-town")
	assert.Equal(t, int64(150), town.TotalMsgCount)
	assert.Equal(t, town.TotalMsgCount, s.Member("c-town", "alice").MsgCount, "seed is read")
	var cats model.OrderedCategories
	a.call("GET", "/api/v4/users/me/teams/t-fake/channels/categories", nil, &cats)
	byType := map[string][]string{}
	for _, c := range cats.Categories {
		byType[c.Type] = c.ChannelIDs
	}
	assert.ElementsMatch(t, []string{"c-town", "c-offtopic", "c-secret"}, byType["channels"])
	assert.ElementsMatch(t, []string{"c-dm-bob", "c-gm"}, byType["direct_messages"])
	assert.Len(t, cats.Order, 3)
}

func TestPostsPagingAndBefore(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var page model.PostList
	a.call("GET", "/api/v4/channels/c-town/posts?page=0&per_page=60", nil, &page)
	require.Len(t, page.Order, 60)
	asc := page.Ascending()
	assert.Equal(t, "Message #150", asc[59].Message)
	assert.NotEmpty(t, page.PrevPostID)
	var older model.PostList
	a.call("GET", "/api/v4/channels/c-town/posts?page=0&per_page=100&before="+asc[0].ID, nil, &older)
	assert.Len(t, older.Order, 90)
	assert.Empty(t, older.PrevPostID, "reached the beginning")
	assert.Equal(t, 403, a.call("GET", "/api/v4/channels/c-nope/posts", nil, nil))
}

func TestCreateIsIdempotentByPendingIDAndCountsMentions(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	b := loginAs(t, s, "bob")
	var p1, p2 model.Post
	body := map[string]string{"channel_id": "c-offtopic", "message": "hi @alice", "pending_post_id": "u-bob:1"}
	require.Equal(t, 201, b.call("POST", "/api/v4/posts", body, &p1))
	require.Equal(t, 201, b.call("POST", "/api/v4/posts", body, &p2))
	assert.Equal(t, p1.ID, p2.ID)
	assert.Equal(t, "u-bob:1", p1.PendingPostID)
	m := s.Member("c-offtopic", "alice")
	assert.Equal(t, int64(1), m.MentionCount)
	assert.Equal(t, s.Channel("c-offtopic").TotalMsgCount-1, m.MsgCount)
	assert.Equal(t, s.Channel("c-offtopic").TotalMsgCount, s.Member("c-offtopic", "bob").MsgCount, "own post is read")
}

func TestDMCountsAsMention(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	s.PostAs("c-dm-bob", "bob", "no at-sign here")
	assert.Equal(t, int64(1), s.Member("c-dm-bob", "alice").MentionCount)
}

func TestSinceReturnsEditsDeletesAndHistoryAndHonoursLimit(t *testing.T) {
	s := Start(Options{SinceLimit: 3})
	defer s.Close()
	a := loginAs(t, s, "alice")
	mark := s.Channel("c-offtopic").LastPostAt
	p := s.PostAs("c-offtopic", "bob", "one")
	s.EditAs(p.ID, "one!")
	q := s.PostAs("c-offtopic", "bob", "two")
	s.DeleteAs(q.ID)
	var l model.PostList
	a.call("GET", "/api/v4/channels/c-offtopic/posts?since="+itoa(mark), nil, &l)
	require.Len(t, l.Order, 3, "limit applied: edited p, deleted q, history row of p")
	var sawHistory, sawDeleted bool
	for _, id := range l.Order {
		post := l.Posts[id]
		if post.OriginalID == p.ID {
			sawHistory = true
		}
		if post.ID == q.ID && post.DeleteAt > 0 {
			sawDeleted = true
		}
	}
	assert.True(t, sawHistory)
	assert.True(t, sawDeleted)
	vis := s.VisiblePosts("c-offtopic")
	assert.Equal(t, "one!", vis[len(vis)-1].Message)
}

func TestCRTFiltersReplies(t *testing.T) {
	s := Start(Options{CRT: true, SeedPosts: -1})
	defer s.Close()
	a := loginAs(t, s, "alice")
	root := s.PostAs("c-town", "bob", "root")
	s.ReplyAs("c-town", root.ID, "carol", "reply")
	var l model.PostList
	a.call("GET", "/api/v4/channels/c-town/posts?page=0&per_page=60&collapsedThreads=true", nil, &l)
	require.Len(t, l.Order, 1)
	assert.Equal(t, int64(1), l.Posts[root.ID].ReplyCount)
	var cfg map[string]string
	a.call("GET", "/api/v4/config/client?format=old", nil, &cfg)
	assert.Equal(t, "always_on", cfg["CollapsedThreads"])
}

func TestViewAndSetUnread(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	p1 := s.PostAs("c-offtopic", "bob", "a @alice")
	s.PostAs("c-offtopic", "bob", "b")
	require.Equal(t, 200, a.call("POST", "/api/v4/channels/members/me/view", map[string]any{"channel_id": "c-offtopic"}, nil))
	m := s.Member("c-offtopic", "alice")
	assert.Equal(t, s.Channel("c-offtopic").TotalMsgCount, m.MsgCount)
	assert.Zero(t, m.MentionCount)
	var u model.ChannelUnreadAt
	require.Equal(t, 200, a.call("POST", "/api/v4/users/me/posts/"+p1.ID+"/set_unread", map[string]any{"collapsed_threads_supported": true}, &u))
	assert.Equal(t, s.Channel("c-offtopic").TotalMsgCount-2, u.MsgCount)
	assert.Equal(t, int64(1), u.MentionCount)
	names := []string{}
	for _, e := range s.Events() {
		names = append(names, e.Name)
	}
	assert.Contains(t, names, "multiple_channels_viewed")
	assert.Contains(t, names, "post_unread")
}

func TestPatchAndDeleteOnlyOwnPosts(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	bobs := s.PostAs("c-town", "bob", "bob's")
	assert.Equal(t, 403, a.call("PUT", "/api/v4/posts/"+bobs.ID+"/patch", map[string]string{"message": "x"}, nil))
	var mine model.Post
	a.call("POST", "/api/v4/posts", map[string]string{"channel_id": "c-town", "message": "mine"}, &mine)
	var edited model.Post
	require.Equal(t, 200, a.call("PUT", "/api/v4/posts/"+mine.ID+"/patch", map[string]string{"message": "mine2"}, &edited))
	assert.NotZero(t, edited.EditAt)
	require.Equal(t, 200, a.call("DELETE", "/api/v4/posts/"+mine.ID, nil, nil))
	for _, p := range s.VisiblePosts("c-town") {
		assert.NotEqual(t, mine.ID, p.ID)
	}
}

func TestUsersPrefsStatus(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var users []model.User
	a.call("POST", "/api/v4/users/ids", []string{"u-bob", "u-carol", "nope"}, &users)
	assert.Len(t, users, 2)
	var prefs []model.Preference
	a.call("GET", "/api/v4/users/me/preferences", nil, &prefs)
	assert.Contains(t, prefs, model.Preference{UserID: "u-alice", Category: "direct_channel_show", Name: "u-bob", Value: "true"})
	require.Equal(t, 200, a.call("PUT", "/api/v4/users/me/preferences", []model.Preference{{UserID: "u-alice", Category: "x", Name: "y", Value: "z"}}, nil))
	a.call("GET", "/api/v4/users/me/preferences", nil, &prefs)
	assert.Contains(t, prefs, model.Preference{UserID: "u-alice", Category: "x", Name: "y", Value: "z"})
	s.SetStatus("alice", "dnd")
	var st model.Status
	a.call("GET", "/api/v4/users/me/status", nil, &st)
	assert.Equal(t, "dnd", st.Status)
	var me model.User
	a.call("GET", "/api/v4/users/me", nil, &me)
	assert.Equal(t, "mention", me.NotifyProps["desktop"])
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
