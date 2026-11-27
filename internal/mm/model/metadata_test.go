package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostMetadataWirePermalinkAndSnapshotRoundTrip(t *testing.T) {
	longMsg := strings.Repeat("Привет мир! ", 60) // > 500 runes
	raw := `{
		"id": "p1",
		"message": "see https://mm.example/team/pl/orig1",
		"metadata": {
			"embeds": [
				{
					"type": "permalink",
					"url": "https://mm.example/team/pl/orig1",
					"data": {
						"post_id": "orig1",
						"post": {
							"id": "orig1",
							"channel_id": "c-town",
							"user_id": "u-bob",
							"root_id": "r1",
							"message": "` + longMsg + `",
							"create_at": 1700000001000,
							"edit_at": 1700000002000,
							"file_ids": ["f1", "f2"],
							"metadata": {"files": [{"id": "f1"}, {"id": "f2"}, {"id": "f3"}]}
						},
						"team_name": "citeck",
						"channel_display_name": "Town Square",
						"channel_name": "town-square",
						"channel_type": "O"
					}
				},
				{
					"type": "opengraph",
					"url": "https://news.example/article",
					"data": {
						"title": "Should be dropped",
						"description": "Huge text",
						"images": [{"url": "https://news.example/photo.jpg", "type": "image/jpeg"}]
					}
				},
				{
					"type": "message_attachment",
					"data": {"big": "ignored"}
				}
			]
		}
	}`
	var p Post
	require.NoError(t, json.Unmarshal([]byte(raw), &p))
	require.NotNil(t, p.Metadata)
	require.Len(t, p.Metadata.Embeds, 1, "only permalink embed kept; non-GIF opengraph and message_attachment dropped")

	pl := p.Metadata.Embeds[0].Permalink
	require.NotNil(t, pl)
	assert.Equal(t, "orig1", pl.PostID)
	assert.Equal(t, "orig1", pl.Post.ID)
	assert.Equal(t, "c-town", pl.Post.ChannelID)
	assert.Equal(t, "u-bob", pl.Post.UserID)
	assert.Equal(t, "r1", pl.Post.RootID)
	assert.Equal(t, 500, len([]rune(pl.Post.Message)), "message truncated to <= 500 runes")
	assert.True(t, strings.HasSuffix(pl.Post.Message, "…"))
	assert.Equal(t, 3, pl.Post.FileCount, "file_count picks max of file_ids and metadata.files")
	assert.Equal(t, "citeck", pl.TeamName)
	assert.Equal(t, "Town Square", pl.ChannelDisplayName)
	assert.Equal(t, "town-square", pl.ChannelName)
	assert.Equal(t, "O", pl.ChannelType)

	// SQLite snapshot round-trip: compact format without raw "data" blob.
	snap, err := json.Marshal(p)
	require.NoError(t, err)
	assert.NotContains(t, string(snap), "Should be dropped")
	assert.NotContains(t, string(snap), `"data"`)

	var restored Post
	require.NoError(t, json.Unmarshal(snap, &restored))
	assert.Equal(t, p.Metadata, restored.Metadata)
}

func TestPostMetadataRetainsOnlyGifImagesAndEmbeds(t *testing.T) {
	raw := `{
		"id": "p2",
		"metadata": {
			"images": {
				"https://cdn.example/anim-by-format": {"width": 320, "height": 240, "format": "gif", "frame_count": 1},
				"https://cdn.example/anim-by-frames": {"width": 400, "height": 300, "format": "webp", "frame_count": 12},
				"https://cdn.example/anim-by-ext.GIF?v=1": {"width": 200, "height": 150, "format": ""},
				"https://cdn.example/static.png": {"width": 800, "height": 600, "format": "png", "frame_count": 1}
			},
			"embeds": [
				{"type": "image", "url": "https://cdn.example/static.png"},
				{"type": "image", "url": "https://cdn.example/anim-by-format"},
				{"type": "opengraph", "url": "https://tenor.example/view/123", "data": {
					"title": "Funny cat",
					"images": [
						{"url": "https://media.tenor.example/cat.png", "type": "image/png"},
						{"secure_url": "https://media.tenor.example/cat.gif?cid=1", "url": "http://media.tenor.example/cat.gif", "type": "image/gif"}
					]
				}}
			]
		}
	}`
	var p Post
	require.NoError(t, json.Unmarshal([]byte(raw), &p))
	require.NotNil(t, p.Metadata)
	assert.Len(t, p.Metadata.Images, 3)
	assert.NotContains(t, p.Metadata.Images, "https://cdn.example/static.png")
	assert.Contains(t, p.Metadata.Images, "https://cdn.example/anim-by-format")
	assert.Contains(t, p.Metadata.Images, "https://cdn.example/anim-by-frames")
	assert.Contains(t, p.Metadata.Images, "https://cdn.example/anim-by-ext.GIF?v=1")

	require.Len(t, p.Metadata.Embeds, 2)
	assert.Equal(t, PostEmbed{Type: "image", URL: "https://cdn.example/anim-by-format"}, p.Metadata.Embeds[0])
	assert.Equal(t, PostEmbed{
		Type:      "opengraph",
		URL:       "https://tenor.example/view/123",
		ImageURLs: []string{"https://media.tenor.example/cat.gif?cid=1"},
	}, p.Metadata.Embeds[1])

	// Snapshot round-trip keeps GIF metadata intact.
	snap, err := json.Marshal(p)
	require.NoError(t, err)
	var restored Post
	require.NoError(t, json.Unmarshal(snap, &restored))
	assert.Equal(t, p.Metadata, restored.Metadata)
}

