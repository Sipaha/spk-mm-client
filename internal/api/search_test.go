package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mmsync"
)

func TestSearchThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "live")

	page, err := f.svc.SearchPosts(ctx, id, "t-fake", "  Message  ", 0, 3*3600)
	require.NoError(t, err)
	assert.Len(t, page.Hits, 20)
	assert.True(t, page.HasNext)
	assert.Equal(t, "c-town", page.Hits[0].ChannelID)
	assert.True(t, page.Hits[0].Jumpable)
	assert.Equal(t, "Message", fake.SearchCalls()[0].Terms, "terms trimmed")

	res, err := f.svc.SearchSuggest(ctx, id, "t-fake", "users", "ca")
	require.NoError(t, err)
	require.Len(t, res.Users, 1)
	assert.Equal(t, "carol", res.Users[0].Username)

	res, err = f.svc.SearchSuggest(ctx, id, "t-fake", "channels", "@b")
	require.NoError(t, err)
	var names []string
	for _, c := range res.Channels {
		names = append(names, c.Name)
	}
	assert.Equal(t, []string{"@bob", "@alice,bob,carol"}, names)
}

func TestSearchValidatesItsInput(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" }, "live")

	bad := []struct {
		name   string
		team   string
		terms  string
		page   int
		offset int
	}{
		{"team not an id", "t/1", "x", 0, 0},
		{"another server's team", "t-other", "x", 0, 0},
		{"empty terms", "t-fake", "", 0, 0},
		{"blank terms", "t-fake", " \t\n", 0, 0},
		{"terms over 1000 bytes", "t-fake", strings.Repeat("я", 501), 0, 0},
		{"invalid UTF-8", "t-fake", "\xff", 0, 0},
		{"negative page", "t-fake", "x", -1, 0},
		{"page past the limit", "t-fake", "x", mmsync.SearchMaxPages, 0},
		{"time zone out of range", "t-fake", "x", 0, 15 * 3600},
	}
	for _, c := range bad {
		_, err := f.svc.SearchPosts(ctx, id, c.team, c.terms, c.page, c.offset)
		assert.Equal(t, CodeInvalidArgument, codeOf(err), c.name)
	}
	assert.Empty(t, fake.SearchCalls(), "nothing asked")
	_, err := f.svc.SearchPosts(ctx, id, "t-fake", strings.Repeat("a", MaxSearchTerms), mmsync.SearchMaxPages-1, -12*3600)
	require.NoError(t, err, "the longest terms, the last page")
	_, err = f.svc.SearchPosts(ctx, 999, "t-fake", "x", 0, 0)
	assert.Error(t, err)

	before := fake.Hits("GET", "/api/v4/users/autocomplete")
	_, err = f.svc.SearchSuggest(ctx, id, "t-fake", "emoji", "b")
	assert.Equal(t, CodeInvalidArgument, codeOf(err), "unknown kind")
	_, err = f.svc.SearchSuggest(ctx, id, "t/1", "users", "b")
	assert.Equal(t, CodeInvalidArgument, codeOf(err), "team not an id")
	_, err = f.svc.SearchSuggest(ctx, id, "t-other", "users", "b c")
	assert.Equal(t, CodeInvalidArgument, codeOf(err), "another server's team, even with nothing to suggest")
	for _, p := range []string{strings.Repeat("b", MaxAutocompletePrefix+1), "b c", "\xff"} {
		res, err := f.svc.SearchSuggest(ctx, id, "t-fake", "users", p)
		require.NoError(t, err, "%q", p)
		assert.Empty(t, res.Users, "%q: nothing to suggest, nothing asked", p)
	}
	assert.Equal(t, before, fake.Hits("GET", "/api/v4/users/autocomplete"))
}

func TestSearchErrorCodes(t *testing.T) {
	for err, code := range map[error]string{
		mmsync.ErrOffline:                            CodeOffline,
		mmsync.ErrUnknownTeam:                        CodeInvalidArgument,
		mmsync.ErrSearchPage:                         CodeInvalidArgument,
		mmsync.ErrSuggestKind:                        CodeInvalidArgument,
		context.Canceled:                             CodeCancelled,
		&rest.Error{Status: http.StatusUnauthorized}: CodeSessionExpired,
		&rest.Error{Status: http.StatusForbidden}:    CodeForbidden,
		errors.New("boom"):                           CodeInternal,
	} {
		assert.Equal(t, code, codeOf(searchError(err)), fmt.Sprint(err))
	}
	assert.NoError(t, searchError(nil))
}
