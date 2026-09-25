package mmsync

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

func reactionOf(h *harness, postID, emoji string) state.ReactionView {
	for _, p := range h.view("c-offtopic").Posts {
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

func serverHas(h *harness, postID, user, emoji string) bool {
	return slices.ContainsFunc(h.fake.Reactions(postID), func(r model.Reaction) bool { return r.UserID == user && r.EmojiName == emoji })
}

func welcomeHarness(t *testing.T) (*harness, string) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	return h, h.fake.FindPost("c-offtopic", "Welcome to off-topic")
}

func TestReactionRoundTrip(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	require.NoError(t, h.w.React(ctx, id, "+1", true))
	assert.Equal(t, state.ReactionView{Emoji: "+1", Count: 3, Mine: true}, reactionOf(h, id, "+1"))
	assert.True(t, serverHas(h, id, "u-alice", "+1"))
	h.eventually(func() bool {
		return strings.Contains(h.fake.Preference("alice", "recent_emojis", "u-alice"), `"name":"+1"`)
	}, "recent emoji not saved")

	require.NoError(t, h.w.React(ctx, id, "+1", false))
	assert.Equal(t, state.ReactionView{Emoji: "+1", Count: 2}, reactionOf(h, id, "+1"))
	assert.False(t, serverHas(h, id, "u-alice", "+1"))
}

func TestReactionRefusedIsRolledBack(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.SetFailure("/api/v4/reactions", 403)
	err := h.w.React(ctx, id, "tada", true)
	var re *rest.Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, 403, re.Status)
	assert.Equal(t, state.ReactionView{Emoji: "tada", Count: 1}, reactionOf(h, id, "tada"), "rolled back")
	assert.Equal(t, StatusLive, h.w.Status(), "a refused reaction is not a dead session")
	assert.ErrorIs(t, h.w.React(ctx, "nope", "tada", true), ErrNoPost)
	assert.ErrorIs(t, h.w.React(ctx, id, "bad name", true), ErrBadEmoji)
}

func TestReactionClicksWhileInFlightAreQueuedLastWins(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.SetLatency("/api/v4/reactions", 300*time.Millisecond)
	del := "/api/v4/users/u-alice/posts/" + id + "/reactions/"

	done := make(chan error, 1)
	go func() { done <- h.w.React(ctx, id, "fire", true) }()
	h.eventually(func() bool { return reactionOf(h, id, "fire").Mine }, "not applied at once")
	require.NoError(t, h.w.React(ctx, id, "fire", false), "a click while the add is in flight returns at once")
	assert.False(t, reactionOf(h, id, "fire").Mine, "…and shows at once")
	require.NoError(t, <-done)
	assert.False(t, serverHas(h, id, "u-alice", "fire"), "the last click was sent after the first")
	assert.Equal(t, 1, h.fake.Hits("DELETE", del+"fire"))

	go func() { done <- h.w.React(ctx, id, "rocket", true) }()
	h.eventually(func() bool { return reactionOf(h, id, "rocket").Mine }, "not applied at once")
	require.NoError(t, h.w.React(ctx, id, "rocket", false))
	require.NoError(t, h.w.React(ctx, id, "rocket", true))
	require.NoError(t, <-done)
	assert.True(t, serverHas(h, id, "u-alice", "rocket"))
	assert.Zero(t, h.fake.Hits("DELETE", del+"rocket"), "add, remove, add: nothing more to send")
}

func TestOthersReactionsArriveLive(t *testing.T) {
	h, id := welcomeHarness(t)
	h.fake.ReactAs("bob", id, "rocket") // a member of Off-Topic (carol is not)
	h.eventually(func() bool { return reactionOf(h, id, "rocket") == state.ReactionView{Emoji: "rocket", Count: 1} }, "live reaction")
}

func TestReactionQueuedClickRefusedRollsBackOnlyItself(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.SetLatency("/api/v4/reactions", 300*time.Millisecond)
	h.fake.SetFailure("/reactions/fire", 403) // the DELETE only

	done := make(chan error, 1)
	go func() { done <- h.w.React(ctx, id, "fire", true) }()
	h.eventually(func() bool { return reactionOf(h, id, "fire").Mine }, "not applied at once")
	require.NoError(t, h.w.React(ctx, id, "fire", false), "queued behind the add")
	var re *rest.Error
	require.ErrorAs(t, <-done, &re, "the queued remove was refused")
	assert.True(t, serverHas(h, id, "u-alice", "fire"), "the add went through")
	assert.Equal(t, state.ReactionView{Emoji: "fire", Count: 1, Mine: true}, reactionOf(h, id, "fire"),
		"only the refused remove is rolled back")
}

