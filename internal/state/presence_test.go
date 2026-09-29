package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

func statusEv(uid, st string) ws.Event {
	d, _ := json.Marshal(map[string]any{"user_id": uid, "status": st})
	return ws.Event{Type: "status_change", Data: d, Broadcast: ws.Broadcast{UserID: uid}}
}

func sidebarItem(s *Server, id string) ChannelItem {
	for _, c := range s.Sidebar("t1").Categories {
		for _, it := range c.Channels {
			if it.ID == id {
				return it
			}
		}
	}
	return ChannelItem{}
}

func TestStatusTargetsAreMeShownDMPartnersAndActiveAuthors(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetUsers([]model.User{{ID: "u9", Username: "ci", IsBot: true}})
	s.SetWindow("town", []model.Post{mkPost("a", "town", "u3", 1000), mkPost("b", "town", "u9", 1001), mkPost("c", "town", "u2", 1002)}, true, 5, 0)
	s.SetActive("town")
	assert.Equal(t, []string{"u1", "u2", "u3"}, s.StatusTargets(100), "bots have no status")
	s.SetActive("off")
	assert.Equal(t, []string{"u1", "u2"}, s.StatusTargets(100), "dm3 is closed (direct_channel_show=false), u3 is not shown")
	assert.Equal(t, []string{"u1"}, s.StatusTargets(1), "me first, then the cap")
}

func TestPresenceShowsInPostsAndDMRows(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetUsers([]model.User{{ID: "u2", Username: "bob", LastPictureUpdate: 77}})
	s.SetWindow("dm2", []model.Post{mkPost("p", "dm2", "u2", 1000)}, true, 5, 0)
	s.SetActive("dm2")
	ch := s.SetPresence([]model.Status{{UserID: "u2", Status: "away"}, {UserID: "u1", Status: "dnd", DNDEndTime: 9}})
	assert.Equal(t, Change{Sidebar: true, Channels: []string{"dm2"}}, ch)

	v, _ := s.ChannelView("dm2")
	require.Len(t, v.Posts, 1)
	assert.Equal(t, "77", v.Posts[0].Avatar)
	assert.Equal(t, "away", v.Posts[0].Status)
	assert.Equal(t, ChannelItem{ID: "dm2", Name: "bob", Type: "D", Unread: true, Mentions: 1, LastActivityAt: 500, UserID: "u2", Avatar: "77", Status: "away"}, sidebarItem(s, "dm2"))
	assert.Equal(t, ChannelItem{ID: "town", Name: "Town Square", Type: "O", LastActivityAt: 400, Slug: "town-square"}, sidebarItem(s, "town"), "channels carry no user")

	s.mu.Lock()
	assert.Equal(t, model.Status{Status: "dnd", DNDEndTime: 9}, model.Status{Status: s.status.Status, DNDEndTime: s.status.DNDEndTime}, "my own status feeds the DND check of notifications")
	s.mu.Unlock()
	assert.True(t, s.SetPresence([]model.Status{{UserID: "u2", Status: "away"}}).Empty(), "no change, no refresh")
}

func TestUnknownAuthorHasNoAvatarAndBotsNoStatus(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetUsers([]model.User{{ID: "u9", Username: "ci", IsBot: true}})
	s.SetWindow("town", []model.Post{mkPost("x", "town", "u404", 1000), mkPost("y", "town", "u9", 1001)}, true, 5, 0)
	s.SetPresence([]model.Status{{UserID: "u404", Status: "online"}, {UserID: "u9", Status: "online"}})
	v, _ := s.ChannelView("town")
	assert.Empty(t, v.Posts[0].Avatar, "profile not loaded: initials, no request")
	assert.Equal(t, "online", v.Posts[0].Status)
	assert.Equal(t, "0", v.Posts[1].Avatar)
	assert.Empty(t, v.Posts[1].Status, "bots have no presence")
}

func TestStatusChangeEventUpdatesPresence(t *testing.T) {
	s := newFixture()
	s.SetActive("town")
	eff := s.ApplyEvent(statusEv("u1", "away"))
	assert.True(t, eff.Sidebar)
	assert.Equal(t, []string{"town"}, eff.Channels)
	s.mu.Lock()
	assert.Equal(t, "away", s.status.Status)
	assert.Equal(t, "away", s.presence["u1"])
	s.mu.Unlock()
	assert.True(t, s.ApplyEvent(statusEv("u1", "away")).Empty())
}

func TestAvatarVersionFollowsUserUpdated(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("dm2", []model.Post{mkPost("p", "dm2", "u2", 1000)}, true, 5, 0)
	v, _ := s.ChannelView("dm2")
	assert.Equal(t, "0", v.Posts[0].Avatar)
	d, _ := json.Marshal(map[string]any{"user": map[string]any{"id": "u2", "username": "bob", "last_picture_update": -42}})
	s.ApplyEvent(ws.Event{Type: "user_updated", Data: d})
	v, _ = s.ChannelView("dm2")
	assert.Equal(t, "-42", v.Posts[0].Avatar, "a reset picture has a negative version")
	assert.Equal(t, "-42", sidebarItem(s, "dm2").Avatar)
}

func TestRestoreSeedsMyPresence(t *testing.T) {
	s := newFixture()
	s.SetPresence([]model.Status{{UserID: "u1", Status: "dnd"}})
	put, _ := s.TakeSnapshot()
	r := New(fixedNow)
	require.NoError(t, r.Restore(put))
	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Equal(t, "dnd", r.status.Status)
	assert.Equal(t, "dnd", r.presence["u1"], "my own status shows before the first poll, as after Bootstrap")
	assert.Len(t, r.presence, 1, "others' presence is not in the snapshot")
}
