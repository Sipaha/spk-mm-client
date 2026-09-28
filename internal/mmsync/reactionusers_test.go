package mmsync

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

// townHarness: c-offtopic has no bob/carol members (AGENTS.md — the fake's
// ...As helpers panic on a non-member), so reactor tests use c-town, which
// does, via a fresh post of our own.
func townHarness(t *testing.T) (*harness, string) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	p := h.fake.PostAs("c-town", "alice", "reactor test post")
	h.eventually(func() bool { return h.hasMessage("c-town", "reactor test post") }, "post did not land")
	return h, p.ID
}

func reactorReactionOf(h *harness, ch, postID, emoji string) state.ReactionView {
	for _, p := range h.view(ch).Posts {
		if p.ID == postID {
			for _, r := range p.Reactions {
				if r.Emoji == emoji {
					return r
				}
			}
		}
	}
	return state.ReactionView{}
}

// TestReactionUsersFromStateOrderedExcludingMe: names come from the
// reactions the post's held copy already carries (in reaction order),
// never including me — the UI adds "You" itself. bob and carol are
// already known by this point (the seed's default dataset has them author
// other posts, loaded at bootstrap), so this also covers the
// already-known path: no ambiguity about whether a fetch happened.
func TestReactionUsersFromStateOrderedExcludingMe(t *testing.T) {
	h, id := townHarness(t)
	ctx := context.Background()
	require.NoError(t, h.w.React(ctx, id, "+1", true)) // me, first — must not appear
	h.fake.ReactAs("bob", id, "+1")
	h.fake.ReactAs("carol", id, "+1")
	h.eventually(func() bool { return reactorReactionOf(h, "c-town", id, "+1").Count == 3 }, "reactions did not land")

	v, err := h.w.ReactionUsers(ctx, id, "+1")
	require.NoError(t, err)
	require.Len(t, v.Users, 2)
	assert.Equal(t, "u-bob", v.Users[0].ID)
	assert.Equal(t, "bob", v.Users[0].Name)
	assert.Equal(t, "u-carol", v.Users[1].ID)
	assert.Equal(t, "carol", v.Users[1].Name)
	assert.Equal(t, 0, v.Unknown)
}

// TestReactionUsersFetchesMissingProfilesInOneBatch: two ids state has
// never seen (ReactAsUnknown — outside the fake's user directory, so they
// can never actually resolve) still cost exactly one POST users/ids, not
// one call per id — the batching is what's under test here, not whether
// the fetch succeeds (see AGENTS.md: this fake's default dataset makes
// bob/carol/alice profiles known from bootstrap on, via post authorship
// elsewhere, so there is no *real* account left unknown to react with).
func TestReactionUsersFetchesMissingProfilesInOneBatch(t *testing.T) {
	h, id := townHarness(t)
	ctx := context.Background()
	h.fake.ReactAsUnknown("u-x1", "c-town", id, "+1")
	h.fake.ReactAsUnknown("u-x2", "c-town", id, "+1")
	h.eventually(func() bool { return reactorReactionOf(h, "c-town", id, "+1").Count == 2 }, "reactions did not land")
	require.ElementsMatch(t, []string{"u-x1", "u-x2"}, h.w.State().MissingAmong([]string{"u-x1", "u-x2"}))

	before := h.fake.Hits("POST", "/api/v4/users/ids")
	v, err := h.w.ReactionUsers(ctx, id, "+1")
	require.NoError(t, err)
	assert.Equal(t, before+1, h.fake.Hits("POST", "/api/v4/users/ids"), "one batch, not one call per id")
	require.Len(t, v.Users, 2)
	assert.Equal(t, []string{"u-x1", "u-x2"}, []string{v.Users[0].ID, v.Users[1].ID})
	assert.Equal(t, 2, v.Unknown, "outside the directory — the fake genuinely cannot resolve them")
}

// TestReactionUsersFetchFailureReturnsPartial: a failed profile fetch
// never blocks the caller and never disturbs names already known — only
// the still-missing id(s) come back empty, counted in Unknown, so the UI
// can say "and N others" and move on.
func TestReactionUsersFetchFailureReturnsPartial(t *testing.T) {
	h, id := townHarness(t)
	ctx := context.Background()
	h.fake.ReactAs("bob", id, "+1")                   // already known — must survive the failure below untouched
	h.fake.ReactAsUnknown("u-x1", "c-town", id, "+1") // still missing — its fetch will fail
	h.eventually(func() bool { return reactorReactionOf(h, "c-town", id, "+1").Count == 2 }, "reactions did not land")

	h.fake.SetFailure("/api/v4/users/ids", 500)
	v, err := h.w.ReactionUsers(ctx, id, "+1")
	require.NoError(t, err, "a fetch failure is not itself an error — partial results still answer the caller")
	require.Len(t, v.Users, 2)
	assert.Equal(t, "bob", v.Users[0].Name, "known before the failing fetch — unaffected by it")
	assert.Empty(t, v.Users[1].Name)
	assert.Equal(t, "u-x1", v.Users[1].ID)
	assert.Equal(t, 1, v.Unknown)
	assert.Equal(t, StatusLive, h.w.Status(), "a 500 is not a dead session")
}

func TestReactionUsersNoPost(t *testing.T) {
	h, _ := townHarness(t)
	_, err := h.w.ReactionUsers(context.Background(), "no-such-post", "+1")
	assert.ErrorIs(t, err, ErrNoPost)
}

// TestReactionUsersReadsFromStateEvenNeedsReauth: once a profile is
// cached, a later call resolves it from state alone and succeeds even
// after the session has gone needs_reauth — a read may still work offline
// from state, unlike the write actions in actions.go (s.writer() in
// api/chat.go fails those fast instead). A call that still needs a
// network fetch behaves like the 500 case above: an expired session (401)
// on that fetch signals re-auth the same way, tested here by asserting the
// status transition itself.
func TestReactionUsersReadsFromStateEvenNeedsReauth(t *testing.T) {
	h, id := townHarness(t)
	ctx := context.Background()
	h.fake.ReactAs("bob", id, "+1")
	h.eventually(func() bool { return reactorReactionOf(h, "c-town", id, "+1").Count == 1 }, "reaction did not land")
	_, err := h.w.ReactionUsers(ctx, id, "+1") // bob is already known — nothing to fetch
	require.NoError(t, err)

	h.fake.SetFailure("/api/v4/users/me", http.StatusUnauthorized)
	h.fake.RevokeAll()
	h.eventually(func() bool { return h.w.Status() == StatusNeedsReauth }, "should need re-auth")

	v, err := h.w.ReactionUsers(ctx, id, "+1")
	require.NoError(t, err)
	assert.Equal(t, "bob", v.Users[0].Name)
}
