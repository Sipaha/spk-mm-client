package state

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

func reactionEv(typ, user, post, emoji string) ws.Event {
	rb, _ := json.Marshal(model.Reaction{UserID: user, PostID: post, EmojiName: emoji})
	d, _ := json.Marshal(map[string]any{"reaction": string(rb)})
	return ws.Event{Type: typ, Data: d, Broadcast: ws.Broadcast{ChannelID: "off"}}
}

func reactions(s *Server, postID string) []ReactionView {
	v, _ := s.ChannelView("off")
	for _, p := range v.Posts {
		if p.ID == postID {
			return p.Reactions
		}
	}
	return nil
}

func withPost(s *Server) {
	s.ClearGuard()
	s.SetWindow("off", []model.Post{mkPost("p", "off", "u2", 1000)}, true, 5, 0)
}

func TestReactLocalAppliesAtOnceAndUndoRestores(t *testing.T) {
	s := newFixture()
	withPost(s)
	ch, was, ok := s.ReactLocalWas("p", "+1", true)
	require.True(t, ok)
	assert.False(t, was, "not ours before the click")
	assert.Equal(t, Change{Channels: []string{"off"}}, ch)
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"))
	assert.Equal(t, Change{Channels: []string{"off"}}, s.SetMyReaction("p", "+1", false))
	assert.Empty(t, reactions(s, "p"))
	_, _, ok = s.ReactLocalWas("nope", "+1", true)
	assert.False(t, ok, "a post not in memory")
}

func TestOwnEchoIsIdempotentAndOthersCount(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocalWas("p", "+1", true)
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"))
	eff := s.ApplyEvent(reactionEv("reaction_added", "u2", "p", "+1"))
	assert.Equal(t, []string{"off"}, eff.Channels)
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 2, Mine: true}}, reactions(s, "p"))
}

func TestLateEchoOfUndoneReactionIsIgnored(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocalWas("p", "+1", true)  // click: add (the request succeeded)
	s.ReactLocalWas("p", "+1", false) // click again: remove
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))
	assert.Empty(t, reactions(s, "p"), "the late echo of the first click is stale")
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1")) // echo of the second click ends the intent
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))   // a real add from another device
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"))
}

func TestIntentExpires(t *testing.T) {
	now := t0
	s := New(func() time.Time { return now })
	s.Bootstrap(fixture())
	withPost(s)
	s.ReactLocalWas("p", "+1", true)
	now = now.Add(31 * time.Second)
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1")) // removed elsewhere, our echo never came
	assert.Empty(t, reactions(s, "p"))
}

func TestRecentEmojisFollowTheWebapp(t *testing.T) {
	s := newFixture()
	s.BumpRecentEmoji("smile")
	s.BumpRecentEmoji("+1")
	s.BumpRecentEmoji("smile")
	pref := s.BumpRecentEmoji("tada")
	assert.Equal(t, model.Preference{UserID: "u1", Category: "recent_emojis", Name: "u1",
		Value: `[{"name":"+1","usageCount":1},{"name":"tada","usageCount":1},{"name":"smile","usageCount":2}]`}, pref)
	assert.Equal(t, []string{"smile", "tada", "+1"}, s.RecentEmojis())
	for i := 0; i < 40; i++ {
		s.BumpRecentEmoji(fmt.Sprintf("e%d", i))
	}
	assert.Len(t, s.RecentEmojis(), 27)
	assert.Equal(t, "smile", s.RecentEmojis()[0], "the most used stays")
}

