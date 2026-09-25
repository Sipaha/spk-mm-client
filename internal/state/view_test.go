package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestChannelViewComposition(t *testing.T) {
	s := newFixture()
	bot := mkPost("bot", "off", "u2", 1000)
	bot.Props = model.PostProps{FromWebhook: true, OverrideUsername: "gitlab", Attachments: []model.Attachment{{Title: "MR !1"}}}
	sys := mkPost("sys", "off", "u3", 1500)
	sys.Type = "system_join_channel"
	withMeta := mkPost("m", "off", "u2", 2000)
	withMeta.EditAt = 2100
	withMeta.PendingPostID = "u2:9"
	withMeta.Metadata = &model.PostMetadata{
		Files:     []model.FileInfo{{ID: "f1", Name: "a.pdf", Size: 10, MimeType: "application/pdf"}},
		Reactions: []model.Reaction{{UserID: "u2", EmojiName: "+1"}, {UserID: "u1", EmojiName: "+1"}, {UserID: "u3", EmojiName: "tada"}},
	}
	s.SetWindow("off", []model.Post{bot, sys, withMeta}, false, 5)
	s.MarkStale(6)
	s.SetDraft("off", "draft text")
	newSince := s.SetActive("off")
	assert.Equal(t, int64(10), newSince, "member.last_viewed_at before opening")
	s.AddPending("off", "", "sending…")

	v, ok := s.ChannelView("off")
	require.True(t, ok)
	assert.Equal(t, "Off-Topic", v.Name)
	assert.Equal(t, "team", v.TeamName)
	assert.Equal(t, "draft text", v.Draft)
	assert.Equal(t, "u1", v.MeID)
	assert.True(t, v.HasMore)
	assert.True(t, v.Syncing)
	assert.Equal(t, "m", v.GapAfter)
	require.Len(t, v.Posts, 4)
	assert.Equal(t, "gitlab", v.Posts[0].Author)
	assert.True(t, v.Posts[0].Bot)
	assert.Equal(t, "MR !1", v.Posts[0].Attachments[0].Title)
	assert.True(t, v.Posts[1].System)
	assert.Equal(t, "bob", v.Posts[2].Author)
	assert.Equal(t, int64(2100), v.Posts[2].EditAt)
	assert.Equal(t, []FileView{{ID: "f1", Name: "a.pdf", Size: 10, Mime: "application/pdf"}}, v.Posts[2].Files)
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 2, Mine: true}, {Emoji: "tada", Count: 1}}, v.Posts[2].Reactions)
	assert.Equal(t, "u2:9", v.Posts[2].PendingPostID, "postViewLocked carries pending_post_id through for the confirmed post")
	assert.True(t, v.Posts[3].Pending)
	assert.Equal(t, "alice", v.Posts[3].Author)
	assert.Equal(t, v.Posts[3].ID, v.Posts[3].PendingPostID, "a local pending entry is keyed by its own id")
}

func TestFileViewCarriesWhatPreviewsNeed(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	p := mkPost("p", "off", "u2", 1000)
	p.Metadata = &model.PostMetadata{Files: []model.FileInfo{{ID: "f1", Name: "a.png", Extension: "png", Size: 10, MimeType: "image/png", Width: 640, Height: 480, HasPreviewImage: true}}}
	s.SetWindow("off", []model.Post{p}, true, 5)
	v, _ := s.ChannelView("off")
	assert.Equal(t, []FileView{{ID: "f1", Name: "a.png", Ext: "png", Size: 10, Mime: "image/png", Width: 640, Height: 480, HasPreview: true}}, v.Posts[0].Files)
}

func TestChannelViewDMNameAndUnknownChannel(t *testing.T) {
	s := newFixture()
	v, ok := s.ChannelView("dm2")
	require.True(t, ok)
	assert.Equal(t, "bob", v.Name)
	assert.Equal(t, "D", v.Type)
	assert.False(t, v.Loaded)
	assert.True(t, v.Syncing)
	_, ok = s.ChannelView("nope")
	assert.False(t, ok)
}
