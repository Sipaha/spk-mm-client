package mmsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mmfake"
)

func hitIDs(hs []SearchHit) []string {
	out := []string{}
	for _, h := range hs {
		out = append(out, h.ID)
	}
	return out
}

// A hit is the post as the feed shows it — its author's profile loaded
// even when no window holds one of their posts — plus its channel named
// the way in: takes it.
func TestSearchReturnsHitsWithChannelAndAuthor(t *testing.T) {
	h := newHarness(t, mmfake.Options{ExtraUsers: 1})
	old := h.fake.PostAs("c-town", "dave", "needle from dave")
	h.fake.SetCreateAt(old.ID, 1000) // far below the window: dave is never loaded
	dm := h.fake.PostAs("c-dm-bob", "bob", "needle in a DM")
	gm := h.fake.PostAs("c-gm", "carol", "needle in a GM")
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	require.Equal(t, []string{"u-dave"}, h.w.State().MissingAmong([]string{"u-dave", "u-bob"}))

	page, err := h.w.SearchPosts(context.Background(), "t-fake", "needle", 0, 3600)
	require.NoError(t, err)
	assert.False(t, page.HasNext)
	assert.False(t, page.LimitReached)
	require.Equal(t, []string{gm.ID, dm.ID, old.ID}, hitIDs(page.Hits), "newest first, as the server orders them")

	g, d, o := page.Hits[0], page.Hits[1], page.Hits[2]
	assert.Equal(t, "dave", o.Author, "the missing author was loaded")
	assert.NotEmpty(t, o.Avatar)
	assert.Equal(t, "needle from dave", o.Message)
	assert.Equal(t, "c-town", o.ChannelID)
	assert.Equal(t, "town-square", o.ChannelName)
	assert.Equal(t, "Town Square", o.ChannelDisplay)
	assert.Equal(t, "O", o.ChannelType)
	assert.True(t, o.Jumpable)
	assert.Equal(t, []string{}, o.Matches, "Bleve sends matches {}")

	assert.Equal(t, "bob", d.Author)
	assert.Equal(t, "bob", d.ChannelName, "a DM is in:@<partner>")
	assert.Equal(t, "D", d.ChannelType)
	assert.NotEmpty(t, d.ChannelDisplay)
	assert.True(t, d.Jumpable)

	assert.Equal(t, "alice,bob,carol", g.ChannelName, "a GM is in:@<all members, me too>")
	assert.Equal(t, "alice, bob, carol", g.ChannelDisplay)
	assert.Equal(t, "G", g.ChannelType)

	calls := h.fake.SearchCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, mmfake.SearchCall{TeamID: "t-fake", UserID: "u-alice", Terms: "needle", Page: 0, PerPage: 20}, calls[0])
}

// has_next is the raw page's length ≥ per_page, on both engines.
func TestSearchHasNextByRawLength(t *testing.T) {
	ctx := context.Background()
	t.Run("bleve", func(t *testing.T) {
		h := liveHarness(t, mmfake.Options{}) // 150 "Message #N" in c-town
		p0, err := h.w.SearchPosts(ctx, "t-fake", "Message", 0, 0)
		require.NoError(t, err)
		assert.Len(t, p0.Hits, 20)
		assert.True(t, p0.HasNext)
		p1, err := h.w.SearchPosts(ctx, "t-fake", "Message", 1, 0)
		require.NoError(t, err)
		assert.NotEqual(t, p0.Hits[0].ID, p1.Hits[0].ID, "an offset page")
		last, err := h.w.SearchPosts(ctx, "t-fake", "Message", 7, 0)
		require.NoError(t, err)
		assert.Len(t, last.Hits, 10)
		assert.False(t, last.HasNext, "a short page is the end")
	})
	t.Run("sql", func(t *testing.T) {
		h := liveHarness(t, mmfake.Options{SearchSQLEngine: true})
		p0, err := h.w.SearchPosts(ctx, "t-fake", "Message", 0, 0)
		require.NoError(t, err)
		assert.Len(t, p0.Hits, 100, "the database engine ignores per_page")
		assert.True(t, p0.HasNext)
		p1, err := h.w.SearchPosts(ctx, "t-fake", "Message", 1, 0)
		require.NoError(t, err)
		assert.Empty(t, p1.Hits)
		assert.False(t, p1.HasNext, "page 1 is empty: the end")
	})
}

// Page 24 is the last one asked for (≤ 500 hits a session): more there is
// "limit reached", not "more".
func TestSearchLimitReachedOnLastPage(t *testing.T) {
	h := liveHarness(t, mmfake.Options{SeedPosts: 520})
	ctx := context.Background()
	p, err := h.w.SearchPosts(ctx, "t-fake", "Message", SearchMaxPages-2, 0)
	require.NoError(t, err)
	assert.True(t, p.HasNext)
	assert.False(t, p.LimitReached)
	p, err = h.w.SearchPosts(ctx, "t-fake", "Message", SearchMaxPages-1, 0)
	require.NoError(t, err)
	assert.Len(t, p.Hits, 20)
	assert.False(t, p.HasNext)
	assert.True(t, p.LimitReached)
	_, err = h.w.SearchPosts(ctx, "t-fake", "Message", SearchMaxPages, 0)
	require.ErrorIs(t, err, ErrSearchPage)
	_, err = h.w.SearchPosts(ctx, "t-fake", "Message", -1, 0)
	require.ErrorIs(t, err, ErrSearchPage)
	assert.Len(t, h.fake.SearchCalls(), 2, "an out-of-range page is never asked")
}

