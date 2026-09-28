package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestPostThreadDefaultsAndCapsPerPage(t *testing.T) {
	var queries []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/posts/r1/thread", r.URL.Path)
		queries = append(queries, r.URL.RawQuery)
		hn := true
		_ = json.NewEncoder(w).Encode(model.PostList{Order: []string{"r1", "p1"}, Posts: map[string]model.Post{
			"r1": {ID: "r1"}, "p1": {ID: "p1"},
		}, HasNext: &hn})
	})
	ctx := context.Background()

	l, err := c.PostThread(ctx, "r1", ThreadQuery{})
	require.NoError(t, err)
	assert.Equal(t, "r1", l.Order[0])
	require.NotNil(t, l.HasNext)
	assert.True(t, *l.HasNext)

	_, err = c.PostThread(ctx, "r1", ThreadQuery{PerPage: 500, CollapsedThreads: true})
	require.NoError(t, err)

	_, err = c.PostThread(ctx, "r1", ThreadQuery{PerPage: 60, FromCreateAt: 1000, FromPost: "p9"})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"collapsedThreads=false&direction=up&perPage=60",
		"collapsedThreads=true&direction=up&perPage=200",
		"collapsedThreads=false&direction=up&fromCreateAt=1000&fromPost=p9&perPage=60",
	}, queries, "perPage never omitted (0 would ask the server for the whole thread); capped at 200")
}

func TestMarkThreadReadIsAPutWithoutBody(t *testing.T) {
	var method, path, body string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		b := make([]byte, 1)
		n, _ := r.Body.Read(b)
		body = string(b[:n])
		_ = json.NewEncoder(w).Encode(model.ThreadResponse{PostID: "r1"})
	})
	require.NoError(t, c.MarkThreadRead(context.Background(), "t1", "r1", 12345))
	assert.Equal(t, http.MethodPut, method)
	assert.Equal(t, "/api/v4/users/me/teams/t1/threads/r1/read/12345", path)
	assert.Empty(t, body, "PUT read carries no body")
}

func TestMarkThreadReadNotFoundIsNotAnError404Surface(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "app.user.get_thread_membership_for_user.not_found", "status_code": 404})
	})
	err := c.MarkThreadRead(context.Background(), "t1", "r1", 1)
	require.Error(t, err) // the caller (state layer) decides to swallow 404; the client just classifies it
	assert.Equal(t, http.StatusNotFound, err.(*Error).Status)
}

func TestTeamsUnread(t *testing.T) {
	var query string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/users/me/teams/unread", r.URL.Path)
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]model.TeamUnread{{TeamID: "t1", ThreadMentionCount: 2}})
	})
	out, err := c.TeamsUnread(context.Background(), true)
	require.NoError(t, err)
	assert.Equal(t, "include_collapsed_threads=true", query)
	require.Len(t, out, 1)
	assert.Equal(t, int64(2), out[0].ThreadMentionCount)
}

func TestThreadTotals(t *testing.T) {
	var queries []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/users/me/teams/t1/threads", r.URL.Path)
		queries = append(queries, r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode(model.ThreadTotals{Total: 3, TotalUnreadMentions: 1})
	})
	ctx := context.Background()
	out, err := c.ThreadTotals(ctx, "t1", false)
	require.NoError(t, err)
	assert.Equal(t, int64(3), out.Total)
	_, err = c.ThreadTotals(ctx, "t1", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"totalsOnly=true", "excludeDirect=true&totalsOnly=true"}, queries)
}
