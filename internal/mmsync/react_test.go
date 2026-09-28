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

// TestReactionBumpsRecentEmojiWhenIssued: the local recent-emoji list (what
// the UI's quick reactions read — EmojiInfo/RecentEmojis) moves the moment
// an add is *issued*, before the SaveReaction round trip returns — not only
// once it lands. The final-review finding on e63449f..e9dbc29: a fetch of
// the panel right after this click used to still see the *previous* list,
// because the local bump only happened once SaveReaction had already
// returned. Saving the preference to the server stays gated on the reaction
// actually landing, unchanged — checked here too.
func TestReactionBumpsRecentEmojiWhenIssued(t *testing.T) {
	h, id := welcomeHarness(t)
	ctx := context.Background()
	h.fake.SetLatency("/api/v4/reactions", 300*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- h.w.React(ctx, id, "tada", true) }()

	h.eventually(func() bool { return slices.Contains(h.w.State().RecentEmojis(), "tada") }, "not bumped at issue time")
	select {
	case <-done:
		t.Fatal("React already returned — this does not prove the bump happened before the network call did")
	default: // good: SaveReaction is still in flight (300ms latency), the local bump already landed
	}
	assert.NotContains(t, h.fake.Preference("alice", "recent_emojis", "u-alice"), `"name":"tada"`, "the server-side preference save must still wait for the add to land")

	require.NoError(t, <-done)
	h.eventually(func() bool {
		return strings.Contains(h.fake.Preference("alice", "recent_emojis", "u-alice"), `"name":"tada"`)
	}, "recent emoji not saved once the add landed")
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
	assert.Empty(t, h.pairs(), "no pair left in flight")
}

// retryHarness: a live worker with short reaction retry backoffs.
func retryHarness(t *testing.T, backoff ...time.Duration) (*harness, string) {
	h := newHarness(t, mmfake.Options{})
	h.tune = func(c *Config) { c.CallTimeout, c.reactBackoff = 150*time.Millisecond, backoff }
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	return h, h.fake.FindPost("c-offtopic", "Welcome to off-topic")
}

func TestReactionNetworkFailureIsRetriedKeepingTheClick(t *testing.T) {
	h, id := retryHarness(t, 300*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond)
	h.fake.SetLatency("/api/v4/reactions", time.Second) // times out before the server applies it
	require.NoError(t, h.w.React(context.Background(), id, "fire", true), "the click is kept for a retry")
	assert.False(t, serverHas(h, id, "u-alice", "fire"))
	assert.True(t, reactionOf(h, id, "fire").Mine, "optimistic while the retry waits")
	h.fake.SetLatency("/api/v4/reactions", 0)
	h.eventually(func() bool { return serverHas(h, id, "u-alice", "fire") }, "not retried")
	assert.True(t, reactionOf(h, id, "fire").Mine)
	h.eventually(func() bool { return len(h.pairs()) == 0 }, "the pair is done")
	h.eventually(func() bool {
		return strings.Contains(h.fake.Preference("alice", "recent_emojis", "u-alice"), `"name":"fire"`)
	}, "recent emoji not saved")
}

func TestReactionLostReplyIsRetried(t *testing.T) {
	h, id := retryHarness(t, 100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond)
	h.fake.BreakReplies("/api/v4/reactions", true) // applied, the reply is lost; the echo beats the error
	require.NoError(t, h.w.React(context.Background(), id, "fire", true))
	h.fake.BreakReplies("/api/v4/reactions", false)
	h.eventually(func() bool { return h.fake.Hits("POST", "/api/v4/reactions") == 2 && len(h.pairs()) == 0 }, "not retried")
	assert.True(t, serverHas(h, id, "u-alice", "fire"))
	assert.Equal(t, state.ReactionView{Emoji: "fire", Count: 1, Mine: true}, reactionOf(h, id, "fire"))
}

func TestReactionFailingOnIsRolledBackAfterTheLastAttempt(t *testing.T) {
	h, id := retryHarness(t, 50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond)
	h.fake.SetFailure("/api/v4/reactions", 503)
	require.NoError(t, h.w.React(context.Background(), id, "tada", true))
	assert.Equal(t, state.ReactionView{Emoji: "tada", Count: 2, Mine: true}, reactionOf(h, id, "tada"))
	h.eventually(func() bool { return reactionOf(h, id, "tada") == state.ReactionView{Emoji: "tada", Count: 1} }, "not rolled back")
	assert.Equal(t, reactAttempts, h.fake.Hits("POST", "/api/v4/reactions"))
	assert.Empty(t, h.pairs())
	time.Sleep(200 * time.Millisecond) // nothing more is tried
	assert.Equal(t, reactAttempts, h.fake.Hits("POST", "/api/v4/reactions"))
}