// TestRecentPreferenceReadsWithoutBumping: RecentPreference (used by
// Worker.saveRecentPreference once a reaction has landed) must read the
// live list without incrementing anything — BumpRecentEmoji is the only
// thing that bumps, called once at issue time (Worker.React).
func TestRecentPreferenceReadsWithoutBumping(t *testing.T) {
	s := newFixture()
	s.BumpRecentEmoji("tada")
	before := s.RecentPreference()
	assert.Equal(t, model.Preference{UserID: "u1", Category: "recent_emojis", Name: "u1",
		Value: `[{"name":"tada","usageCount":1}]`}, before)
	// Reading it again changes nothing: usageCount stays 1, not 2.
	assert.Equal(t, before, s.RecentPreference())
	assert.Equal(t, []string{"tada"}, s.RecentEmojis())

	// It reflects a bump made after the first read, live — not a snapshot.
	s.BumpRecentEmoji("fire")
	assert.Equal(t, model.Preference{UserID: "u1", Category: "recent_emojis", Name: "u1",
		Value: `[{"name":"tada","usageCount":1},{"name":"fire","usageCount":1}]`}, s.RecentPreference())
}

func TestRecentEmojisSurviveABadPreference(t *testing.T) {
	s := newFixture()
	d, _ := json.Marshal(map[string]any{"preferences": `[{"user_id":"u1","category":"recent_emojis","name":"u1","value":"not json"}]`})
	s.ApplyEvent(ws.Event{Type: "preferences_changed", Data: d})
	assert.Empty(t, s.RecentEmojis())
	s.BumpRecentEmoji("+1")
	assert.Equal(t, []string{"+1"}, s.RecentEmojis())
}

func TestReactLocalWasReportsOurPreviousReaction(t *testing.T) {
	s := newFixture()
	withPost(s)
	_, was, ok := s.ReactLocalWas("p", "+1", true)
	require.True(t, ok)
	assert.False(t, was)
	_, was, _ = s.ReactLocalWas("p", "+1", true) // a second add: it was there
	assert.True(t, was)
	_, was, _ = s.ReactLocalWas("p", "+1", false)
	assert.True(t, was)
	_, was, _ = s.ReactLocalWas("p", "+1", false)
	assert.False(t, was)
}

func TestSetMyReactionIsExplicitAndEndsTheIntent(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocalWas("p", "+1", true)
	assert.Equal(t, Change{Channels: []string{"off"}}, s.SetMyReaction("p", "+1", true))
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"), "sets, does not toggle")
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1"))
	assert.Empty(t, reactions(s, "p"), "no intent left to drop the event")
	s.SetMyReaction("p", "+1", false)
	s.SetMyReaction("p", "+1", false)
	assert.Empty(t, reactions(s, "p"))
	assert.Equal(t, Change{}, s.SetMyReaction("nope", "+1", true))
}

func TestForgetReactIntent(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocalWas("p", "+1", true)
	s.ReactLocalWas("p", "+1", false) // cancelled before it was sent
	s.ForgetReactIntent("p", "+1")
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1")) // from another device
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"))
}

func TestPinnedIntentDoesNotExpire(t *testing.T) {
	now := t0
	s := New(func() time.Time { return now })
	s.Bootstrap(fixture())
	withPost(s)
	s.ReactLocalWas("p", "+1", true)
	s.PinReactIntent("p", "+1", true) // its request waits for a retry
	now = now.Add(time.Hour)
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1"))
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"), "still our latest click")
	s.PinReactIntent("p", "+1", false) // sent: the echo is due within intentTTL
	now = now.Add(31 * time.Second)
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1"))
	assert.Empty(t, reactions(s, "p"))
	s.PinReactIntent("p", "+1", true) // no intent: nothing to pin
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"))
}

func TestAClickKeepsThePinOfAWaitingIntent(t *testing.T) {
	now := t0
	s := New(func() time.Time { return now })
	s.Bootstrap(fixture())
	withPost(s)
	s.ReactLocalWas("p", "+1", true)
	s.PinReactIntent("p", "+1", true) // its request waits for a retry
	s.ReactLocalWas("p", "+1", false) // a click meanwhile: the retry will send it
	s.ReactLocalWas("p", "+1", true)
	now = now.Add(time.Hour)
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1"))
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"), "still pinned")
}

