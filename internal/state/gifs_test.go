package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestExtractPostGifsSourcesDedupCodeAndCap(t *testing.T) {
	meta := &model.PostMetadata{
		Images: map[string]model.PostImage{
			"https://cdn.example/inline.gif":    {Width: 400, Height: 225, Format: "gif"},
			"https://cdn.example/no-ext-giphy":  {Width: 320, Height: 180, Format: "gif"},
			"https://cdn.example/meta-only.gif": {Width: 250, Height: 250, Format: "gif"},
		},
		Embeds: []model.PostEmbed{
			{Type: "opengraph", ImageURLs: []string{"https://og.example/cat.gif"}},
		},
	}
	msg := "Check ![party](https://cdn.example/inline.gif) and ![tenor](https://cdn.example/no-ext-giphy)\n" +
		"Duplicate bare https://cdn.example/inline.gif and bare https://bare.example/dance.gif?v=2!\n" +
		"Inline code `![nope](https://code.example/a.gif)` and `https://code.example/b.gif`\n" +
		"```\n![block](https://code.example/c.gif)\nhttps://code.example/d.gif\n```\n" +
		"Static ![pic](https://cdn.example/photo.png)"

	got := extractPostGifs(msg, meta)
	require.Len(t, got, 5, "deduplicated and capped at 5, ignoring code and non-GIF")
	assert.Equal(t, []GifPreviewView{
		{ID: GifKey("https://cdn.example/inline.gif"), URL: "https://cdn.example/inline.gif", Alt: "party", Width: 400, Height: 225, Inline: true},
		{ID: GifKey("https://cdn.example/no-ext-giphy"), URL: "https://cdn.example/no-ext-giphy", Alt: "tenor", Width: 320, Height: 180, Inline: true},
		{ID: GifKey("https://og.example/cat.gif"), URL: "https://og.example/cat.gif", Inline: false},
		{ID: GifKey("https://cdn.example/meta-only.gif"), URL: "https://cdn.example/meta-only.gif", Width: 250, Height: 250, Inline: false},
		{ID: GifKey("https://bare.example/dance.gif?v=2"), URL: "https://bare.example/dance.gif?v=2", Inline: false},
	}, got)
}

func TestPostGifURLHeldOnlyAndPostViewsClearsGifs(t *testing.T) {
	s := newFixture()
	const gifURL = "https://cdn.example/hello.gif"
	p := model.Post{ID: "p-gif", ChannelID: "town", UserID: "u1", Message: "![hi](" + gifURL + ")", CreateAt: 10}
	s.SetWindow("town", []model.Post{p}, true, 10, 0)

	key := GifKey(gifURL)
	gotURL, ok := s.PostGifURL("p-gif", key)
	require.True(t, ok)
	assert.Equal(t, gifURL, gotURL)

	_, ok = s.PostGifURL("p-gif", "0000000000000000")
	assert.False(t, ok, "wrong key")
	_, ok = s.PostGifURL("not-held", key)
	assert.False(t, ok, "unheld post refused")

	// Search hits (PostViews) clear GifPreviews because the post may not be held.
	views := s.PostViews([]model.Post{p})
	require.Len(t, views, 1)
	assert.Empty(t, views[0].GifPreviews)
}

