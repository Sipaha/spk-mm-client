package api

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReactionsThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") }, "off-topic not loaded")
	post := fake.FindPost("c-offtopic", "Welcome to off-topic")

	require.NoError(t, f.svc.AddReaction(ctx, id, post, "rocket"))
	info, err := f.svc.EmojiInfo(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, []string{"rocket"}, info.Recent)
	assert.True(t, info.CustomEnabled)
	f.eventually(func() bool { i, _ := f.svc.EmojiInfo(ctx, id); return slices.Contains(i.Custom, "partyparrot") }, "custom emoji not listed")
	require.NoError(t, f.svc.RemoveReaction(ctx, id, post, "rocket"))

	fake.FailWith("/api/v4/reactions", 400, "app.reaction.save.save.too_many_reactions")
	assert.Equal(t, CodeTooManyReactions, codeOf(f.svc.AddReaction(ctx, id, post, "fire")))
	fake.FailWith("/api/v4/reactions", 403, "api.reaction.save.archived_channel.app_error")
	assert.Equal(t, CodeForbidden, codeOf(f.svc.AddReaction(ctx, id, post, "fire")))
	assert.Equal(t, "live", f.server(id).State, "403 on a reaction is not an expired session")
	fake.SetFailure("/api/v4/reactions", 0)
	assert.Equal(t, CodeNoPost, codeOf(f.svc.AddReaction(ctx, id, "nope", "fire")))
}
