package state

import (
	"strings"
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
	s.SetWindow("off", []model.Post{bot, sys, withMeta}, false, 5, 0)
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
	assert.Equal(t, "0", v.MeAvatar, "my own picture version, like a post's author's — fixture's alice has LastPictureUpdate 0")
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

func TestPostViewSavedComesFromFlaggedPostPref(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	p1 := mkPost("p1", "off", "u2", 1000)
	p2 := mkPost("p2", "off", "u2", 2000)
	s.SetWindow("off", []model.Post{p1, p2}, true, 5, 0)
	assert.Equal(t, Change{Channels: []string{"off"}}, s.SetPostSaved("p1", true))
	v, ok := s.ChannelView("off")
	require.True(t, ok)
	require.Len(t, v.Posts, 2)
	assert.True(t, v.Posts[0].Saved)
	assert.False(t, v.Posts[1].Saved)
	assert.Equal(t, Change{Channels: []string{"off"}}, s.SetPostSaved("p1", false))
	v, _ = s.ChannelView("off")
	assert.False(t, v.Posts[0].Saved)
}

func TestFileViewCarriesWhatPreviewsNeed(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	p := mkPost("p", "off", "u2", 1000)
	p.Metadata = &model.PostMetadata{Files: []model.FileInfo{{ID: "f1", Name: "a.png", Extension: "png", Size: 10, MimeType: "image/png", Width: 640, Height: 480, HasPreviewImage: true}}}
	s.SetWindow("off", []model.Post{p}, true, 5, 0)
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

// T7 / spike §4.1 п.6: under CRT a reply being sent lives in the thread
// pane only; without CRT the feed shows it inline.
func TestPendingReplyIsNotInTheCRTFeed(t *testing.T) {
	for _, crt := range []bool{true, false} {
		s := crtFixture(crt)
		s.SetWindow("town", []model.Post{mkPost("root", "town", "u2", 1000)}, true, 5, 0)
		s.AddPending("town", "root", "a reply")
		s.AddPending("town", "", "a root")
		v, _ := s.ChannelView("town")
		var msgs []string
		for _, p := range v.Posts {
			msgs = append(msgs, p.Message)
		}
		if crt {
			assert.Equal(t, []string{"msg root", "a root"}, msgs, "crt: the pending reply is not in the feed")
		} else {
			assert.Equal(t, []string{"msg root", "a reply", "a root"}, msgs, "no crt: inline")
		}
	}
}

// Without CRT a reply in the feed carries its root's author and a short
// snippet, taken from what is held (window, loaded history); a root not
// held gives none (the UI says "reply in a thread").
func TestNonCRTReplyCarriesRootContext(t *testing.T) {
	s := crtFixture(false)
	long := mkPost("long", "town", "u2", 1000)
	long.Message = "  first   line\n\nsecond\tline " + strings.Repeat("я", 100)
	file := mkPost("file", "town", "u3", 1100)
	file.Message = " \n "
	file.Metadata = &model.PostMetadata{Files: []model.FileInfo{{ID: "f1", Name: "report.pdf"}, {ID: "f2", Name: "b.png"}}}
	hist := mkPost("hist", "town", "u2", 500)
	hist.Message = "from history"
	s.SetWindow("town", []model.Post{long, file,
		reply("r1", "long", "u3", 2000, 1), reply("r2", "file", "u2", 2100, 1),
		reply("r3", "hist", "u3", 2200, 1), reply("r4", "gone", "u3", 2300, 1)}, false, 5, 0)

	byID := func() map[string]PostView {
		v, _ := s.ChannelView("town")
		out := map[string]PostView{}
		for _, p := range v.Posts {
			out[p.ID] = p
		}
		return out
	}
	v := byID()
	assert.Equal(t, "bob", v["r1"].RootAuthor)
	want := []rune("first line second line " + strings.Repeat("я", 100))
	assert.Equal(t, string(want[:79])+"…", v["r1"].RootSnippet, "whitespace collapsed, ≤ 80 runes")
	assert.Equal(t, 80, len([]rune(v["r1"].RootSnippet)))
	assert.Equal(t, "carol", v["r2"].RootAuthor)
	assert.Equal(t, "report.pdf", v["r2"].RootSnippet, "no text: the first file's name")
	assert.Empty(t, v["r3"].RootAuthor, "root not held yet")
	assert.Empty(t, v["r4"].RootSnippet)
	assert.Empty(t, v["long"].RootAuthor, "roots carry no context")

	s.SetActive("town")
	s.AppendOlder("town", []model.Post{hist}, true, 0)
	v = byID()
	assert.Equal(t, "bob", v["r3"].RootAuthor, "root found in the loaded history")
	assert.Equal(t, "from history", v["r3"].RootSnippet)
}

// TestChannelViewMeAvatarReflectsMyPictureVersion: MeAvatar is not fixed to
// the bootstrap snapshot — a later picture version for me (e.g. a live
// user_updated, mirrored here via SetUsers) shows up on the next view, the
// same way it already would for a post I authored (PostView.Avatar).
func TestChannelViewMeAvatarReflectsMyPictureVersion(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", nil, true, 0, 0)
	v, ok := s.ChannelView("off")
	require.True(t, ok)
	assert.Equal(t, "0", v.MeAvatar)

	s.SetUsers([]model.User{{ID: "u1", Username: "alice", LastPictureUpdate: 42}})
	v, ok = s.ChannelView("off")
	require.True(t, ok)
	assert.Equal(t, "42", v.MeAvatar)
}
