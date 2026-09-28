package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

func counts(s *Server, id string) (model.Channel, model.ChannelMember) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chans[id].Info, s.chans[id].Member
}

func TestPostedBumpsCountsAndInsertsIntoLoadedWindow(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetWindow("town", nil, true, 5)
	eff := s.ApplyEvent(postedEv(mkPost("p1", "town", "u2", 5000), "u1"))
	info, m := counts(s, "town")
	assert.Equal(t, int64(11), info.TotalMsgCount)
	assert.Equal(t, int64(1), m.MentionCount)
	assert.Equal(t, int64(5000), info.LastPostAt)
	assert.Equal(t, []string{"p1"}, windowIDs(s, "town"))
	assert.True(t, eff.Sidebar)
	assert.True(t, eff.Badge)
	assert.Equal(t, []string{"town"}, eff.Channels)
	require.NotNil(t, eff.Notify)
	assert.Equal(t, "Town Square", eff.Notify.ChannelName)
}

func TestPostedTwiceIsIdempotent(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	p := mkPost("p1", "off", "u2", 5000)
	s.ApplyEvent(postedEv(p))
	eff := s.ApplyEvent(postedEv(p))
	info, _ := counts(s, "off")
	assert.Equal(t, int64(8), info.TotalMsgCount)
	assert.Nil(t, eff.Notify, "a replay does not notify twice")
}

func TestPostedDuringBootstrapNotDoubleCounted(t *testing.T) {
	s := newFixture() // guard active: off.last_post_at = 300 from REST
	s.ApplyEvent(postedEv(mkPost("early", "off", "u2", 300)))
	info, _ := counts(s, "off")
	assert.Equal(t, int64(7), info.TotalMsgCount, "REST already counted it")
	s.ApplyEvent(postedEv(mkPost("late", "off", "u2", 301)))
	info, _ = counts(s, "off")
	assert.Equal(t, int64(8), info.TotalMsgCount)
	s.ClearGuard()
	s.ApplyEvent(postedEv(mkPost("x", "off", "u2", 200))) // clock skew after the guard: still new
	info, _ = counts(s, "off")
	assert.Equal(t, int64(9), info.TotalMsgCount)
}

func TestOwnPostMarksChannelRead(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	eff := s.ApplyEvent(postedEv(mkPost("mine", "off", "u1", 5000)))
	info, m := counts(s, "off")
	assert.Equal(t, info.TotalMsgCount, m.MsgCount)
	assert.Nil(t, eff.Notify)
}

func TestCRTReplyUpdatesRootNotFeed(t *testing.T) {
	b := fixture()
	b.Config.CollapsedThreads = "always_on"
	s := New(fixedNow)
	s.Bootstrap(b)
	s.ClearGuard()
	s.SetWindow("town", []model.Post{mkPost("root", "town", "u2", 1000)}, true, 5)
	reply := mkPost("r1", "town", "u3", 2000)
	reply.RootID = "root"
	s.ApplyEvent(postedEv(reply, "u1"))
	assert.Equal(t, []string{"root"}, windowIDs(s, "town"))
	s.mu.Lock()
	assert.Equal(t, int64(1), s.chans["town"].Win.Posts[0].ReplyCount)
	s.mu.Unlock()
	info, m := counts(s, "town")
	assert.Equal(t, int64(10), info.TotalMsgCountRoot)
	assert.Equal(t, int64(0), m.MentionCountRoot, "reply mentions are thread mentions, not channel ones")
	assert.Equal(t, int64(1), m.MentionCount)
}

