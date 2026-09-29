package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// webhookPosts: a GitLab webhook with its own name and icon, one with an
// emoji icon (the server also rewrites its URL to the emoji's), one asking
// for the account's own picture, and a plain API post carrying the same
// props without from_webhook (the webapp ignores them there).
func webhookPosts() []model.Post {
	gl := mkPost("w1", "off", "u2", 1000)
	gl.Props = model.PostProps{FromWebhook: true, OverrideUsername: "GitLab", OverrideIconURL: "https://gitlab.example/fox.png"}
	emo := mkPost("w2", "off", "u2", 2000)
	emo.Props = model.PostProps{FromWebhook: true, OverrideIconEmoji: ":tada:", OverrideIconURL: "/static/emoji/1f389.png"}
	own := mkPost("w3", "off", "u2", 3000)
	own.Props = model.PostProps{FromWebhook: true, OverrideIconURL: "https://gitlab.example/fox.png", UseUserIcon: true}
	api := mkPost("w4", "off", "u2", 4000)
	api.Props = model.PostProps{OverrideUsername: "Impostor", OverrideIconURL: "https://evil.example/x.png"}
	odd := mkPost("w5", "off", "u2", 5000)
	odd.Props = model.PostProps{FromWebhook: true, OverrideIconEmoji: "../x", OverrideIconURL: "https://gitlab.example/fox.png"}
	return []model.Post{gl, emo, own, api, odd}
}

func TestPostViewWebhookOverridesFollowTheServerConfig(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", webhookPosts(), true, 5, 0)
	v, ok := s.ChannelView("off")
	require.True(t, ok)
	require.Len(t, v.Posts, 5)

	gl := v.Posts[0]
	assert.Equal(t, "GitLab", gl.Author)
	assert.Equal(t, "bob", gl.RealAuthor, "the account behind the webhook, for a tooltip")
	assert.Equal(t, "post", gl.Icon)
	assert.True(t, gl.Bot, "a webhook post is marked BOT (webapp: BotTag for from_webhook)")
	assert.True(t, gl.Webhook)
	assert.Empty(t, gl.Status, "no presence dot for a webhook")

	assert.Equal(t, ":tada:", v.Posts[1].Icon, "an emoji icon wins over the URL the server derived from it")
	assert.Equal(t, "bob", v.Posts[1].Author, "no override_username: the account's name")
	assert.Empty(t, v.Posts[1].RealAuthor)

	assert.Empty(t, v.Posts[2].Icon, "use_user_icon keeps the account's picture")

	api := v.Posts[3]
	assert.Equal(t, "bob", api.Author, "not from a webhook: overrides are ignored")
	assert.Empty(t, api.Icon)
	assert.False(t, api.Webhook)
	assert.False(t, api.Bot)

	assert.Equal(t, "post", v.Posts[4].Icon, "an emoji name that is not a name falls back to the URL")

	url, ok := s.PostIconURL("w1")
	assert.True(t, ok)
	assert.Equal(t, "https://gitlab.example/fox.png", url)
	for _, id := range []string{"w2", "w3", "w4", "nope"} {
		_, ok := s.PostIconURL(id)
		assert.False(t, ok, "%s: no picture to fetch for it", id)
	}
}

