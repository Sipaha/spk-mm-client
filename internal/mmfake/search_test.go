package mmfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// search runs POST /teams/t-fake/posts/search and returns the hit ids in
// response order (newest first).
func (a authed) search(terms string, orSearch bool) []string {
	a.t.Helper()
	res := a.searchPage("t-fake", terms, orSearch, 0, 60)
	return res.Order
}

func (a authed) searchPage(team, terms string, orSearch bool, page, perPage int) model.PostSearchResults {
	a.t.Helper()
	var res model.PostSearchResults
	st := a.call("POST", "/api/v4/teams/"+team+"/posts/search", model.SearchParams{
		Terms: terms, IsOrSearch: orSearch, Page: page, PerPage: perPage,
	}, &res)
	require.Equal(a.t, 200, st, "search %q", terms)
	for _, id := range res.Order {
		require.Contains(a.t, res.Posts, id, "every order id has its post")
	}
	return res
}

type quokkaPosts struct{ p1, p2, p3, p4 model.Post }

// seedQuokka posts four searchable messages, oldest first.
func seedQuokka(s *Server) quokkaPosts {
	return quokkaPosts{
		p1: s.PostAs("c-town", "bob", "Zephyr Quokka rollout tonight"),
		p2: s.PostAs("c-town", "carol", "quokka zephyr-fix failed"),
		p3: s.PostAs("c-offtopic", "bob", "the quokka is green"),
		p4: s.PostAs("c-town", "bob", "Привет, Мир! Квокка тут"),
	}
}

func TestSearchWordsAndPhrase(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	q := seedQuokka(s)
	a := loginAs(t, s, "alice")

	assert.Equal(t, []string{q.p2.ID, q.p1.ID}, a.search("zephyr quokka", false), "AND of words, newest first")
	assert.Equal(t, []string{q.p2.ID, q.p1.ID}, a.search("ZEPHYR", false), "case-insensitive")
	assert.Equal(t, []string{q.p3.ID, q.p2.ID, q.p1.ID}, a.search("zephyr green", true), "is_or_search: OR of words")
	assert.Empty(t, a.search("zephyr green", false))
	assert.Empty(t, a.search("zeph", false), "whole words only, no substring")
	assert.Equal(t, []string{q.p1.ID}, a.search(`"quokka rollout"`, false), "phrase: adjacent words")
	assert.Empty(t, a.search(`"rollout quokka"`, false), "phrase keeps word order")
	assert.Equal(t, []string{q.p4.ID}, a.search("квокка", false), "Unicode case folding")
	assert.Equal(t, []string{q.p4.ID}, a.search("мир!", false), "punctuation is not part of a word")
}

func TestSearchPrefixAndExclusion(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	q := seedQuokka(s)
	a := loginAs(t, s, "alice")

	assert.Equal(t, []string{q.p2.ID, q.p1.ID}, a.search("zeph*", false), "word* is a word prefix")
	assert.Equal(t, []string{q.p4.ID}, a.search("КВОК*", false))
	assert.Equal(t, []string{q.p3.ID}, a.search("quokka -zephyr", false), "-word excludes")
	assert.Equal(t, []string{q.p1.ID}, a.search(`quokka -"zephyr fix" -green`, false), "-\"phrase\" excludes too")
	assert.Empty(t, a.search("-zephyr", false), "only excluded words: the server's tsquery is invalid, empty result")
	assert.Empty(t, a.search("*", false), "a bare * is never searched")
	assert.Empty(t, a.search("!!!", false), "a term without letters or digits is dropped")
}