func TestPendingReplacedByEchoEitherOrder(t *testing.T) {
	for _, echoFirst := range []bool{false, true} {
		s := newFixture()
		s.ClearGuard()
		s.SetWindow("off", nil, true, 5)
		pd := s.AddPending("off", "", "hello")
		assert.Contains(t, pd.ID, "u1:")
		v, _ := s.ChannelView("off")
		require.Len(t, v.Posts, 1)
		assert.True(t, v.Posts[0].Pending)

		p := mkPost("real", "off", "u1", 6000)
		p.PendingPostID = pd.ID
		if echoFirst {
			s.ApplyEvent(postedEv(p))
			s.PostCreated(p)
		} else {
			s.PostCreated(p)
			s.ApplyEvent(postedEv(p))
		}
		v, _ = s.ChannelView("off")
		require.Len(t, v.Posts, 1, "echoFirst=%v", echoFirst)
		assert.Equal(t, "real", v.Posts[0].ID)
		assert.False(t, v.Posts[0].Pending)
		info, _ := counts(s, "off")
		assert.Equal(t, int64(8), info.TotalMsgCount, "counted once")
	}
}

func TestFailRetryDropPending(t *testing.T) {
	s := newFixture()
	pd := s.AddPending("off", "", "hello")
	s.FailPending("off", pd.ID)
	v, _ := s.ChannelView("off")
	assert.True(t, v.Posts[0].Failed)
	again, ok := s.RetryPending("off", pd.ID)
	require.True(t, ok)
	assert.Equal(t, pd.ID, again.ID, "same pending id → the server dedupes")
	v, _ = s.ChannelView("off")
	assert.False(t, v.Posts[0].Failed)
	s.DropPending("off", pd.ID)
	v, _ = s.ChannelView("off")
	assert.Empty(t, v.Posts)
}

func TestEditDeleteReactionEvents(t *testing.T) {
	s := newFixture()
	s.SetWindow("off", []model.Post{mkPost("a", "off", "u2", 1000), mkPost("b", "off", "u2", 2000)}, true, 5)
	e := mkPost("a", "off", "u2", 1000)
	e.Message, e.EditAt, e.UpdateAt = "changed", 3000, 3000
	eff := s.ApplyEvent(postEv("post_edited", e))
	assert.Equal(t, []string{"off"}, eff.Channels)
	s.ApplyEvent(postEv("post_deleted", mkPost("b", "off", "u2", 2000)))
	assert.Equal(t, []string{"a"}, windowIDs(s, "off"))

	rb, _ := json.Marshal(model.Reaction{UserID: "u1", PostID: "a", EmojiName: "+1"})
	db, _ := json.Marshal(map[string]string{"reaction": string(rb)})
	s.ApplyEvent(ws.Event{Type: "reaction_added", Data: db, Broadcast: ws.Broadcast{ChannelID: "off"}})
	s.ApplyEvent(ws.Event{Type: "reaction_added", Data: db, Broadcast: ws.Broadcast{ChannelID: "off"}}) // duplicate
	v, _ := s.ChannelView("off")
	assert.Equal(t, "changed", v.Posts[0].Message)
	assert.Equal(t, []ReactionView{{Emoji: "+1", Count: 1, Mine: true}}, v.Posts[0].Reactions)
	s.ApplyEvent(ws.Event{Type: "reaction_removed", Data: db, Broadcast: ws.Broadcast{ChannelID: "off"}})
	v, _ = s.ChannelView("off")
	assert.Empty(t, v.Posts[0].Reactions)
}

func TestViewedEventsAndPostUnread(t *testing.T) {
	s := newFixture()
	db, _ := json.Marshal(map[string]any{"channel_times": map[string]int64{"off": 777, "dm2": 778}})
	s.ApplyEvent(ws.Event{Type: "multiple_channels_viewed", Data: db})
	b := s.Badge()
	assert.False(t, b.Unread)
	assert.Zero(t, b.Mentions)
	_, m := counts(s, "off")
	assert.Equal(t, int64(777), m.LastViewedAt)

	ub, _ := json.Marshal(map[string]any{"msg_count": 3, "msg_count_root": 3, "mention_count": 1, "mention_count_root": 1, "last_viewed_at": 5})
	s.ApplyEvent(ws.Event{Type: "post_unread", Data: ub, Broadcast: ws.Broadcast{ChannelID: "off", TeamID: "t1"}})
	_, m = counts(s, "off")
	assert.Equal(t, int64(3), m.MsgCount)
	assert.Equal(t, 1, s.Badge().Mentions)
}

