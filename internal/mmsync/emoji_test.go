package mmsync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
)

func TestCustomEmojiLoadAndEmojiID(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(func() bool { _, ok := h.w.State().CustomEmojiID("partyparrot"); return ok }, "custom emoji list not loaded")
	ctx := context.Background()
	id, err := h.w.EmojiID(ctx, "partyparrot")
	require.NoError(t, err)
	assert.Equal(t, "e-parrot", id)

	id, err = h.w.EmojiID(ctx, "nope")
	require.NoError(t, err)
	assert.Empty(t, id)
	_, _ = h.w.EmojiID(ctx, "nope")
	assert.Equal(t, 1, h.fake.Hits("GET", "/api/v4/emoji/name/nope"), "a miss is remembered")

	e := h.fake.AddEmoji("shipit")
	h.eventually(func() bool { got, ok := h.w.State().CustomEmojiID("shipit"); return ok && got == e.ID }, "emoji_added not applied")
	id, err = h.w.EmojiID(ctx, "shipit")
	require.NoError(t, err)
	assert.Equal(t, e.ID, id)
	assert.Zero(t, h.fake.Hits("GET", "/api/v4/emoji/name/shipit"), "known from the event, no lookup")
}

func TestCustomEmojiDisabledSkipsTheList(t *testing.T) {
	h := newHarness(t, mmfake.Options{DisableCustomEmoji: true})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	id, err := h.w.EmojiID(context.Background(), "partyparrot")
	require.NoError(t, err)
	assert.Empty(t, id)
	assert.Zero(t, h.fake.Hits("GET", "/api/v4/emoji"))
	assert.Zero(t, h.fake.Hits("GET", "/api/v4/emoji/name/partyparrot"))
}