func TestPostMetadataCapsAndToleratesMalformed(t *testing.T) {
	// More than 5 GIF images are capped deterministically at 5.
	rawCap := `{"id":"p3","metadata":{"images":{
		"https://a.test/1.gif":{"format":"gif"},
		"https://a.test/2.gif":{"format":"gif"},
		"https://a.test/3.gif":{"format":"gif"},
		"https://a.test/4.gif":{"format":"gif"},
		"https://a.test/5.gif":{"format":"gif"},
		"https://a.test/6.gif":{"format":"gif"},
		"https://a.test/7.gif":{"format":"gif"}
	}}}`
	var p Post
	require.NoError(t, json.Unmarshal([]byte(rawCap), &p))
	require.NotNil(t, p.Metadata)
	assert.Len(t, p.Metadata.Images, 5)
	assert.NotContains(t, p.Metadata.Images, "https://a.test/6.gif")

	// Malformed embeds/images do not drop the post or its files/reactions.
	var bad Post
	require.NoError(t, json.Unmarshal([]byte(`{"id":"p4","metadata":{"files":[{"id":"f1","name":"a.txt"}],"embeds":"garbage","images":123}}`), &bad))
	assert.Equal(t, "p4", bad.ID)
	require.NotNil(t, bad.Metadata)
	require.Len(t, bad.Metadata.Files, 1)
	assert.Nil(t, bad.Metadata.Embeds)
	assert.Nil(t, bad.Metadata.Images)
}

func TestOpenGraphNoExtensionGifSurvivesSnapshotRoundTrip(t *testing.T) {
	raw := `{
		"id": "p-og-noext",
		"metadata": {
			"embeds": [
				{
					"type": "opengraph",
					"url": "https://giphy.example/gifs/abc",
					"data": {
						"title": "Animated without extension",
						"images": [
							{"url": "http://cdn.example/animation?id=123", "secure_url": "https://cdn.example/animation?id=123", "type": "image/gif"},
							{"url": "https://cdn.example/second-anim?fmt=gif", "type": "image/gif"},
							{"url": "https://cdn.example/static-image?id=456", "type": "image/png"},
							{"url": "javascript:alert(1)", "type": "image/gif"}
						]
					}
				},
				{
					"type": "opengraph",
					"url": "https://example.com/article",
					"data": {
						"title": "Non-GIF page",
						"images": [
							{"secure_url": "https://cdn.example/banner?id=999", "type": "image/png"}
						]
					}
				}
			]
		}
	}`

	var wire Post
	require.NoError(t, json.Unmarshal([]byte(raw), &wire))
	require.NotNil(t, wire.Metadata)
	assert.Nil(t, wire.Metadata.Images, "no metadata.images present")
	require.Len(t, wire.Metadata.Embeds, 1, "non-GIF opengraph dropped; GIF opengraph kept")
	assert.Equal(t, PostEmbed{
		Type: "opengraph",
		URL:  "https://giphy.example/gifs/abc",
		ImageURLs: []string{
			"https://cdn.example/animation?id=123",
			"https://cdn.example/second-anim?fmt=gif",
		},
	}, wire.Metadata.Embeds[0])

	// SQLite snapshot round-trip: Marshal -> Unmarshal preserves no-extension GIF URLs
	// without keeping raw OpenGraph `data`.
	snap, err := json.Marshal(wire)
	require.NoError(t, err)
	assert.NotContains(t, string(snap), `"data"`)
	assert.NotContains(t, string(snap), "Animated without extension")

	var restored Post
	require.NoError(t, json.Unmarshal(snap, &restored))
	require.NotNil(t, restored.Metadata)
	assert.Equal(t, wire.Metadata, restored.Metadata)

	// Snapshot decode still enforces http(s) scheme validation and the 5-URL post cap.
	dirtySnap := `{"id":"p-og-cap","metadata":{"embeds":[
		{"type":"opengraph","url":"https://a.test/1","image_urls":[
			"ftp://bad.test/a.gif",
			"javascript:alert(1)",
			"https://cdn.example/1",
			"https://cdn.example/2",
			"https://cdn.example/3"
		]},
		{"type":"opengraph","url":"https://a.test/2","image_urls":[
			"https://cdn.example/4",
			"https://cdn.example/5",
			"https://cdn.example/6"
		]}
	]}}`
	var capped Post
	require.NoError(t, json.Unmarshal([]byte(dirtySnap), &capped))
	require.NotNil(t, capped.Metadata)
	require.Len(t, capped.Metadata.Embeds, 2)
	assert.Equal(t, []string{"https://cdn.example/1", "https://cdn.example/2", "https://cdn.example/3"}, capped.Metadata.Embeds[0].ImageURLs)
	assert.Equal(t, []string{"https://cdn.example/4", "https://cdn.example/5"}, capped.Metadata.Embeds[1].ImageURLs)
}

func TestIsGifURL(t *testing.T) {
	assert.True(t, IsGifURL("https://media.giphy.com/media/abc/giphy.gif"))
	assert.True(t, IsGifURL("https://cdn.example/CAT.GIF?width=320#frame"))
	assert.True(t, IsGifURL("/static/images/sample.gif"))
	assert.False(t, IsGifURL("https://cdn.example/photo.png"))
	assert.False(t, IsGifURL("https://cdn.example/gif-page?ext=.gif"))
	assert.False(t, IsGifURL(""))
}
