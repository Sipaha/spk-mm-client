package state

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func TestSidebarCategoriesVisibilityAndOrder(t *testing.T) {
	sb := newFixture().Sidebar("t1")
	assert.Equal(t, "t1", sb.TeamID)
	require.Len(t, sb.Categories, 3)
	assert.Equal(t, []string{"off"}, ids(sb.Categories[0].Channels))
	assert.Equal(t, []string{"town", "muted"}, ids(sb.Categories[1].Channels), "alpha, muted last, archived hidden")
	assert.Equal(t, []string{"dm2", "gm"}, ids(sb.Categories[2].Channels), "recent; dm3 closed by preference")
	off := sb.Categories[0].Channels[0]
	assert.True(t, off.Unread)
	muted := sb.Categories[1].Channels[1]
	assert.True(t, muted.Muted)
	assert.False(t, muted.Unread)
}

// TestChannelItemLastActivityAt: the frontend's Unreads section (sidebar-
// sections-brief.md) sorts by recency the same way the webapp's
// sortUnreadChannels does (mattermost-redux channels.ts) — last_root_post_at
// under CRT if set, else last_post_at, never older than create_at. The
// fixture's channels all set LastPostAt == LastRootPostAt == last argument
// and CreateAt: 1, so with CRT disabled (fixture's default) the expected
// value is just that last-post argument.
func TestChannelItemLastActivityAt(t *testing.T) {
	sb := newFixture().Sidebar("t1")
	fav := sb.Categories[0].Channels[0]
	require.Equal(t, "off", fav.ID)
	assert.Equal(t, int64(300), fav.LastActivityAt, "off: ch(..., 300)")

	var town ChannelItem
	for _, c := range sb.Categories[1].Channels {
		if c.ID == "town" {
			town = c
		}
	}
	assert.Equal(t, int64(400), town.LastActivityAt, "town: ch(..., 400)")
}

func TestSidebarTeamsAggregateWithoutDMs(t *testing.T) {
	sb := newFixture().Sidebar("")
	require.Len(t, sb.Teams, 1)
	assert.True(t, sb.Teams[0].Unread, "off is unread")
	assert.Equal(t, 0, sb.Teams[0].Mentions, "dm2 mention is not a team mention")
}

// TestTeamItemSkipsDeactivatedDMPartner: teamItemsLocked shares
// excludedFromSumsLocked with Badge, so a DM whose partner is deactivated
// must not contribute to the TeamItem sums either. Real DM channels always
// carry an empty team_id (so they never reach this loop), but the shared
// helper must behave identically regardless of call site.
func TestTeamItemSkipsDeactivatedDMPartner(t *testing.T) {
	b := fixture()
	b.Channels = append(b.Channels, model.Channel{ID: "dmT", TeamID: "t1", Type: model.ChannelDirect, Name: "u1__u4", TotalMsgCount: 2})
	b.Members = append(b.Members, model.ChannelMember{ChannelID: "dmT", UserID: "u1", MentionCount: 2, NotifyProps: notify()})
	s := New(fixedNow)
	s.Bootstrap(b)
	s.SetUsers([]model.User{{ID: "u4", Username: "dave"}})

	sb := s.Sidebar("")
	require.Len(t, sb.Teams, 1)
	assert.Equal(t, 2, sb.Teams[0].Mentions, "active partner: dmT's mentions count")

	s.SetUsers([]model.User{{ID: "u4", Username: "dave", DeleteAt: 1}})
	sb = s.Sidebar("")
	assert.Equal(t, 0, sb.Teams[0].Mentions, "deactivated partner: dmT is excluded")
}

func TestUncategorizedChannelsAreAppended(t *testing.T) {
	b := fixture()
	b.Channels = append(b.Channels, model.Channel{ID: "new", TeamID: "t1", Type: model.ChannelOpen, DisplayName: "Brand New"})
	b.Members = append(b.Members, model.ChannelMember{ChannelID: "new", UserID: "u1"})
	s := New(fixedNow)
	s.Bootstrap(b)
	assert.Contains(t, ids(s.Sidebar("t1").Categories[1].Channels), "new")
}

func TestMissingCategoriesAreSynthesized(t *testing.T) {
	b := fixture()
	b.Categories = nil
	s := New(fixedNow)
	s.Bootstrap(b)
	sb := s.Sidebar("t1")
	require.Len(t, sb.Categories, 2)
	assert.Equal(t, "channels", sb.Categories[0].Type)
	assert.Equal(t, "direct_messages", sb.Categories[1].Type)
	assert.ElementsMatch(t, []string{"town", "off", "muted"}, ids(sb.Categories[0].Channels))
}

func TestNaturalAlphaSort(t *testing.T) {
	b := fixture()
	b.Categories["t1"].Categories[1].ChannelIDs = []string{"c10", "c2", "c1"}
	for _, n := range []string{"c10", "c2", "c1"} {
		b.Channels = append(b.Channels, model.Channel{ID: n, TeamID: "t1", Type: model.ChannelOpen, DisplayName: "Ch " + n[1:]})
		b.Members = append(b.Members, model.ChannelMember{ChannelID: n, UserID: "u1"})
	}
	s := New(fixedNow)
	s.Bootstrap(b)
	// town/muted are not in the category any more → appended as uncategorized, sorted after
	assert.Equal(t, []string{"c1", "c2", "c10"}, ids(s.Sidebar("t1").Categories[1].Channels)[:3])
}

func TestManualSortKeepsServerOrder(t *testing.T) {
	b := fixture()
	b.Categories["t1"].Categories[1].Sorting = "manual"
	b.Categories["t1"].Categories[1].ChannelIDs = []string{"muted", "town"}
	s := New(fixedNow)
	s.Bootstrap(b)
	assert.Equal(t, []string{"muted", "town"}, ids(s.Sidebar("t1").Categories[1].Channels))
}

func TestDMLimitKeepsUnreadAndMostRecentlyViewed(t *testing.T) {
	b := fixture()
	b.Prefs = append(b.Prefs, model.Preference{Category: "sidebar_settings", Name: "limit_visible_dms_gms", Value: "2"})
	var dmIDs []string
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("dx%d", i)
		dmIDs = append(dmIDs, id)
		other := fmt.Sprintf("ux%d", i)
		b.Channels = append(b.Channels, model.Channel{ID: id, Type: model.ChannelDirect, Name: "u1__" + other, TotalMsgCount: 1, LastPostAt: int64(10 + i)})
		b.Members = append(b.Members, model.ChannelMember{ChannelID: id, UserID: "u1", MsgCount: 1, LastViewedAt: int64(100 + i)})
		b.Prefs = append(b.Prefs, model.Preference{Category: "direct_channel_show", Name: other, Value: "true"})
	}
	b.Categories["t1"].Categories[2].ChannelIDs = append([]string{"dm2", "dm3", "gm"}, dmIDs...)
	s := New(fixedNow)
	s.Bootstrap(b)
	got := ids(s.Sidebar("t1").Categories[2].Channels)
	// dm2 (unread) + the most recently viewed read one (dx3); limit = max(2, unread count 1) = 2
	assert.ElementsMatch(t, []string{"dm2", "dx3"}, got)
}

func TestSelectedChannelDefaultsAndSticks(t *testing.T) {
	s := newFixture()
	assert.Equal(t, "town", s.Sidebar("t1").SelectedChannelID, "no history: the team's town-square-like first channel")
	s.mu.Lock()
	s.nav.Channel = map[string]string{"t1": "off"}
	s.mu.Unlock()
	assert.Equal(t, "off", s.Sidebar("t1").SelectedChannelID)
}