func TestSearchFromAndIn(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	q := seedQuokka(s)
	dm := s.PostAs("c-dm-bob", "bob", "quokka in a direct message")
	gm := s.PostAs("c-gm", "carol", "quokka in a group")
	a := loginAs(t, s, "alice")

	assert.Equal(t, []string{gm.ID, dm.ID, q.p3.ID, q.p2.ID, q.p1.ID}, a.search("quokka from:bob from:carol", false),
		"several from: are ORed")
	assert.Equal(t, []string{dm.ID, q.p3.ID, q.p1.ID}, a.search("quokka from:bob", false))
	assert.Equal(t, []string{gm.ID, q.p2.ID}, a.search("quokka from:@carol", false), "@ is trimmed off a username")
	assert.Equal(t, []string{gm.ID, q.p2.ID}, a.search("quokka -from:bob", false))
	assert.Equal(t, []string{q.p2.ID, q.p1.ID}, a.search("quokka in:town-square", false), "in: takes the channel name (slug)")
	assert.Equal(t, []string{q.p3.ID}, a.search("quokka in:~off-topic", false), "~ is trimmed off a channel name")
	assert.Equal(t, []string{q.p3.ID, q.p2.ID, q.p1.ID}, a.search("quokka in:town-square channel:off-topic", false))
	assert.Equal(t, []string{gm.ID, dm.ID, q.p3.ID}, a.search("quokka -in:town-square", false))
	assert.Equal(t, []string{dm.ID}, a.search("quokka in:@bob", false), "in:@user is the DM with that user")
	assert.Equal(t, []string{gm.ID}, a.search("quokka in:@alice,bob,carol", false),
		"in:@a,b,c is the group message of exactly those users (the webapp lists every member, the caller too)")
	assert.Empty(t, a.search("quokka in:@bob,carol", false), "no GM of just bob and carol")
	assert.Equal(t, []string{q.p2.ID, q.p1.ID}, a.search("quokka in: town-square", false), "the value may follow the colon after a space")
	assert.Empty(t, a.search("quokka in:no-such-channel", false), "an unknown channel matches nothing")
	assert.Empty(t, a.search("quokka from:nobody", false), "an unknown user matches nothing")

	// Filters alone search everything they select.
	for _, id := range a.search("from:bob in:off-topic", false) {
		var p model.Post
		require.Equal(t, 200, a.call("GET", "/api/v4/posts/"+id, nil, &p))
		assert.Equal(t, "u-bob", p.UserID)
		assert.Equal(t, "c-offtopic", p.ChannelID)
	}
	assert.Equal(t, q.p3.ID, a.search("from:bob in:off-topic", false)[0])

	// Dates are days in the caller's time zone (time_zone_offset seconds).
	day := time.UnixMilli(q.p1.CreateAt).UTC().Format("2006-01-02")
	on := a.search("quokka on:"+day, false)
	assert.Contains(t, on, q.p1.ID)
	for _, id := range on {
		var p model.Post
		require.Equal(t, 200, a.call("GET", "/api/v4/posts/"+id, nil, &p))
		assert.Equal(t, day, time.UnixMilli(p.CreateAt).UTC().Format("2006-01-02"))
	}
	assert.Len(t, a.search("quokka after:2000-01-01", false), 5)
	assert.Empty(t, a.search("quokka before:2000-01-01", false))
	assert.Empty(t, a.search("quokka after:2999-01-01", false))
	assert.Len(t, a.search("quokka before:2999-01-01", false), 5)
}

func TestSearchOnlyMemberChannels(t *testing.T) {
	s := Start(Options{CRT: true})
	defer s.Close()
	q := seedQuokka(s)
	s.PostAs("c-offices", "bob", "quokka in a channel alice is not in")
	secret := s.PostAs("c-secret", "alice", "quokka in private")
	reply := s.ReplyAs("c-town", q.p2.ID, "carol", "quokka reply")
	s.EditAs(q.p1.ID, "Zephyr rollout moved") // the history row keeps "quokka"
	s.DeleteAs(q.p3.ID)
	s.mu.Lock()
	sys := &fpost{Post: model.Post{ID: newID(), ChannelID: "c-town", UserID: "u-bob", Type: "system_join_channel",
		Message: "quokka joined the channel", CreateAt: s.nowLocked()}}
	sys.UpdateAt = sys.CreateAt
	s.insertPostLocked(sys)
	s.mu.Unlock()

	a := loginAs(t, s, "alice")
	assert.Equal(t, []string{reply.ID, secret.ID, q.p2.ID}, a.search("quokka", false),
		"member channels only; replies too (even under CRT); no deleted posts, edit history or system messages")

	b := loginAs(t, s, "bob")
	hits := b.search("quokka", false)
	assert.NotContains(t, hits, secret.ID, "bob is not in c-secret")
	assert.Len(t, hits, 3, "bob: c-offices, the reply and p2")

	st, id := a.callErr("POST", "/api/v4/teams/t-fake/posts/search", model.SearchParams{Terms: ""})
	assert.Equal(t, 400, st)
	assert.Equal(t, "api.context.invalid_param.app_error", id)
	st, id = a.callErr("POST", "/api/v4/teams/t-fake/posts/search", model.SearchParams{Terms: "   "})
	assert.Equal(t, 200, st, "blank (not empty) terms parse to nothing: an empty result, like the server")
	assert.Empty(t, id)
	st, _ = a.callErr("POST", "/api/v4/teams/t-other/posts/search", model.SearchParams{Terms: "quokka"})
	assert.Equal(t, 403, st, "not a member of the team")

	req, _ := http.NewRequest("POST", s.URL()+"/api/v4/teams/t-fake/posts/search", strings.NewReader("{"))
	req.Header.Set("Authorization", "Bearer "+a.tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 400, resp.StatusCode, "a malformed body")
}