func TestPostViewWebhookOverridesOffInConfig(t *testing.T) {
	s := New(fixedNow)
	b := fixture()
	b.Config.PostUsernameOverride, b.Config.PostIconOverride = false, false
	s.Bootstrap(b)
	s.SetUsers([]model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob"}})
	s.SetWindow("off", webhookPosts(), true, 5, 0)
	v, ok := s.ChannelView("off")
	require.True(t, ok)
	gl := v.Posts[0]
	assert.Equal(t, "bob", gl.Author, "EnablePostUsernameOverride=false: the account's name")
	assert.Empty(t, gl.RealAuthor)
	// Deliberately unlike the webapp (which shows the owner's picture here):
	// the owner's avatar would read as if the owner had written the post.
	assert.Equal(t, "webhook", gl.Icon, "EnablePostIconOverride=false: its URL is ignored, the generic webhook icon")
	assert.Equal(t, "webhook", v.Posts[1].Icon, "EnablePostIconOverride=false: its emoji is ignored too")
	assert.Empty(t, v.Posts[2].Icon, "use_user_icon: the integration asked for the account's picture")
	assert.Empty(t, v.Posts[3].Icon, "not from a webhook")
	assert.True(t, gl.Bot, "still a webhook post")
	_, ok = s.PostIconURL("w1")
	assert.False(t, ok, "no icon override allowed: nothing to fetch")
}

// A webhook post without an icon of its own shows the generic webhook icon
// ("webhook"), never the account's picture: it would look as if the owner
// had written it (webapp post_profile_picture: DEFAULT_WEBHOOK_LOGO for
// from_webhook without use_user_icon, with EnablePostIconOverride; unlike
// the webapp, with it off too). A bot account's own post and use_user_icon
// keep the account's picture.
func TestPostViewWebhookWithoutAnIconShowsTheGenericOne(t *testing.T) {
	bare := mkPost("h1", "off", "u2", 1000)
	bare.Props = model.PostProps{FromWebhook: true, OverrideUsername: "CI"}
	own := mkPost("h2", "off", "u2", 2000)
	own.Props = model.PostProps{FromWebhook: true, UseUserIcon: true}
	bot := mkPost("h3", "off", "u2", 3000)
	bot.Props = model.PostProps{FromBot: true}

	s := newFixture()
	s.SetWindow("off", []model.Post{bare, own, bot}, true, 3, 0)
	v, ok := s.ChannelView("off")
	require.True(t, ok)
	assert.Equal(t, "webhook", v.Posts[0].Icon)
	assert.Empty(t, v.Posts[0].IconVersion)
	assert.Equal(t, "bob", v.Posts[0].RealAuthor, "the tooltip still names the account")
	assert.Empty(t, v.Posts[1].Icon, "use_user_icon: the account's picture")
	assert.Empty(t, v.Posts[2].Icon, "a bot account's own post: its own avatar")
	_, ok = s.PostIconURL("h1")
	assert.False(t, ok, "no picture to fetch")

	off := New(fixedNow)
	b := fixture()
	b.Config.PostIconOverride = false
	off.Bootstrap(b)
	off.SetUsers([]model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob"}})
	off.SetWindow("off", []model.Post{bare}, true, 1, 0)
	v, ok = off.ChannelView("off")
	require.True(t, ok)
	assert.Equal(t, "webhook", v.Posts[0].Icon, "EnablePostIconOverride=false: still the generic icon (unlike the webapp)")
}

func TestConfigCarriesPostOverrideFlags(t *testing.T) {
	s := newFixture()
	cfg := s.Config()
	assert.True(t, cfg.PostUsernameOverride)
	assert.True(t, cfg.PostIconOverride)
	assert.False(t, cfg.ImageProxy)
}

// M2: the icon URL the UI uses is versioned by what the post points at.
func TestPostViewIconVersionFollowsTheIconURL(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", webhookPosts(), true, 5, 0)
	v, _ := s.ChannelView("off")
	gl := v.Posts[0]
	require.Equal(t, "post", gl.Icon)
	assert.Regexp(t, `^[0-9a-f]{16}$`, gl.IconVersion)
	assert.Empty(t, v.Posts[1].IconVersion, "an emoji icon has no picture to version")
	assert.Empty(t, v.Posts[3].IconVersion)

	edited := webhookPosts()[0]
	edited.Props.OverrideIconURL = "https://gitlab.example/fox-2.png"
	edited.UpdateAt, edited.EditAt = 1500, 1500
	s.SetWindow("off", []model.Post{edited}, true, 5, 0)
	v, _ = s.ChannelView("off")
	assert.NotEqual(t, gl.IconVersion, v.Posts[0].IconVersion, "a changed icon URL is a new version")
}

// M6: a desktop notification names a webhook post's sender the way the
// feed does — override_username only when the server allows it.
func TestNotifySenderFollowsTheUsernameOverride(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		s := New(fixedNow)
		b := fixture()
		b.Config.PostUsernameOverride = allowed
		s.Bootstrap(b)
		s.SetUsers([]model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob"}})
		s.ClearGuard()
		p := mkPost("h1", "town", "u2", 5000)
		p.Props = model.PostProps{FromWebhook: true, OverrideUsername: "GitLab"}
		eff := s.ApplyEvent(postedEv(p, "u1"))
		require.NotNil(t, eff.Notify)
		want := "bob"
		if allowed {
			want = "GitLab"
		}
		assert.Equal(t, want, eff.Notify.SenderName, "allowed=%v", allowed)

		api := mkPost("h2", "town", "u2", 6000)
		api.Props = model.PostProps{OverrideUsername: "Impostor"}
		eff = s.ApplyEvent(postedEv(api, "u1"))
		require.NotNil(t, eff.Notify)
		assert.Equal(t, "bob", eff.Notify.SenderName, "not from a webhook")
	}
}