func TestActiveFocusedChannelRequestsViewUnlessMarkedUnread(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	s.SetActive("off")
	s.SetFocused(true)
	eff := s.ApplyEvent(postedEv(mkPost("p1", "off", "u2", 5000)))
	assert.Equal(t, "off", eff.View)
	assert.True(t, s.NeedsView("off"))
	s.ViewedLocally("off", 5001)
	assert.False(t, s.NeedsView("off"))

	s.SetUnread(model.ChannelUnreadAt{ChannelID: "off", MsgCount: 1, MsgCountRoot: 1, LastViewedAt: 1})
	eff = s.ApplyEvent(postedEv(mkPost("p2", "off", "u2", 6000)))
	assert.Empty(t, eff.View, "marked unread by the user: stay unread until they leave")
	assert.False(t, s.NeedsView("off"))
	s.SetActive("town")
	assert.True(t, s.NeedsView("off"), "leaving clears the suppression")

	s.SetFocused(false)
	s.SetActive("off")
	eff = s.ApplyEvent(postedEv(mkPost("p3", "off", "u2", 7000)))
	assert.Empty(t, eff.View, "window not focused")
}

func TestMembershipAndPreferenceEvents(t *testing.T) {
	s := newFixture()
	s.SetActive("off")
	db, _ := json.Marshal(map[string]string{"channel_id": "off", "remover_id": "u2"})
	eff := s.ApplyEvent(ws.Event{Type: "user_removed", Data: db, Broadcast: ws.Broadcast{UserID: "u1"}})
	assert.True(t, eff.Sidebar)
	assert.Equal(t, []string{"off"}, eff.Channels)
	_, ok := s.ChannelView("off")
	assert.False(t, ok)

	db, _ = json.Marshal(map[string]string{"user_id": "u1", "team_id": "t1"})
	assert.True(t, s.ApplyEvent(ws.Event{Type: "user_added", Data: db, Broadcast: ws.Broadcast{ChannelID: "new", UserID: "u1"}}).NeedMeta)
	db, _ = json.Marshal(map[string]string{"user_id": "u9", "team_id": "t1"})
	assert.False(t, s.ApplyEvent(ws.Event{Type: "user_added", Data: db, Broadcast: ws.Broadcast{ChannelID: "town"}}).NeedMeta)
	assert.True(t, s.ApplyEvent(ws.Event{Type: "direct_added", Data: []byte(`{}`)}).NeedMeta)

	pb, _ := json.Marshal([]model.Preference{{Category: "display_settings", Name: "collapsed_reply_threads", Value: "on"}})
	db, _ = json.Marshal(map[string]string{"preferences": string(pb)})
	b := fixture()
	b.Config.CollapsedThreads = "default_off"
	s.Bootstrap(b)
	eff = s.ApplyEvent(ws.Event{Type: "preferences_changed", Data: db})
	assert.True(t, eff.Resync, "CRT toggled: windows hold the wrong kind of posts")
	assert.True(t, s.CRT())
}