func TestSearchPagesNewestFirst(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	var ids []string // oldest first
	for i := 1; i <= 25; i++ {
		ids = append(ids, s.PostAs("c-town", "bob", fmt.Sprintf("pagetest %d", i)).ID)
	}
	a := loginAs(t, s, "alice")

	var got []string
	for page := 0; page < 4; page++ {
		res := a.searchPage("t-fake", "pagetest", false, page, 10)
		assert.Nil(t, res.Matches, "the database engine never fills matches")
		assert.Len(t, res.Order, []int{10, 10, 5, 0}[page])
		got = append(got, res.Order...)
	}
	want := make([]string, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		want = append(want, ids[i])
	}
	assert.Equal(t, want, got, "offset pages, newest first, no gaps or repeats")

	// matches is present and null on the wire, like the database engine.
	var raw map[string]json.RawMessage
	require.Equal(t, 200, a.call("POST", "/api/v4/teams/t-fake/posts/search", model.SearchParams{Terms: "pagetest", PerPage: 5}, &raw))
	assert.JSONEq(t, "null", string(raw["matches"]))

	calls := s.SearchCalls()
	require.Len(t, calls, 5)
	assert.Equal(t, SearchCall{TeamID: "t-fake", UserID: "u-alice", Terms: "pagetest", Page: 2, PerPage: 10}, calls[2])
	assert.Equal(t, 5, calls[4].PerPage)
}

func TestSearchSQLEnginePaging(t *testing.T) {
	s := Start(Options{SearchSQLEngine: true})
	defer s.Close()
	for i := 1; i <= 120; i++ {
		s.PostAs("c-town", "bob", fmt.Sprintf("sqlpage %d", i))
	}
	a := loginAs(t, s, "alice")
	assert.Len(t, a.searchPage("t-fake", "sqlpage", false, 0, 20).Order, 100,
		"page 0 ignores per_page and returns at most 100 (post_store.go search: Limit(100))")
	assert.Empty(t, a.searchPage("t-fake", "sqlpage", false, 1, 20).Order,
		"page > 0 is always empty (SearchPostsForUser: no paging for DB search)")
}

