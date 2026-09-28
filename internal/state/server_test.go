package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestUnreadNonCRTAndMuted(t *testing.T) {
	s := newFixture()
	s.mu.Lock()
	defer s.mu.Unlock()
	u, m := s.unreadLocked(s.chans["off"])
	assert.True(t, u)
	assert.Equal(t, 0, m)
	u, _ = s.unreadLocked(s.chans["muted"])
	assert.False(t, u, "muted: unread only on mentions")
	u, m = s.unreadLocked(s.chans["dm2"])
	assert.True(t, u)
	assert.Equal(t, 1, m)
	u, _ = s.unreadLocked(s.chans["town"])
	assert.False(t, u)
}

func TestUnreadCRTUsesRootCounters(t *testing.T) {
	b := fixture()
	b.Config.CollapsedThreads = "always_on"
	for i := range b.Channels {
		if b.Channels[i].ID == "town" {
			b.Channels[i].TotalMsgCount = 15 // 5 replies, roots unchanged
		}
	}
	s := New(fixedNow)
	s.Bootstrap(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	u, _ := s.unreadLocked(s.chans["town"])
	assert.False(t, u, "replies do not make a channel unread under CRT")
}

func TestCRTFromConfigAndPreference(t *testing.T) {
	cases := []struct {
		cfg, pref string
		want      bool
	}{
		{"disabled", "on", false}, {"", "on", false}, {"always_on", "off", true},
		{"default_on", "", true}, {"default_on", "off", false}, {"default_off", "", false}, {"default_off", "on", true},
	}
	for _, c := range cases {
		b := fixture()
		b.Config.CollapsedThreads = c.cfg
		if c.pref != "" {
			b.Prefs = append(b.Prefs, model.Preference{Category: "display_settings", Name: "collapsed_reply_threads", Value: c.pref})
		}
		s := New(fixedNow)
		s.Bootstrap(b)
		assert.Equal(t, c.want, s.CRT(), "%+v", c)
	}
}

func TestBootstrapCarriesFileLimits(t *testing.T) {
	s := newFixture()
	assert.Zero(t, s.MaxFileSize(), "unknown before any bootstrap")
	assert.False(t, s.FileAttachmentsEnabled())
	b := fixture()
	b.Config.MaxFileSize = 104857600
	b.Config.EnableFileAttachments = true
	s.Bootstrap(b)
	assert.Equal(t, int64(104857600), s.MaxFileSize())
	assert.True(t, s.FileAttachmentsEnabled())
}

func TestBadgeSkipsMutedAndArchived(t *testing.T) {
	s := newFixture()
	b := s.Badge()
	assert.True(t, b.Unread)
	assert.Equal(t, 1, b.Mentions)
}

// TestBadgeSkipsDeactivatedDMPartner: dm2's mention (u2) must not count once
// u2 is known to be deactivated (api-facts: team/server sums skip DMs with a
// deactivated user), but "off" still makes the badge unread.
func TestBadgeSkipsDeactivatedDMPartner(t *testing.T) {
	s := newFixture()
	s.SetUsers([]model.User{{ID: "u2", Username: "bob", DeleteAt: 1}})
	b := s.Badge()
	assert.True(t, b.Unread, "off is still unread")
	assert.Equal(t, 0, b.Mentions, "dm2's mention is excluded: its partner is deactivated")
}

// TestExcludedFromSumsLockedDeactivatedDMPartner exercises the shared helper
// excludedFromSumsLocked directly: it is what both Badge() and
// teamItemsLocked() (per-team TeamItem sums) rely on to skip a DM whose
// partner is deactivated, while an unloaded partner still counts.
func TestExcludedFromSumsLockedDeactivatedDMPartner(t *testing.T) {
	s := newFixture()
	s.mu.Lock()
	assert.False(t, s.excludedFromSumsLocked(s.chans["dm2"]), "partner loaded and active: counts")
	s.mu.Unlock()

	s.SetUsers([]model.User{{ID: "u2", Username: "bob", DeleteAt: 1}})
	s.mu.Lock()
	assert.True(t, s.excludedFromSumsLocked(s.chans["dm2"]), "partner deactivated: excluded")
	s.mu.Unlock()
}

func TestNameFormat(t *testing.T) {
	s := newFixture()
	sb := s.Sidebar("t1")
	assert.Equal(t, "bob", findItem(sb, "dm2").Name)
	b := fixture()
	b.Prefs = append(b.Prefs, model.Preference{Category: "display_settings", Name: "name_format", Value: "full_name"})
	s.Bootstrap(b)
	assert.Equal(t, "Bob Brown", findItem(s.Sidebar("t1"), "dm2").Name)
}

func TestMissingUsers(t *testing.T) {
	s := New(fixedNow)
	s.Bootstrap(fixture())
	assert.ElementsMatch(t, []string{"u2", "u3"}, s.MissingUserIDs())
	s.SetUsers([]model.User{{ID: "u2"}, {ID: "u3"}})
	assert.Empty(t, s.MissingUserIDs())
}

func TestBootstrapDropsChannelsWeLeft(t *testing.T) {
	s := newFixture()
	b := fixture()
	b.Members = b.Members[1:] // left "town"
	s.Bootstrap(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.chans["town"]
	require.False(t, ok)
	_, ok = s.chans["notmember"]
	assert.False(t, ok, "channels without membership are ignored")
}

func findItem(sb SidebarView, id string) ChannelItem {
	for _, c := range sb.Categories {
		for _, it := range c.Channels {
			if it.ID == id {
				return it
			}
		}
	}
	return ChannelItem{}
}

func TestRefreshUsersKeepsNewerProfilesAndReportsChanges(t *testing.T) {
	s := newFixture()
	s.SetUsers([]model.User{{ID: "u3", Username: "carol", UpdateAt: 50}})
	assert.Equal(t, []string{"u1", "u2", "u3"}, s.KnownUserIDs())
	assert.False(t, s.RefreshUsers([]model.User{{ID: "u1", Username: "alice"}}), "nothing shown changed")
	assert.True(t, s.RefreshUsers([]model.User{{ID: "u2", Username: "bob", FirstName: "Bob", LastName: "Brown", LastPictureUpdate: 9, UpdateAt: 10}}))
	assert.Equal(t, "9", s.avatarLocked("u2"))
	assert.False(t, s.RefreshUsers([]model.User{{ID: "u3", Username: "old-carol", UpdateAt: 40}}), "an older read does not undo a newer event")
	assert.False(t, s.RefreshUsers([]model.User{{ID: "u8", Username: "stranger"}}), "only users we know")
	assert.Equal(t, []string{"u1", "u2", "u3"}, s.KnownUserIDs())
}

// Spike §4.1 п.5: Bootstrap reports a CRT change against what was held —
// a live session or a snapshot saved in the other mode — so the worker can
// drop the windows that hold the wrong kind of posts.
func TestBootstrapReportsCRTChange(t *testing.T) {
	crtOn := fixture()
	crtOn.Config.CollapsedThreads = "always_on"
	byPref := fixture()
	byPref.Config.CollapsedThreads = "default_off"
	byPref.Prefs = append(byPref.Prefs, model.Preference{Category: "display_settings", Name: "collapsed_reply_threads", Value: "on"})

	s := New(fixedNow)
	assert.False(t, s.Bootstrap(fixture()), "the first bootstrap has nothing to compare with")
	assert.False(t, s.Bootstrap(fixture()), "same mode")
	assert.True(t, s.Bootstrap(crtOn), "admin turned CRT on")
	assert.False(t, s.Bootstrap(byPref), "on by preference is still on")
	assert.True(t, s.Bootstrap(fixture()), "off again")

	put, _ := s.TakeSnapshot() // saved with CRT off
	r := New(fixedNow)
	require.NoError(t, r.Restore(put))
	assert.True(t, r.Bootstrap(crtOn), "a snapshot saved in the other mode counts")
}