func TestReactionConcurrentClicksConverge(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.SetLatency("/api/v4/reactions", 20*time.Millisecond)
	for round := 0; round < 5; round++ {
		errs := make(chan error, 16)
		for i := 0; i < 16; i++ {
			go func() { errs <- h.w.React(ctx, id, "rocket", (i+round)%2 == 0) }()
		}
		for i := 0; i < 16; i++ {
			require.NoError(t, <-errs)
		}
		mine := reactionOf(h, id, "rocket").Mine
		assert.Equal(t, mine, serverHas(h, id, "u-alice", "rocket"), "round %d: the last click shown is the one sent", round)
		h.eventually(func() bool { return reactionOf(h, id, "rocket").Mine == serverHas(h, id, "u-alice", "rocket") }, "echoes diverged")
	}
	h.w.reactMu.Lock()
	assert.Empty(t, h.w.reactWant, "no pair left in flight")
	h.w.reactMu.Unlock()
}

func TestReactionLostReplyIsReconciledFromTheServer(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.BreakReplies("/api/v4/reactions", true) // the server applies it, the reply is lost
	require.NoError(t, h.w.React(ctx, id, "fire", true), "the server has what was asked")
	assert.True(t, serverHas(h, id, "u-alice", "fire"))
	assert.Equal(t, state.ReactionView{Emoji: "fire", Count: 1, Mine: true}, reactionOf(h, id, "fire"))
	assert.Equal(t, 1, h.fake.Hits("GET", "/api/v4/posts/"+id+"/reactions"), "reconciled")
	time.Sleep(100 * time.Millisecond) // the echo has come (it beat the lost reply) and changed nothing
	assert.True(t, reactionOf(h, id, "fire").Mine)
	h.eventually(func() bool {
		return strings.Contains(h.fake.Preference("alice", "recent_emojis", "u-alice"), `"name":"fire"`)
	}, "recent emoji not saved")
}

func TestReactionTimeoutNotAppliedIsRolledBack(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.tune = func(c *Config) { c.CallTimeout = 150 * time.Millisecond }
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	id := h.fake.FindPost("c-offtopic", "Welcome to off-topic")
	h.fake.SetLatency("/api/v4/reactions", time.Second) // times out before the server applies it
	err := h.w.React(context.Background(), id, "fire", true)
	var re *rest.Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, rest.KindNetwork, re.Kind)
	assert.False(t, serverHas(h, id, "u-alice", "fire"))
	assert.Equal(t, state.ReactionView{}, reactionOf(h, id, "fire"), "rolled back to what the server has")
	assert.Equal(t, 1, h.fake.Hits("GET", "/api/v4/posts/"+id+"/reactions"), "reconciled")
}

func TestReactionQueuedRequestOutlivesTheFirstCaller(t *testing.T) {
	h, id := welcomeHarness(t)
	h.fake.SetLatency("/api/v4/reactions", 300*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.w.React(ctx, id, "fire", true) }()
	h.eventually(func() bool { return reactionOf(h, id, "fire").Mine }, "not applied at once")
	cancel() // the page that clicked went away
	require.NoError(t, <-done)
	assert.True(t, serverHas(h, id, "u-alice", "fire"))
	assert.True(t, reactionOf(h, id, "fire").Mine)
}

func TestReactionOnItsOwnStateSendsNothing(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	require.NoError(t, h.w.React(ctx, id, "tada", false), "not ours: nothing to remove")
	assert.Zero(t, h.fake.Hits("DELETE", "/api/v4/users/u-alice/posts/"+id+"/reactions/tada"))
	h.fake.ReactAs("alice", id, "tada") // from another device: must not be taken for a stale echo
	h.eventually(func() bool { return reactionOf(h, id, "tada").Mine }, "the intent of the unsent click was not dropped")
}