func TestChannelPostsAfterCursor(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	vis := s.VisiblePosts("c-town") // 150, oldest first
	require.Len(t, vis, 150)

	page := func(query string) model.PostList {
		t.Helper()
		var l model.PostList
		require.Equal(t, 200, a.call("GET", "/api/v4/channels/c-town/posts?page=0&"+query, nil, &l))
		for i := 1; i < len(l.Order); i++ {
			require.Greater(t, l.Posts[l.Order[i-1]].CreateAt, l.Posts[l.Order[i]].CreateAt, "newest first")
		}
		return l
	}

	// Walk forward from the oldest post with after=, 60 at a time.
	var got []string
	cursor := vis[0].ID
	for n := 0; ; n++ {
		require.Less(t, n, 10, "the walk ends")
		l := page("per_page=60&after=" + cursor)
		assert.Equal(t, cursor, l.PrevPostID, "page 0 of after=: prev_post_id is the cursor itself")
		for i := len(l.Order) - 1; i >= 0; i-- {
			got = append(got, l.Order[i])
		}
		if len(l.Order) < 60 {
			assert.Empty(t, l.NextPostID, "a short page is the end")
			break
		}
		cursor = l.Order[0] // the newest of the page
		assert.Equal(t, vis[indexOf(vis, cursor)+1].ID, l.NextPostID, "a full page names the next newer post")
	}
	want := make([]string, 0, 149)
	for _, p := range vis[1:] {
		want = append(want, p.ID)
	}
	assert.Equal(t, want, got, "no gaps, no repeats")

	full := page("per_page=60&after=" + vis[89].ID)
	assert.Len(t, full.Order, 60)
	assert.Empty(t, full.NextPostID, "a full last page: nothing newer, empty next_post_id")

	unknown := page("per_page=60&after=nosuchpost")
	assert.Empty(t, unknown.Order, "an unknown cursor: 200 with an empty page")
	assert.Equal(t, "nosuchpost", unknown.PrevPostID, "…whose prev_post_id still echoes the cursor")
	assert.Empty(t, unknown.NextPostID)

	// before=: next_post_id is the cursor, prev_post_id the next older post
	// or empty at the start of the channel.
	b := page("per_page=60&before=" + vis[149].ID)
	assert.Equal(t, vis[148].ID, b.Order[0])
	assert.Equal(t, vis[89].ID, b.Order[59])
	assert.Equal(t, vis[149].ID, b.NextPostID)
	assert.Equal(t, vis[88].ID, b.PrevPostID)
	b = page("per_page=60&before=" + vis[60].ID)
	assert.Len(t, b.Order, 60)
	assert.Empty(t, b.PrevPostID, "a full first page: nothing older")
	assert.Empty(t, page("per_page=60&before=nosuchpost").Order)

	// since= wins over after=, after= over before= (api4/post.go getPostsForChannel).
	l := page("per_page=5&after=" + vis[10].ID + "&before=" + vis[149].ID)
	assert.Equal(t, vis[11].ID, l.Order[4], "after= wins over before=")
	l = page(fmt.Sprintf("since=%d&after=%s", vis[148].UpdateAt, vis[10].ID))
	assert.Equal(t, []string{vis[149].ID}, l.Order, "since= wins over after=")
	assert.Empty(t, l.PrevPostID)
	assert.Empty(t, l.NextPostID)

	// A deleted cursor still pages by its create_at (the server's subquery
	// reads the row whatever its delete_at).
	s.DeleteAs(vis[100].ID)
	d := page("per_page=10&after=" + vis[100].ID)
	assert.Equal(t, vis[101].ID, d.Order[9])
	d = page("per_page=10&before=" + vis[100].ID)
	assert.Equal(t, vis[99].ID, d.Order[0])

}

func indexOf(ps []model.Post, id string) int {
	for i, p := range ps {
		if p.ID == id {
			return i
		}
	}
	return -1
}

func TestChannelPostsAfterUnderCRTSkipsReplies(t *testing.T) {
	s := Start(Options{CRT: true})
	defer s.Close()
	a := loginAs(t, s, "alice")
	vis := s.VisiblePosts("c-town")
	root := s.PostAs("c-town", "bob", "root")
	s.ReplyAs("c-town", root.ID, "carol", "reply")
	newer := s.PostAs("c-town", "bob", "newer root")

	var l model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/channels/c-town/posts?page=0&per_page=60&collapsedThreads=true&after="+vis[149].ID, nil, &l))
	assert.Equal(t, []string{newer.ID, root.ID}, l.Order)
	require.Equal(t, 200, a.call("GET", "/api/v4/channels/c-town/posts?page=0&per_page=1&collapsedThreads=true&after="+vis[149].ID, nil, &l))
	assert.Equal(t, []string{root.ID}, l.Order)
	assert.Equal(t, newer.ID, l.NextPostID, "the next root, not the reply")
}

