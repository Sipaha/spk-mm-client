package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestMyChannelsAndTeams(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/me/teams":
			_, _ = w.Write([]byte(`[{"id":"t1","name":"fake","display_name":"Fake"}]`))
		case "/api/v4/users/me/channels":
			_, _ = w.Write([]byte(`[{"id":"c1","team_id":"t1","type":"O","display_name":"Town","total_msg_count":5}]`))
		default:
			http.NotFound(w, r)
		}
	})
	teams, err := c.MyTeams(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Fake", teams[0].DisplayName)
	chans, err := c.MyChannels(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(5), chans[0].TotalMsgCount)
}

func TestMyChannelMembersPagesUntilShortPage(t *testing.T) {
	var pages []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/users/me/channel_members", r.URL.Path)
		assert.Equal(t, "200", r.URL.Query().Get("per_page"))
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		n := 200
		if page == "1" {
			n = 5
		}
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf(`{"channel_id":"c%s-%d","msg_count":1}`, page, i)
		}
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	})
	got, err := c.MyChannelMembers(context.Background())
	require.NoError(t, err)
	assert.Len(t, got, 205)
	assert.Equal(t, []string{"0", "1"}, pages)
}

func TestChannelPostsQueryParams(t *testing.T) {
	var q []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/channels/c1/posts", r.URL.Path)
		q = append(q, r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode(map[string]any{"order": []string{"p1"}, "posts": map[string]any{"p1": map[string]any{"id": "p1"}}, "prev_post_id": ""})
	})
	ctx := context.Background()
	_, err := c.ChannelPosts(ctx, "c1", PostsQuery{PerPage: 60, CollapsedThreads: true})
	require.NoError(t, err)
	_, err = c.ChannelPosts(ctx, "c1", PostsQuery{PerPage: 60, Before: "p9"})
	require.NoError(t, err)
	l, err := c.ChannelPosts(ctx, "c1", PostsQuery{Since: 1234, CollapsedThreads: true})
	require.NoError(t, err)
	assert.Equal(t, "p1", l.Order[0])
	assert.Equal(t, []string{
		"collapsedThreads=true&collapsedThreadsExtended=false&page=0&per_page=60",
		"before=p9&collapsedThreads=false&collapsedThreadsExtended=false&page=0&per_page=60",
		"collapsedThreads=true&collapsedThreadsExtended=false&since=1234&skipFetchThreads=true",
	}, q)
}

func TestUsersByIDsChunksBy100(t *testing.T) {
	var sizes []int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v4/users/ids", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var ids []string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&ids))
		sizes = append(sizes, len(ids))
		out := make([]map[string]string, len(ids))
		for i, id := range ids {
			out[i] = map[string]string{"id": id, "username": "u" + id}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	ids := make([]string, 150)
	for i := range ids {
		ids[i] = fmt.Sprint(i)
	}
	users, err := c.UsersByIDs(context.Background(), ids)
	require.NoError(t, err)
	assert.Len(t, users, 150)
	assert.Equal(t, []int{100, 50}, sizes)
	none, err := c.UsersByIDs(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestCategoriesPrefsStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/me/teams/t1/channels/categories":
			_, _ = w.Write([]byte(`{"categories":[{"id":"k1","type":"channels","channel_ids":["c1"]}],"order":["k1"]}`))
		case "/api/v4/users/me/preferences":
			_, _ = w.Write([]byte(`[{"category":"display_settings","name":"name_format","value":"full_name"}]`))
		case "/api/v4/users/me/status":
			_, _ = w.Write([]byte(`{"user_id":"u1","status":"dnd","dnd_end_time":0}`))
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	cats, err := c.Categories(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, []string{"c1"}, cats.Categories[0].ChannelIDs)
	prefs, err := c.MyPreferences(ctx)
	require.NoError(t, err)
	assert.Equal(t, "full_name", prefs[0].Value)
	st, err := c.MyStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dnd", st.Status)
}

func TestLimiterSpacesRequests(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"OK"}`)) })
	c = c.WithLimiter(rate.NewLimiter(rate.Every(40*time.Millisecond), 1))
	start := time.Now()
	for i := 0; i < 4; i++ {
		require.NoError(t, c.Ping(context.Background()))
	}
	assert.GreaterOrEqual(t, time.Since(start), 110*time.Millisecond)
}

func TestLimiterWaitHonoursContext(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"OK"}`)) })
	c = c.WithLimiter(rate.NewLimiter(rate.Every(time.Hour), 1))
	require.NoError(t, c.Ping(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Ping(ctx)
	require.Error(t, err)
	assert.True(t, IsNetwork(err))
}
