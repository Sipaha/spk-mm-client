package state

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestAutocompleteScopeIsTheChannelsTeamOrTheCurrentOne(t *testing.T) {
	s := newFixture()
	team, ok := s.AutocompleteScope("off")
	assert.True(t, ok)
	assert.Equal(t, "t1", team)
	team, ok = s.AutocompleteScope("dm2")
	assert.True(t, ok)
	assert.Equal(t, "t1", team, "a DM searches the team the user is on")
	_, ok = s.AutocompleteScope("notmember")
	assert.False(t, ok, "only channels we are in")
	_, ok = s.AutocompleteScope("nope")
	assert.False(t, ok)
}

func TestJoinedAndPresences(t *testing.T) {
	s := newFixture()
	assert.Equal(t, map[string]bool{"off": true}, s.Joined([]string{"off", "notmember", "x"}))
	s.SetPresence([]model.Status{{UserID: "u2", Status: "online"}})
	assert.Equal(t, map[string]string{"u2": "online"}, s.Presences([]string{"u2", "u3"}), "only known statuses")
}

// A ~channel link in a message resolves by the channel's URL name: the
// sidebar carries it for team channels (not DMs/GMs, whose names are ids).
func TestSidebarItemsCarryTheChannelSlug(t *testing.T) {
	s := newFixture()
	assert.Equal(t, "town-square", sidebarItem(s, "town").Slug)
	assert.Empty(t, sidebarItem(s, "dm2").Slug)
}

// Search hits name their channel the way in: takes it: a team channel by
// its slug, a DM by the partner's username, a GM by all its members'
// usernames (me too) — its display name without spaces. A channel the
// state does not hold is left out.
func TestChannelLabelsForSearch(t *testing.T) {
	s := newFixture()
	s.SetUsers([]model.User{{ID: "u2", Username: "bob", FirstName: "Bob"}})
	got := s.ChannelLabels([]string{"town", "dm2", "gm", "ghost"})
	assert.Equal(t, map[string]ChannelLabel{
		"town": {Name: "town-square", Display: "Town Square", Type: "O"},
		"dm2":  {Name: "bob", Display: "bob", Type: "D"},
		"gm":   {Name: "alice,bob,carol", Display: "alice, bob, carol", Type: "G"},
	}, got)
}

func TestDirectChannelsForSearch(t *testing.T) {
	s := newFixture()
	s.SetUsers([]model.User{{ID: "u2", Username: "bob"}})
	got := s.DirectChannels()
	assert.Equal(t, []DirectChannel{
		{ID: "dm2", Type: "D", PartnerID: "u2", Name: "bob", Display: "bob"},
		{ID: "dm3", Type: "D", PartnerID: "u3", Name: "carol", Display: "carol"},
		{ID: "gm", Type: "G", Name: "alice,bob,carol", Display: "alice, bob, carol"},
	}, got)
}

// The server cuts a GM's display name at 64 bytes: a name that long may be
// missing members, so it names no in: filter.
func TestGMSearchNameOfATruncatedDisplayName(t *testing.T) {
	assert.Equal(t, "a,b", gmSearchName("b, a"))
	long := strings.Repeat("abcdefghi, ", 6) // 66 bytes
	assert.Empty(t, gmSearchName(long[:64]))
	assert.Empty(t, gmSearchName(""))
}

// A post no window holds (a search hit) renders like the feed's: the
// author's name, picture version and status from the held profiles.
func TestPostViewsOfForeignPosts(t *testing.T) {
	s := newFixture()
	s.SetUsers([]model.User{{ID: "u2", Username: "bob", LastPictureUpdate: 7}})
	s.SetPresence([]model.Status{{UserID: "u2", Status: "online"}})
	vs := s.PostViews([]model.Post{{ID: "p1", UserID: "u2", ChannelID: "elsewhere", Message: "hi", CreateAt: 5, ReplyCount: 2}})
	assert.Equal(t, []PostView{{ID: "p1", UserID: "u2", Author: "bob", Avatar: "7", Status: "online", Message: "hi", CreateAt: 5, ReplyCount: 2}}, vs)
}

// Review (Codex) 2: a hit's own webhook picture would be served from
// /media/<srv>/posticon/<id>, which resolves held posts only — a hit is
// not held, so it shows the generic webhook icon (no URL in the DTO); an
// emoji icon needs no lookup and stays.
func TestPostViewsOfForeignWebhookPostsUseTheGenericIcon(t *testing.T) {
	s := newFixture()
	url := model.Post{ID: "p1", UserID: "u2", Props: model.PostProps{FromWebhook: true, OverrideIconURL: "https://gitlab.example/fox.png"}}
	emo := model.Post{ID: "p2", UserID: "u2", Props: model.PostProps{FromWebhook: true, OverrideIconEmoji: ":tada:"}}
	vs := s.PostViews([]model.Post{url, emo})
	assert.Equal(t, "webhook", vs[0].Icon)
	assert.Empty(t, vs[0].IconVersion)
	assert.Equal(t, ":tada:", vs[1].Icon)
	_, ok := s.PostIconURL("p1")
	assert.False(t, ok, "the media route would not find it")
}
