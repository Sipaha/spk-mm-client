package state

import (
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
