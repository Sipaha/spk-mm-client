package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/model"
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

func TestBadgeSkipsMutedAndArchived(t *testing.T) {
	s := newFixture()
	b := s.Badge()
	assert.True(t, b.Unread)
	assert.Equal(t, 1, b.Mentions)
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