func TestGetPostByID(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	b := loginAs(t, s, "bob")
	c := loginAs(t, s, "carol")

	p := s.PostAs("c-town", "bob", "fetch me")
	var got model.Post
	require.Equal(t, 200, a.call("GET", "/api/v4/posts/"+p.ID, nil, &got))
	assert.Equal(t, "fetch me", got.Message)
	assert.Equal(t, "c-town", got.ChannelID)

	s.EditAs(p.ID, "fetch me, edited")
	require.Equal(t, 200, a.call("GET", "/api/v4/posts/"+p.ID, nil, &got))
	assert.Equal(t, "fetch me, edited", got.Message)
	s.mu.Lock()
	var histID string
	for _, q := range s.chat.posts["c-town"] {
		if q.OriginalID == p.ID {
			histID = q.ID
		}
	}
	s.mu.Unlock()
	require.NotEmpty(t, histID)
	st, id := a.callErr("GET", "/api/v4/posts/"+histID, nil)
	assert.Equal(t, 404, st, "an edit-history row is deleted")
	assert.Equal(t, "app.post.get.app_error", id)

	s.DeleteAs(p.ID)
	st, _ = a.callErr("GET", "/api/v4/posts/"+p.ID, nil)
	assert.Equal(t, 404, st, "deleted")
	st, _ = a.callErr("GET", "/api/v4/posts/nosuchpost", nil)
	assert.Equal(t, 404, st, "unknown")

	offices := s.PostAs("c-offices", "bob", "public, alice is not in it")
	assert.Equal(t, 200, a.call("GET", "/api/v4/posts/"+offices.ID, nil, &got),
		"an open channel of her team: readable without membership (GetPostIfAuthorized)")

	secret := s.PostAs("c-secret", "alice", "private")
	st, _ = b.callErr("GET", "/api/v4/posts/"+secret.ID, nil)
	assert.Equal(t, 403, st, "a private channel she is not in")
	dm := s.PostAs("c-dm-bob", "bob", "dm")
	st, _ = c.callErr("GET", "/api/v4/posts/"+dm.ID, nil)
	assert.Equal(t, 403, st, "someone else's DM")
}

func TestThreadDown(t *testing.T) {
	for _, crt := range []bool{false, true} {
		t.Run(fmt.Sprintf("crt=%v", crt), func(t *testing.T) {
			s := Start(Options{CRT: crt})
			defer s.Close()
			a := loginAs(t, s, "alice")
			rootID := s.SeedThread("c-town", "alice", 10)
			s.mu.Lock()
			replies := s.repliesLocked(rootID) // oldest first
			s.mu.Unlock()
			require.Len(t, replies, 10)

			thread := func(q string) (order []string, hasNext bool) {
				t.Helper()
				var l model.PostList
				require.Equal(t, 200, a.call("GET", fmt.Sprintf("/api/v4/posts/%s/thread?collapsedThreads=%v&%s", rootID, crt, q), nil, &l))
				require.NotNil(t, l.HasNext)
				return l.Order, *l.HasNext
			}
			r := func(i int) string { return replies[i].ID }

			order, next := thread(fmt.Sprintf("perPage=3&direction=down&fromCreateAt=%d&fromPost=%s", replies[1].CreateAt, r(1)))
			assert.Equal(t, []string{rootID, r(2), r(3), r(4)}, order, "root first, then newer replies oldest first")
			assert.True(t, next, "newer replies exist")

			order, next = thread(fmt.Sprintf("perPage=3&direction=down&fromCreateAt=%d", replies[1].CreateAt))
			assert.Equal(t, []string{rootID, r(2), r(3), r(4)}, order, "fromCreateAt alone")
			assert.True(t, next)

			order, next = thread(fmt.Sprintf("perPage=3&direction=down&fromCreateAt=%d&fromPost=%s", replies[6].CreateAt, r(6)))
			assert.Equal(t, []string{rootID, r(7), r(8), r(9)}, order)
			assert.False(t, next, "exactly the rest: nothing newer")

			order, next = thread(fmt.Sprintf("perPage=3&direction=down&fromCreateAt=%d&fromPost=%s", replies[9].CreateAt, r(9)))
			assert.Equal(t, []string{rootID}, order, "past the newest: the root alone")
			assert.False(t, next)

			order, next = thread(fmt.Sprintf("perPage=3&direction=up&fromCreateAt=%d&fromPost=%s", replies[5].CreateAt, r(5)))
			assert.Equal(t, []string{rootID, r(4), r(3), r(2)}, order, "up is unchanged: older replies, newest first")
			assert.True(t, next)
		})
	}
}