func TestOlderMatchingEchoDoesNotRetireNewerReactionRequests(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocalWas("p", "+1", true)
	s.ExpectReactEcho("p", "+1", true)
	s.ReactLocalWas("p", "+1", false)
	s.ExpectReactEcho("p", "+1", false)
	s.ReactLocalWas("p", "+1", true)
	s.ExpectReactEcho("p", "+1", true)
	s.PinReactIntent("p", "+1", false) // HTTP completed; WS echoes are still queued
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1"))
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"), "older opposite echo cannot undo the last click")
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1")) // another device after all our echoes
	assert.Empty(t, reactions(s, "p"), "final echo retires the guard")
}

func TestCoalescedUnsentClicksKeepEarlierPendingEchoProtection(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocalWas("p", "+1", true)
	s.ExpectReactEcho("p", "+1", true)
	s.ReactLocalWas("p", "+1", false)
	s.ExpectReactEcho("p", "+1", false)
	s.ReactLocalWas("p", "+1", true) // this add/remove pair was coalesced before sending
	s.ReactLocalWas("p", "+1", false)
	s.ForgetReactIntent("p", "+1")
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))
	assert.Empty(t, reactions(s, "p"), "unsent cancellation does not let an older echo restore the reaction")
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1"))
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1")) // another device
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, reactions(s, "p"))
}

func TestRefusedRequestDoesNotWaitForAnEchoThatCannotArrive(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocalWas("p", "+1", true)
	s.ExpectReactEcho("p", "+1", true)
	s.ReactLocalWas("p", "+1", false)
	refused := s.ExpectReactEcho("p", "+1", false)
	s.CancelReactEcho("p", "+1", refused)
	s.SetMyReaction("p", "+1", true)
	s.ApplyEvent(reactionEv("reaction_added", "u1", "p", "+1"))
	s.ApplyEvent(reactionEv("reaction_removed", "u1", "p", "+1")) // another device
	assert.Empty(t, reactions(s, "p"), "refusal must not keep an impossible echo pending")
}

// ---- who reacted (reactor tooltip/modal) ----

func TestReactorIDsOrderedOldestFirstExcludingMe(t *testing.T) {
	s := newFixture()
	withPost(s)
	s.ReactLocalWas("p", "+1", true) // me (u1): excluded from the result
	// bob's reaction predates carol's.
	s.reactLocked("off", model.Reaction{UserID: "u2", PostID: "p", EmojiName: "+1", CreateAt: 10}, true, true)
	s.reactLocked("off", model.Reaction{UserID: "u3", PostID: "p", EmojiName: "+1", CreateAt: 20}, true, true)
	ids, ok := s.ReactorIDs("p", "+1")
	require.True(t, ok)
	assert.Equal(t, []string{"u2", "u3"}, ids, "bob (oldest) before carol, me excluded")
	none, ok := s.ReactorIDs("p", "tada")
	require.True(t, ok, "the post is held, just with no reactions of this emoji")
	assert.Empty(t, none)
}

func TestReactorIDsPostNotHeld(t *testing.T) {
	s := newFixture()
	_, ok := s.ReactorIDs("nope", "+1")
	assert.False(t, ok)
}

func TestMissingAmong(t *testing.T) {
	s := newFixture() // u1, u2, u3 are known
	assert.Equal(t, []string{"u9"}, s.MissingAmong([]string{"u2", "u9", "u3"}))
	assert.Empty(t, s.MissingAmong([]string{"u1", "u2"}))
}

func TestResolveReactors(t *testing.T) {
	s := newFixture()
	v := s.ResolveReactors([]string{"u2", "u9", "u3"})
	assert.Equal(t, ReactionUsersView{
		Users: []Reactor{
			{ID: "u2", Name: "bob", Avatar: "0"},
			{ID: "u9", Name: "", Avatar: ""},
			{ID: "u3", Name: "carol", Avatar: "0"},
		},
		Unknown: 1,
	}, v)
}