func TestPermalinkProjectionMissingUsersReactionsAndDeltaReuse(t *testing.T) {
	s := newFixture()
	p := model.Post{
		ID: "p-quote", ChannelID: "town", UserID: "u1", Message: "see https://mm.example/t/pl/orig", CreateAt: 20,
		Metadata: &model.PostMetadata{
			Embeds: []model.PostEmbed{{
				Type: "permalink",
				Permalink: &model.PermalinkData{
					PostID: "orig",
					Post: model.PermalinkPost{
						ID: "orig", ChannelID: "town", UserID: "u-quoted", Message: "original text", CreateAt: 10, FileCount: 2,
					},
					TeamName: "team", ChannelDisplayName: "Town Square", ChannelName: "town-square", ChannelType: "O",
				},
			}},
			Images: map[string]model.PostImage{"https://cdn.example/a.gif": {Width: 100, Height: 80, Format: "gif"}},
		},
	}
	s.SetWindow("town", []model.Post{p}, true, 20, 0)

	// MissingUserIDs includes the permalink author.
	assert.Contains(t, s.MissingUserIDs(), "u-quoted")

	// Initial ChannelDelta projection populates the permalink (author not loaded yet).
	patch1, ok := s.ChannelDelta("town", 0)
	require.True(t, ok)
	require.Len(t, patch1.Posts, 1)
	require.Len(t, patch1.Posts[0].Permalinks, 1)
	assert.Equal(t, "", patch1.Posts[0].Permalinks[0].Author)
	assert.Equal(t, 2, patch1.Posts[0].Permalinks[0].FileCount)
	require.Len(t, patch1.Posts[0].GifPreviews, 1)

	// Once the quoted user profile loads, the next ChannelDelta (even with
	// metadata pointer reuse) emits an update with fresh author + avatar!
	s.SetUsers([]model.User{{ID: "u-quoted", Username: "charlie", LastPictureUpdate: 42}})
	patch2, ok := s.ChannelDelta("town", patch1.Revision)
	require.True(t, ok)
	require.False(t, patch2.Reset)
	require.Len(t, patch2.Posts, 1, "delta must emit post when permalink author/avatar resolves")
	assert.Equal(t, "charlie", patch2.Posts[0].Permalinks[0].Author)
	assert.Equal(t, "42", patch2.Posts[0].Permalinks[0].Avatar)

	// Adding a reaction preserves Embeds and Images on the post metadata.
	s.mu.Lock()
	s.reactLocked("town", model.Reaction{UserID: "u1", PostID: "p-quote", EmojiName: "+1", CreateAt: 30}, true, true)
	s.mu.Unlock()
	after, ok := s.FindPost("p-quote")
	require.True(t, ok)
	require.NotNil(t, after.Metadata)
	assert.Len(t, after.Metadata.Embeds, 1, "reactLocked must preserve Embeds")
	assert.Len(t, after.Metadata.Images, 1, "reactLocked must preserve Images")
	assert.Len(t, after.Metadata.Reactions, 1)
}

func TestOpenGraphNoExtensionGifRoundTripProjectionAndPostGifURL(t *testing.T) {
	const raw = `{
		"id": "p-og-rt",
		"channel_id": "town",
		"user_id": "u1",
		"create_at": 40,
		"message": "https://giphy.example/gifs/abc",
		"metadata": {
			"embeds": [{
				"type": "opengraph",
				"url": "https://giphy.example/gifs/abc",
				"data": {
					"images": [{
						"secure_url": "https://cdn.example/animation?id=123",
						"type": "image/gif"
					}]
				}
			}]
		}
	}`
	var wire model.Post
	require.NoError(t, json.Unmarshal([]byte(raw), &wire))
	snap, err := json.Marshal(wire)
	require.NoError(t, err)
	var restored model.Post
	require.NoError(t, json.Unmarshal(snap, &restored))

	s := newFixture()
	s.SetWindow("town", []model.Post{restored}, true, 40, 0)

	const wantURL = "https://cdn.example/animation?id=123"
	gotURL, ok := s.PostGifURL("p-og-rt", GifKey(wantURL))
	require.True(t, ok, "PostGifURL must resolve no-extension OpenGraph GIF after snapshot reload")
	assert.Equal(t, wantURL, gotURL)

	patch, ok := s.ChannelDelta("town", 0)
	require.True(t, ok)
	require.Len(t, patch.Posts, 1)
	require.Len(t, patch.Posts[0].GifPreviews, 1)
	assert.Equal(t, wantURL, patch.Posts[0].GifPreviews[0].URL)
	assert.False(t, patch.Posts[0].GifPreviews[0].Inline)
}
