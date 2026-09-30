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

func TestSearchPostsBodyPathAndPerPageCap(t *testing.T) {
	var bodies []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v4/teams/t%2F1/posts/search", r.URL.EscapedPath())
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		_, _ = w.Write([]byte(`{"order":["p2","p1"],"posts":{"p1":{"id":"p1","channel_id":"c1"},"p2":{"id":"p2","channel_id":"c2"}},` +
			`"next_post_id":"","prev_post_id":"","matches":null}`))
	})
	ctx := context.Background()
	res, err := c.SearchPosts(ctx, "t/1", model.SearchParams{Terms: `from:bob "a b"`, TimeZoneOffset: 10800, Page: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"p2", "p1"}, res.Order, "order as the server sent it (newest first)")
	assert.Equal(t, "c2", res.Posts["p2"].ChannelID)
	assert.Nil(t, res.Matches, "the database engine sends matches: null")

	_, err = c.SearchPosts(ctx, "t/1", model.SearchParams{Terms: "x", IsOrSearch: true, IncludeDeletedChannels: true, PerPage: 500})
	require.NoError(t, err)
	_, err = c.SearchPosts(ctx, "t/1", model.SearchParams{Terms: "x", PerPage: 50})
	require.NoError(t, err)

	require.Len(t, bodies, 3)
	assert.JSONEq(t, `{"terms":"from:bob \"a b\"","is_or_search":false,"time_zone_offset":10800,"include_deleted_channels":false,"page":2,"per_page":20}`,
		bodies[0], "snake_case like the server's SearchParameter; per_page 0 → 20")
	assert.JSONEq(t, `{"terms":"x","is_or_search":true,"time_zone_offset":0,"include_deleted_channels":true,"page":0,"per_page":200}`,
		bodies[1], "per_page capped at 200")
	assert.JSONEq(t, `{"terms":"x","is_or_search":false,"time_zone_offset":0,"include_deleted_channels":false,"page":0,"per_page":50}`, bodies[2])
}

func TestSearchPostsDecodesMatches(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"order":["p1"],"posts":{"p1":{"id":"p1"}},"matches":{"p1":["deploy","deployed"]}}`))
	})
	res, err := c.SearchPosts(context.Background(), "t1", model.SearchParams{Terms: "deploy"})
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"p1": {"deploy", "deployed"}}, res.Matches, "a search engine's matches, when there are any")
}

func TestSearchPostsIsNeverRetried(t *testing.T) {
	var n atomic.Int32
	c, slept := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err := c.SearchPosts(context.Background(), "t1", model.SearchParams{Terms: "x"})
	assert.True(t, IsNetwork(err))
	assert.Equal(t, int32(1), n.Load(), "one attempt on 502")
	assert.Empty(t, *slept)

	n.Store(0)
	c, _ = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	})
	_, err = c.SearchPosts(context.Background(), "t1", model.SearchParams{Terms: "x"})
	assert.True(t, IsNetwork(err))
	assert.Equal(t, int32(1), n.Load(), "one attempt on a transport error: POST is not replayed")
}

func TestSearchPostsErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		id     string
		check  func(error) bool
	}{
		{401, "api.context.session_expired.app_error", IsAuth},
		{403, "api.context.permissions.app_error", IsAuth},
		{400, "api.context.invalid_param.app_error", func(err error) bool { return kindOf(err) == KindAPI }},
	} {
		c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": tc.id, "message": "m", "status_code": tc.status})
		})
		_, err := c.SearchPosts(context.Background(), "t1", model.SearchParams{Terms: "x"})
		require.Error(t, err)
		assert.True(t, tc.check(err), "%d", tc.status)
		var e *Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, tc.status, e.Status)
		assert.Equal(t, tc.id, e.ID)
	}
}

func TestPostByID(t *testing.T) {
	var got []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		if r.URL.Path == "/api/v4/posts/gone" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"id":"app.post.get.app_error","message":"not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"p1","channel_id":"c1","root_id":"r1","message":"hi","create_at":5}`))
	})
	p, err := c.Post(context.Background(), "p1")
	require.NoError(t, err)
	assert.Equal(t, model.Post{ID: "p1", ChannelID: "c1", RootID: "r1", Message: "hi", CreateAt: 5}, p)

	_, err = c.Post(context.Background(), "gone")
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, 404, e.Status)
	assert.Equal(t, KindAPI, e.Kind)
	assert.Equal(t, []string{"GET /api/v4/posts/p1?", "GET /api/v4/posts/gone?"}, got)
}