func TestReactionToggledBackWhileWaitingIsSentAtOnce(t *testing.T) {
	h, id := retryHarness(t, time.Hour)
	h.fake.SetFailure("/api/v4/reactions", 503) // the add may or may not have landed
	require.NoError(t, h.w.React(context.Background(), id, "fire", true))
	h.fake.SetFailure("/api/v4/reactions", 0)
	require.NoError(t, h.w.React(context.Background(), id, "fire", false), "back to what the server had")
	assert.Equal(t, state.ReactionView{}, reactionOf(h, id, "fire"))
	del := "/api/v4/users/u-alice/posts/" + id + "/reactions/fire"
	h.eventually(func() bool { return h.fake.Hits("DELETE", del) == 1 && len(h.pairs()) == 0 }, "the unsure add was not undone at once")
	assert.Equal(t, 1, h.fake.Hits("POST", "/api/v4/reactions"), "the add is not retried")
	assert.False(t, serverHas(h, id, "u-alice", "fire"))
	assert.Equal(t, state.ReactionView{}, reactionOf(h, id, "fire"))
}

func TestReactionToggledBackAfterALostReplyUndoesIt(t *testing.T) {
	h, id := retryHarness(t, time.Hour)
	h.fake.BreakReplies("/api/v4/reactions", true) // the add lands, its reply is lost
	require.NoError(t, h.w.React(context.Background(), id, "fire", true))
	require.True(t, serverHas(h, id, "u-alice", "fire"))
	time.Sleep(200 * time.Millisecond) // its echo has come and ended the intent
	require.NoError(t, h.w.React(context.Background(), id, "fire", false))
	h.fake.BreakReplies("/api/v4/reactions", false)
	h.eventually(func() bool { return !serverHas(h, id, "u-alice", "fire") }, "the server kept the reaction the user took back")
	h.eventually(func() bool { return len(h.pairs()) == 0 }, "the pair is done")
	assert.Equal(t, state.ReactionView{}, reactionOf(h, id, "fire"))
}

func TestReactionToggledBackInFlightWithoutDoubtSendsNothingMore(t *testing.T) {
	h, id := welcomeHarness(t)
	h.fake.SetLatency("/api/v4/reactions", 300*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- h.w.React(context.Background(), id, "fire", true) }()
	h.eventually(func() bool { return reactionOf(h, id, "fire").Mine }, "not applied at once")
	require.NoError(t, h.w.React(context.Background(), id, "fire", false))
	require.NoError(t, h.w.React(context.Background(), id, "fire", true))
	require.NoError(t, <-done)
	assert.Zero(t, h.fake.Hits("DELETE", "/api/v4/users/u-alice/posts/"+id+"/reactions/fire"))
	assert.Equal(t, 1, h.fake.Hits("POST", "/api/v4/reactions"))
}

func TestReactionRetriedWhenTheWorkerIsLiveAgain(t *testing.T) {
	h, id := retryHarness(t, time.Hour)
	h.fake.SetDown(true)
	h.fake.DropConnections(false)
	h.eventually(func() bool { return h.w.Status() != StatusLive }, "still live")
	require.NoError(t, h.w.React(context.Background(), id, "fire", true))
	assert.True(t, reactionOf(h, id, "fire").Mine)
	h.fake.SetDown(false)
	h.live()
	h.eventually(func() bool { return serverHas(h, id, "u-alice", "fire") }, "not retried on going live")
	h.eventually(func() bool { return len(h.pairs()) == 0 }, "the pair is done")
}

func TestReactionPendingRetryStopsWithTheWorker(t *testing.T) {
	h, id := retryHarness(t, 100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond)
	h.fake.SetFailure("/api/v4/reactions", 503)
	require.NoError(t, h.w.React(context.Background(), id, "fire", true))
	h.stop() // Run returns: its loops, the retry loop among them, are done
	n := h.fake.Hits("POST", "/api/v4/reactions")
	time.Sleep(400 * time.Millisecond)
	assert.Equal(t, n, h.fake.Hits("POST", "/api/v4/reactions"), "nothing is sent after stop")
	assert.Less(t, n, reactAttempts)
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

func (h *harness) pairs() []string {
	h.w.reactMu.Lock()
	defer h.w.reactMu.Unlock()
	var out []string
	for k := range h.w.reactPairs {
		out = append(out, k)
	}
	return out
}
