package state

import (
	"time"

	"github.com/spk/spk-mattermost/internal/mm/model"
)

var t0 = time.UnixMilli(1_000_000)

func fixedNow() time.Time { return t0 }

func notify(extra ...string) map[string]string {
	m := map[string]string{"desktop": "default", "mark_unread": "all"}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	return m
}

// fixture: me=u1 in team t1.
//   town   O read              off   O 2 unread (favorite)
//   muted  O 3 unread, muted   arch  O archived
//   dm2    D u2, 1 unread (mention)   dm3 D u3, read, direct_channel_show=false
//   gm     G read, group_channel_show=true
func fixture() Bootstrap {
	ch := func(id, typ, name string, total int64, lastPost int64) model.Channel {
		team := "t1"
		if typ == model.ChannelDirect || typ == model.ChannelGroup {
			team = ""
		}
		slug := id
		if id == "town" {
			slug = "town-square"
		}
		return model.Channel{ID: id, TeamID: team, Type: typ, DisplayName: name, Name: slug,
			TotalMsgCount: total, TotalMsgCountRoot: total, LastPostAt: lastPost, LastRootPostAt: lastPost, CreateAt: 1}
	}
	mem := func(id string, read int64, mentions int64, np map[string]string) model.ChannelMember {
		return model.ChannelMember{ChannelID: id, UserID: "u1", MsgCount: read, MsgCountRoot: read,
			MentionCount: mentions, MentionCountRoot: mentions, NotifyProps: np, LastViewedAt: 10}
	}
	dm2 := ch("dm2", model.ChannelDirect, "", 5, 500)
	dm2.Name = "u1__u2"
	dm3 := ch("dm3", model.ChannelDirect, "", 1, 100)
	dm3.Name = "u3__u1"
	arch := ch("arch", model.ChannelOpen, "Archived", 1, 50)
	arch.DeleteAt = 99
	return Bootstrap{
		Me:     model.User{ID: "u1", Username: "alice"},
		Config: Config{CollapsedThreads: "disabled", TeammateNameDisplay: "username"},
		Prefs: []model.Preference{
			{Category: "direct_channel_show", Name: "u3", Value: "false"},
			{Category: "group_channel_show", Name: "gm", Value: "true"},
		},
		Teams: []model.Team{{ID: "t1", Name: "team", DisplayName: "Team"}},
		Channels: []model.Channel{
			ch("town", model.ChannelOpen, "Town Square", 10, 400), ch("off", model.ChannelOpen, "Off-Topic", 7, 300),
			ch("muted", model.ChannelOpen, "Muted", 3, 200), arch, dm2, dm3, ch("gm", model.ChannelGroup, "alice, bob, carol", 2, 150),
			ch("notmember", model.ChannelOpen, "Not a member", 1, 1),
		},
		Members: []model.ChannelMember{
			mem("town", 10, 0, notify()), mem("off", 5, 0, notify()), mem("muted", 0, 0, notify("mark_unread", "mention")),
			mem("arch", 1, 0, notify()), mem("dm2", 4, 1, notify()), mem("dm3", 1, 0, notify()), mem("gm", 2, 0, notify()),
		},
		Categories: map[string]model.OrderedCategories{"t1": {
			Categories: []model.SidebarCategory{
				{ID: "fav", Type: "favorites", DisplayName: "Favorites", ChannelIDs: []string{"off"}},
				{ID: "chs", Type: "channels", DisplayName: "Channels", Sorting: "alpha", ChannelIDs: []string{"town", "muted", "arch"}},
				{ID: "dms", Type: "direct_messages", DisplayName: "Direct Messages", Sorting: "recent", ChannelIDs: []string{"dm2", "dm3", "gm"}},
			},
			Order: []string{"fav", "chs", "dms"},
		}},
	}
}

func newFixture() *Server {
	s := New(fixedNow)
	s.Bootstrap(fixture())
	s.SetUsers([]model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob", FirstName: "Bob", LastName: "Brown"}, {ID: "u3", Username: "carol"}})
	return s
}

func ids(items []ChannelItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}