// TestFlaggedPostPrefEventMarksTheChannelChanged mirrors how a reaction
// event does it (AGENTS.md/task-3-brief: "как делают реакции") — a
// flagged_post preference_changed/preferences_deleted event echoes another
// device's Save/Unsave and must repaint the post's channel, not just the
// sidebar/badge every preference change already does.
func TestFlaggedPostPrefEventMarksTheChannelChanged(t *testing.T) {
	s := newFixture()
	withPost(s)
	pb, _ := json.Marshal([]model.Preference{{UserID: "u1", Category: "flagged_post", Name: "p", Value: "true"}})
	db, _ := json.Marshal(map[string]string{"preferences": string(pb)})
	eff := s.ApplyEvent(ws.Event{Type: "preferences_changed", Data: db})
	assert.Equal(t, []string{"off"}, eff.Channels)
	v, _ := s.ChannelView("off")
	require.Len(t, v.Posts, 1)
	assert.True(t, v.Posts[0].Saved)

	eff = s.ApplyEvent(ws.Event{Type: "preferences_deleted", Data: db})
	assert.Equal(t, []string{"off"}, eff.Channels)
	v, _ = s.ChannelView("off")
	assert.False(t, v.Posts[0].Saved)

	// A flagged_post pref for a post we don't hold: no channel to repaint,
	// but the sidebar/badge still refresh like any preference change.
	pb, _ = json.Marshal([]model.Preference{{UserID: "u1", Category: "flagged_post", Name: "elsewhere", Value: "true"}})
	db, _ = json.Marshal(map[string]string{"preferences": string(pb)})
	eff = s.ApplyEvent(ws.Event{Type: "preferences_changed", Data: db})
	assert.Empty(t, eff.Channels)
	assert.True(t, eff.Sidebar)
}

func TestPostInHiddenDMAsksToShowIt(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	eff := s.ApplyEvent(postedEv(mkPost("d1", "dm3", "u3", 5000)))
	require.NotNil(t, eff.ShowDM)
	assert.Equal(t, model.Preference{UserID: "u1", Category: "direct_channel_show", Name: "u3", Value: "true"}, *eff.ShowDM)
	s.ViewedLocally("dm3", 5001)
	assert.Contains(t, ids(s.Sidebar("t1").Categories[2].Channels), "dm3", "shown optimistically")
}

func TestUnknownChannelPostAsksForMeta(t *testing.T) {
	s := newFixture()
	eff := s.ApplyEvent(postedEv(mkPost("x", "brand-new", "u2", 5000)))
	assert.True(t, eff.NeedMeta)
}

// Fix round 1, finding 1(a): a prefetch page can land a post in the window
// before its own "posted" event arrives (REST-before-WS race). SetWindow
// must not mark the post seen — only the event applying it may, so the
// event still bumps counters and produces a notification.
func TestPostedAfterPageAlreadyContainsItStillBumpsCounters(t *testing.T) {
	s := newFixture()
	s.ClearGuard()
	p := mkPost("p1", "off", "u2", 5000)
	s.SetWindow("off", []model.Post{p}, true, 5) // prefetch already landed p1
	assert.Equal(t, []string{"p1"}, windowIDs(s, "off"))
	eff := s.ApplyEvent(postedEv(p))
	info, _ := counts(s, "off")
	assert.Equal(t, int64(8), info.TotalMsgCount, "the event still counts p1 once")
	assert.Equal(t, []string{"p1"}, windowIDs(s, "off"), "no duplicate in the window")
	require.NotNil(t, eff.Notify, "the event still notifies")
}

// Fix round 1, finding 1: root ReplyCount/LastReplyAt bump. A CRT root
// fetched via REST already reflects a reply in its own ReplyCount/LastReplyAt
// fields; the reply's own "posted" event must not bump it again.
func TestCRTReplyAlreadyReflectedByRESTDoesNotDoubleBump(t *testing.T) {
	b := fixture()
	b.Config.CollapsedThreads = "always_on"
	s := New(fixedNow)
	s.Bootstrap(b)
	s.ClearGuard()
	root := mkPost("root", "town", "u2", 1000)
	root.ReplyCount, root.LastReplyAt = 1, 2000 // REST already counted this reply
	s.SetWindow("town", []model.Post{root}, true, 5)
	reply := mkPost("r1", "town", "u3", 2000)
	reply.RootID = "root"
	s.ApplyEvent(postedEv(reply, "u1"))
	s.mu.Lock()
	got := s.chans["town"].Win.Posts[0]
	s.mu.Unlock()
	assert.Equal(t, int64(1), got.ReplyCount, "not double-bumped")
	assert.Equal(t, int64(2000), got.LastReplyAt)
}