// matches (Elasticsearch) are per post id: each hit carries its own words.
func TestSearchMatchesBelongToTheirHit(t *testing.T) {
	h := liveHarness(t, mmfake.Options{SearchMatches: true})
	a := h.fake.PostAs("c-town", "bob", "Alpha first")
	b := h.fake.PostAs("c-town", "carol", "then ALPHA again")
	page, err := h.w.SearchPosts(context.Background(), "t-fake", "alpha", 0, 0)
	require.NoError(t, err)
	require.Equal(t, []string{b.ID, a.ID}, hitIDs(page.Hits))
	assert.Equal(t, []string{"ALPHA"}, page.Hits[0].Matches)
	assert.Equal(t, []string{"Alpha"}, page.Hits[1].Matches)
}

// A hit in a channel the state does not hold (its metadata may lag) shows,
// but cannot be jumped to.
func TestSearchUnknownChannelIsNotJumpable(t *testing.T) {
	hook := func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "/posts/search") {
			return false
		}
		p := model.Post{ID: "p-ghost", ChannelID: "c-ghost", UserID: "u-bob", Message: "boo", CreateAt: 5}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.PostSearchResults{
			PostList: model.PostList{Order: []string{p.ID}, Posts: map[string]model.Post{p.ID: p}},
			Matches:  map[string][]string{},
		})
		return true
	}
	h := liveHarness(t, mmfake.Options{RequestHook: hook})
	page, err := h.w.SearchPosts(context.Background(), "t-fake", "boo", 0, 0)
	require.NoError(t, err)
	require.Len(t, page.Hits, 1)
	hit := page.Hits[0]
	assert.Equal(t, "c-ghost", hit.ChannelID)
	assert.False(t, hit.Jumpable)
	assert.Empty(t, hit.ChannelDisplay)
	assert.Empty(t, hit.ChannelName)
	assert.Equal(t, "bob", hit.Author)
}

func TestSearchExpiredSessionSignalsAuth(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	h.fake.SetFailure("/posts/search", http.StatusUnauthorized)
	_, err := h.w.SearchPosts(context.Background(), "t-fake", "Message", 0, 0)
	require.Error(t, err)
	h.eventually(func() bool { return h.w.Status() == StatusNeedsReauth }, "a search's 401 did not signal auth")
}

func TestSearchOfflineAsksNothing(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	h.fake.SetDown(true)
	h.w.Nudge()
	h.eventually(func() bool { return h.w.Status() != StatusLive }, "never went offline")
	_, err := h.w.SearchPosts(context.Background(), "t-fake", "Message", 0, 0)
	require.ErrorIs(t, err, ErrOffline)
	assert.Empty(t, h.fake.SearchCalls())
	// Suggestions: nothing asked; our DMs/GMs still come from the state.
	before := h.fake.Hits("GET", "/api/v4/teams/t-fake/channels/autocomplete")
	res, err := h.w.SearchSuggest(context.Background(), "t-fake", ACChannels, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"@bob", "@alice,bob,carol"}, acChannelNames(res.Channels))
	assert.Equal(t, before, h.fake.Hits("GET", "/api/v4/teams/t-fake/channels/autocomplete"))
}

func TestSearchOfAnotherTeamIsRefused(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	_, err := h.w.SearchPosts(context.Background(), "t-other", "Message", 0, 0)
	require.ErrorIs(t, err, ErrUnknownTeam)
	_, err = h.w.SearchSuggest(context.Background(), "t-other", ACUsers, "b")
	require.ErrorIs(t, err, ErrUnknownTeam)
	assert.Empty(t, h.fake.SearchCalls())
}

func TestSearchIsCancelledWithItsContext(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	h.fake.SetLatency("/posts/search", 5_000_000_000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.w.SearchPosts(ctx, "t-fake", "Message", 0, 0)
	require.True(t, errors.Is(err, context.Canceled), "got %v", err)
}

func acChannelNames(cs []ACChannel) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

// Suggestions for from:/in: are the team's, whatever channel is open (a
// DM here): from: offers every team member; in: the team's channels by
// slug, DMs as @username and GMs as @a,b,c (all members, me too).
func TestSearchSuggestIsTeamScoped(t *testing.T) {
	h := liveHarness(t, mmfake.Options{})
	_, ok := h.w.OpenChannel("c-dm-bob")
	require.True(t, ok)
	ctx := context.Background()

	res, err := h.w.SearchSuggest(ctx, "t-fake", ACUsers, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"alice", "bob", "carol"}, append(acNames(res.Users), acNames(res.Others)...),
		"carol is not in the open DM, yet offered")

	res, err = h.w.SearchSuggest(ctx, "t-fake", ACChannels, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"off-topic", "secret", "town-square", "offices", "@bob", "@alice,bob,carol"}, acChannelNames(res.Channels))
	dm := res.Channels[4]
	assert.Equal(t, ACChannel{ID: "c-dm-bob", Name: "@bob", DisplayName: dm.DisplayName, Type: "D", Joined: true}, dm)

	res, err = h.w.SearchSuggest(ctx, "t-fake", ACChannels, "bo")
	require.NoError(t, err)
	assert.Equal(t, []string{"@bob", "@alice,bob,carol"}, acChannelNames(res.Channels), "a member's name finds the GM")

	res, err = h.w.SearchSuggest(ctx, "t-fake", ACChannels, "@alice,b")
	require.NoError(t, err)
	assert.Equal(t, []string{"@alice,bob,carol"}, acChannelNames(res.Channels))

	res, err = h.w.SearchSuggest(ctx, "t-fake", ACChannels, "~off")
	require.NoError(t, err)
	assert.Equal(t, []string{"off-topic", "offices"}, acChannelNames(res.Channels))

	_, err = h.w.SearchSuggest(ctx, "t-fake", ACEmoji, "")
	require.ErrorIs(t, err, ErrSuggestKind)
}
