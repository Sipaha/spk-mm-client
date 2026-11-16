package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

func TestBadgeAndQuickChannelsSkipChannelsOfArchivedOrLeftTeams(t *testing.T) {
	b := fixture()
	// Make all active-team and DM channels read so only unreachable teams have unreads.
	for i := range b.Members {
		for _, ch := range b.Channels {
			if ch.ID == b.Members[i].ChannelID {
				b.Members[i].MsgCount = ch.TotalMsgCount
				b.Members[i].MsgCountRoot = ch.TotalMsgCountRoot
				b.Members[i].MentionCount = 0
				b.Members[i].MentionCountRoot = 0
			}
		}
	}
	// Mattermost /users/me/channels and /channel_members still return memberships
	// for channels in archived teams (DeleteAt != 0) or teams the user left.
	b.Teams = append(b.Teams, model.Team{ID: "t-arch", Name: "archived", DisplayName: "Archived Team", DeleteAt: 500})
	b.Channels = append(b.Channels,
		model.Channel{ID: "c-arch-team", TeamID: "t-arch", Type: model.ChannelOpen, Name: "old-general", DisplayName: "Old General", TotalMsgCount: 5, TotalMsgCountRoot: 5, LastPostAt: 900, CreateAt: 1},
		model.Channel{ID: "c-left-team", TeamID: "t-left", Type: model.ChannelPrivate, Name: "left-priv", DisplayName: "Left Private", TotalMsgCount: 3, TotalMsgCountRoot: 3, LastPostAt: 950, CreateAt: 1},
	)
	b.Members = append(b.Members,
		model.ChannelMember{ChannelID: "c-arch-team", UserID: "u1", MsgCount: 2, MsgCountRoot: 2, NotifyProps: notify()},
		model.ChannelMember{ChannelID: "c-left-team", UserID: "u1", MsgCount: 0, MsgCountRoot: 0, MentionCount: 2, MentionCountRoot: 2, NotifyProps: notify()},
	)

	s := New(fixedNow)
	s.Bootstrap(b)
	s.SetUsers([]model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob"}, {ID: "u3", Username: "carol"}})
	s.SetActive("dm2")

	sb := s.Sidebar("t1")
	require.Len(t, sb.Teams, 1, "single active team: team bar is hidden in UI")
	assert.False(t, sb.Teams[0].Unread)
	assert.Zero(t, sb.Teams[0].Mentions)
	for _, cat := range sb.Categories {
		for _, ch := range cat.Channels {
			assert.False(t, ch.Unread, "channel %s should be read", ch.ID)
		}
	}

	assert.Equal(t, Badge{}, s.Badge(), "unreachable archived/left team channels must not leave a stuck unread badge")

	for _, qc := range s.QuickChannels() {
		assert.NotEqual(t, "c-arch-team", qc.ID)
		assert.NotEqual(t, "c-left-team", qc.ID)
	}
}

func TestTeamLifecycleEventsRequestMetadataRefresh(t *testing.T) {
	s := newFixture()
	for _, typ := range []string{"added_to_team", "leave_team", "delete_team", "restore_team", "update_team"} {
		eff := s.ApplyEvent(ws.Event{Type: typ, Data: []byte(`{"team_id":"t1"}`)})
		assert.True(t, eff.NeedMeta, "%s must trigger metadata refresh", typ)
	}
}

func TestUserUpdatedMarksBadgeChangedForDeactivatedDMPartner(t *testing.T) {
	s := newFixture()
	s.ViewedLocally("off", 1000)
	require.Equal(t, Badge{Unread: true, Mentions: 1}, s.Badge(), "only dm2 (partner u2) is unread")

	db, err := json.Marshal(map[string]any{
		"user": model.User{ID: "u2", Username: "bob", DeleteAt: 999, UpdateAt: 999},
	})
	require.NoError(t, err)

	eff := s.ApplyEvent(ws.Event{Type: "user_updated", Data: db})
	assert.True(t, eff.Sidebar)
	assert.True(t, eff.Badge, "deactivating a DM partner changes Badge() and must notify badge subscribers")
	assert.Equal(t, Badge{}, s.Badge())
}

func TestVisibleDMsKeepsAllUnreadsWhenActiveDMIsRead(t *testing.T) {
	b := fixture()
	b.Prefs = append(b.Prefs, model.Preference{Category: "sidebar_settings", Name: "limit_visible_dms_gms", Value: "1"})
	// Make off and dm2 read; make gm have 1 plain unread message (Unread: true, Mentions: 0).
	for i := range b.Members {
		switch b.Members[i].ChannelID {
		case "off":
			b.Members[i].MsgCount, b.Members[i].MsgCountRoot = 7, 7
		case "dm2":
			b.Members[i].MsgCount, b.Members[i].MsgCountRoot = 5, 5
			b.Members[i].MentionCount, b.Members[i].MentionCountRoot = 0, 0
		case "gm":
			b.Members[i].MsgCount, b.Members[i].MsgCountRoot = 1, 1 // total is 2 -> 1 plain unread
		}
	}
	s := New(fixedNow)
	s.Bootstrap(b)
	s.SetUsers([]model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob"}, {ID: "u3", Username: "carol"}})
	s.SetActive("dm2") // active personal DM is read

	assert.Equal(t, Badge{Unread: true, Mentions: 0}, s.Badge())
	sb := s.Sidebar("t1")
	dmIDs := ids(sb.Categories[2].Channels)
	assert.ElementsMatch(t, []string{"dm2", "gm"}, dmIDs, "active read DM must not push unread GM past limit_visible_dms_gms")
}

func TestCategoriesFallbackPreservesUncategorizedChannelsAndDMs(t *testing.T) {
	b := fixture()
	b.Categories = map[string]model.OrderedCategories{
		"t1": {
			Categories: []model.SidebarCategory{
				{ID: "fav", Type: "favorites", DisplayName: "Favorites", ChannelIDs: []string{"town"}},
			},
			Order: []string{"fav"},
		},
	}
	s := New(fixedNow)
	s.Bootstrap(b)
	s.SetUsers([]model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob"}, {ID: "u3", Username: "carol"}})

	sb := s.Sidebar("t1")
	assert.Equal(t, "off", findItem(sb, "off").ID)
	assert.True(t, findItem(sb, "off").Unread)
	assert.Equal(t, "dm2", findItem(sb, "dm2").ID)
	assert.True(t, findItem(sb, "dm2").Unread)
}

func TestPostDeletedWithCRTThreadMentionsRequestsTotalsReread(t *testing.T) {
	s := crtCounts(map[string]int64{"t1": 1})
	_, _, tok := s.ThreadCountsFetch()

	eff := s.ApplyEvent(deletedEv(reply("r1", "root", "u2", 2000, 1)))
	assert.True(t, eff.RereadThreadCounts, "deleting a post while team has thread mentions must reread totals")
	_, retry := s.SetThreadMentions(map[string]int64{"t1": 1}, tok, false)
	assert.True(t, retry, "in-flight reread started before post_deleted must be unsettled")

	// When no thread mentions are held (and past guard), post_deleted avoids extra REST calls.
	clean := crtCounts(map[string]int64{"t1": 0})
	eff = clean.ApplyEvent(deletedEv(reply("r2", "root", "u2", 2100, 1)))
	assert.False(t, eff.RereadThreadCounts)
}

func TestReopeningActiveChannelClearsMarkUnreadSuppression(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetActive("dm2")
	s.ViewedLocally("dm2", 1000)
	require.False(t, s.NeedsView("dm2"))

	s.SetUnread(model.ChannelUnreadAt{ChannelID: "dm2", MsgCount: 4, MsgCountRoot: 4, MentionCount: 1, MentionCountRoot: 1, LastViewedAt: 10})
	assert.False(t, s.NeedsView("dm2"), "suppressed while user stays in channel without re-selecting")

	// Re-selecting the same active channel from Unreads clears suppressView.
	s.SetActive("dm2")
	assert.True(t, s.NeedsView("dm2"))
}
