package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestCreatePostSendsPendingIDAndAccepts201(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/posts", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var in map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		assert.Equal(t, "c1", in["channel_id"])
		assert.Equal(t, "hello", in["message"])
		assert.Equal(t, "u1:1", in["pending_post_id"])
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"p1","channel_id":"c1","message":"hello","pending_post_id":"u1:1","create_at":5}`))
	})
	p, err := c.CreatePost(context.Background(), model.Post{ChannelID: "c1", Message: "hello", PendingPostID: "u1:1", UserID: "u1"})
	require.NoError(t, err)
	assert.Equal(t, "p1", p.ID)
}

func TestCreatePostIsNotRetriedOnServerError(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.CreatePost(context.Background(), model.Post{ChannelID: "c1", Message: "x"})
	require.Error(t, err)
	assert.Equal(t, int32(1), n.Load())
}

func TestPatchDeleteViewUnreadPrefs(t *testing.T) {
	var seen []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/v4/posts/p1/patch":
			assert.Equal(t, "edited", body["message"])
			_, _ = w.Write([]byte(`{"id":"p1","message":"edited","edit_at":9}`))
		case "/api/v4/posts/p1":
			_, _ = w.Write([]byte(`{"status":"OK"}`))
		case "/api/v4/channels/members/me/view":
			assert.Equal(t, "c1", body["channel_id"])
			assert.Equal(t, true, body["collapsed_threads_supported"])
			_, _ = w.Write([]byte(`{"status":"OK"}`))
		case "/api/v4/users/me/posts/p1/set_unread":
			assert.Equal(t, true, body["collapsed_threads_supported"])
			_, _ = w.Write([]byte(`{"channel_id":"c1","msg_count":3,"last_viewed_at":7}`))
		case "/api/v4/users/me/preferences":
			_, _ = w.Write([]byte(`{"status":"OK"}`))
		}
	})
	ctx := context.Background()
	p, err := c.PatchPost(ctx, "p1", "edited")
	require.NoError(t, err)
	assert.Equal(t, int64(9), p.EditAt)
	require.NoError(t, c.DeletePost(ctx, "p1"))
	require.NoError(t, c.ViewChannel(ctx, "c1"))
	u, err := c.SetUnread(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, int64(3), u.MsgCount)
	require.NoError(t, c.SavePreferences(ctx, []model.Preference{{UserID: "u1", Category: "direct_channel_show", Name: "u2", Value: "true"}}))
	assert.Equal(t, []string{
		"PUT /api/v4/posts/p1/patch", "DELETE /api/v4/posts/p1", "POST /api/v4/channels/members/me/view",
		"POST /api/v4/users/me/posts/p1/set_unread", "PUT /api/v4/users/me/preferences",
	}, seen)
}

func TestReactionCalls(t *testing.T) {
	var got []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.EscapedPath()+" "+string(b))
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"user_id":"u1","post_id":"p1","emoji_name":"+1","create_at":5}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	})
	ctx := context.Background()
	r, err := c.SaveReaction(ctx, model.Reaction{UserID: "u1", PostID: "p1", EmojiName: "+1"})
	require.NoError(t, err)
	assert.Equal(t, int64(5), r.CreateAt)
	require.NoError(t, c.DeleteReaction(ctx, "u1", "p1", "+1"))
	assert.Equal(t, []string{
		`POST /api/v4/reactions {"emoji_name":"+1","post_id":"p1","user_id":"u1"}`,
		`DELETE /api/v4/users/u1/posts/p1/reactions/+1 `,
	}, got)
}

func TestPostReactions(t *testing.T) {
	var path string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.EscapedPath()
		_, _ = w.Write([]byte(`[{"user_id":"u1","post_id":"p1","emoji_name":"+1"}]`))
	})
	rs, err := c.PostReactions(context.Background(), "p1")
	require.NoError(t, err)
	assert.Equal(t, "GET /api/v4/posts/p1/reactions", path)
	assert.Equal(t, []model.Reaction{{UserID: "u1", PostID: "p1", EmojiName: "+1"}}, rs)
}
