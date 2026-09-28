package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
)

// threadUpdatedEv is thread_updated as the server sends it
// (notification.go): data.thread a JSON string, broadcast team_id the
// channel's team ("" for a DM/GM).
func threadUpdatedEv(team, root, channel string, replyCount, lastReplyAt, unread, prev int64) ws.Event {
	th := model.ThreadResponse{PostID: root, ReplyCount: replyCount, LastReplyAt: lastReplyAt, UnreadMentions: unread, UnreadReplies: 1,
		Post: &model.Post{ID: root, ChannelID: channel, ReplyCount: replyCount, LastReplyAt: lastReplyAt}}
	tb, _ := json.Marshal(th)
	db, _ := json.Marshal(map[string]any{"thread": string(tb), "previous_unread_mentions": prev, "previous_unread_replies": 0})
	return ws.Event{Type: "thread_updated", Data: db, Broadcast: ws.Broadcast{TeamID: team, UserID: "u1"}}
}

// threadReadEv is thread_read_changed (app/user.go UpdateThreadReadForUser):
// broadcast team_id is the team the PUT addressed — for a DM thread the
// team the client picked, not "". root "": every thread read.
func threadReadEv(team, root, channel string, unread, prev int64) ws.Event {
	d := map[string]any{"timestamp": 1}
	if root != "" {
		d["thread_id"], d["channel_id"] = root, channel
		d["unread_mentions"], d["previous_unread_mentions"] = unread, prev
	}
	db, _ := json.Marshal(d)
	return ws.Event{Type: "thread_read_changed", Data: db, Broadcast: ws.Broadcast{TeamID: team, UserID: "u1"}}
}

// crtCounts: a CRT server bootstrapped with thread mention totals, past
// the post-bootstrap guard.
func crtCounts(m map[string]int64) *Server {
	b := fixture()
	b.Config.CollapsedThreads = "always_on"
	b.ThreadMentions = m
	s := New(fixedNow)
	s.Bootstrap(b)
	s.SetUsers([]model.User{{ID: "u1", Username: "alice"}, {ID: "u2", Username: "bob"}, {ID: "u3", Username: "carol"}})
	s.ClearGuard()
	return s
}

func teamMentions(s *Server, team string) int {
	for _, t := range s.Sidebar(team).Teams {
		if t.ID == team {
			return t.Mentions
		}
	}
	return -1
}

// Ruling: thread mentions raise the server's and the team's badge, never
// a channel row; the DM/GM part counts once, in the server badge only.
func TestThreadMentionsRaiseServerAndTeamBadgesOnly(t *testing.T) {
	base := crtFixture(true)
	s := crtCounts(map[string]int64{"t1": 2, "": 1})
	assert.Equal(t, base.Badge().Mentions+3, s.Badge().Mentions, "server: channels + Σ teams + DM/GM")
	assert.Equal(t, teamMentions(base, "t1")+2, teamMentions(s, "t1"), "team: its channels + its threads")
	assert.Equal(t, ids(base.Sidebar("t1").Categories[0].Channels), ids(s.Sidebar("t1").Categories[0].Channels))
	for i, c := range s.Sidebar("t1").Categories {
		for j, it := range c.Channels {
			assert.Equal(t, base.Sidebar("t1").Categories[i].Channels[j].Mentions, it.Mentions, "channel row %s", it.ID)
		}
	}

	// A team we are no longer in counts nothing.
	s = crtCounts(map[string]int64{"gone-team": 4})
	assert.Equal(t, base.Badge().Mentions, s.Badge().Mentions)

	// Without CRT the totals are not used even if some were handed in.
	b := fixture()
	b.ThreadMentions = map[string]int64{"t1": 2}
	off := New(fixedNow)
	off.Bootstrap(b)
	assert.Equal(t, crtFixture(false).Badge().Mentions, off.Badge().Mentions)
}

// Unread threads without mentions raise nothing: only unread_mentions
// deltas are counted.
func TestThreadMentionDeltas(t *testing.T) {
	s := crtCounts(map[string]int64{"t1": 0, "": 0})
	base := s.Badge().Mentions
	team := teamMentions(s, "t1")

	eff := s.ApplyEvent(threadUpdatedEv("t1", "r1", "town", 1, 2000, 1, 0))
	assert.True(t, eff.Badge && eff.Sidebar)
	assert.False(t, eff.RereadThreadCounts)
	assert.Equal(t, base+1, s.Badge().Mentions)
	assert.Equal(t, team+1, teamMentions(s, "t1"))

	s.ApplyEvent(threadUpdatedEv("t1", "r1", "town", 2, 2100, 1, 1)) // a reply without a mention
	assert.Equal(t, base+1, s.Badge().Mentions, "unread replies without mentions raise nothing")

	s.ApplyEvent(threadReadEv("t1", "r1", "town", 0, 1))
	assert.Equal(t, base, s.Badge().Mentions)
	assert.Equal(t, team, teamMentions(s, "t1"))

	// A DM thread: thread_updated says team "", thread_read_changed says
	// the team the PUT went to — the channel decides, not the broadcast.
	s.ApplyEvent(threadUpdatedEv("", "r2", "dm2", 1, 2200, 2, 0))
	assert.Equal(t, base+2, s.Badge().Mentions)
	assert.Equal(t, team, teamMentions(s, "t1"), "a DM thread is not a team's")
	s.ApplyEvent(threadReadEv("t1", "r2", "dm2", 0, 2))
	assert.Equal(t, base, s.Badge().Mentions)
	assert.Equal(t, team, teamMentions(s, "t1"), "the DM read did not come off the team")

	// Never below 0; unknown channels and follow changes count nothing.
	s.ApplyEvent(threadReadEv("t1", "r1", "town", 0, 5))
	assert.Equal(t, team, teamMentions(s, "t1"))
	s.ApplyEvent(threadUpdatedEv("t1", "r3", "nowhere", 1, 2300, 1, 0))
	fb, _ := json.Marshal(map[string]any{"thread_id": "r1", "state": true, "reply_count": 3})
	s.ApplyEvent(ws.Event{Type: "thread_follow_changed", Data: fb, Broadcast: ws.Broadcast{TeamID: "t1", UserID: "u1"}})
	assert.Equal(t, base, s.Badge().Mentions)
}

// Review focus 5: events that arrive while the totals are read (the
// post-bootstrap guard) may or may not be in them — they are not counted,
// the totals are read again instead.
func TestThreadMentionsUnderGuardAreRereadNotCounted(t *testing.T) {
	b := fixture()
	b.Config.CollapsedThreads = "always_on"
	b.ThreadMentions = map[string]int64{"t1": 1}
	s := New(fixedNow)
	s.Bootstrap(b) // guard on
	base := s.Badge().Mentions
	eff := s.ApplyEvent(threadUpdatedEv("t1", "r1", "town", 1, 2000, 1, 0))
	assert.True(t, eff.RereadThreadCounts)
	assert.Equal(t, base, s.Badge().Mentions, "maybe already in the totals: not added")
	eff = s.ApplyEvent(threadReadEv("t1", "r1", "town", 0, 1))
	assert.True(t, eff.RereadThreadCounts)
	assert.Equal(t, base, s.Badge().Mentions)

	s.ClearGuard()
	eff = s.ApplyEvent(threadUpdatedEv("t1", "r1", "town", 2, 2100, 1, 0))
	assert.False(t, eff.RereadThreadCounts)
	assert.Equal(t, base+1, s.Badge().Mentions)
}

func TestThreadCountsRereadInstallsOnlyWhenSettled(t *testing.T) {
	s := crtCounts(map[string]int64{"t1": 1})
	base := s.Badge().Mentions - 1

	crt, team, tok := s.ThreadCountsFetch()
	require.True(t, crt)
	assert.Equal(t, "t1", team)
	s.ApplyEvent(threadUpdatedEv("t1", "r1", "town", 1, 2000, 1, 0)) // in flight
	installed, retry := s.SetThreadMentions(map[string]int64{"t1": 5}, tok, false)
	assert.False(t, installed)
	assert.True(t, retry, "an event moved the counts meanwhile: read again")
	assert.Equal(t, base+2, s.Badge().Mentions)

	_, _, tok = s.ThreadCountsFetch()
	installed, _ = s.SetThreadMentions(map[string]int64{"t1": 5, "": 1}, tok, false)
	assert.True(t, installed)
	assert.Equal(t, base+6, s.Badge().Mentions, "a reread replaces the totals whole")

	// Last try: installed even though an event moved them…
	_, _, tok = s.ThreadCountsFetch()
	s.ApplyEvent(threadUpdatedEv("t1", "r1", "town", 2, 2100, 2, 1))
	installed, _ = s.SetThreadMentions(map[string]int64{"t1": 2}, tok, true)
	assert.True(t, installed)
	assert.Equal(t, base+2, s.Badge().Mentions)

	// …but never over a fresher bootstrap.
	_, _, tok = s.ThreadCountsFetch()
	b := fixture()
	b.Config.CollapsedThreads = "always_on"
	b.ThreadMentions = map[string]int64{"t1": 3}
	s.Bootstrap(b)
	installed, retry = s.SetThreadMentions(map[string]int64{"t1": 9}, tok, true)
	assert.False(t, installed)
	assert.False(t, retry)
	assert.Equal(t, base+3, s.Badge().Mentions)
}

// thread_read_changed without thread_id (every thread of a team read, on
// another device) cannot be applied as a delta: the totals are read again.
func TestAllThreadsReadAsksForReread(t *testing.T) {
	s := crtCounts(map[string]int64{"t1": 2, "": 1})
	before := s.Badge().Mentions
	eff := s.ApplyEvent(threadReadEv("t1", "", "", 0, 0))
	assert.True(t, eff.RereadThreadCounts)
	assert.Equal(t, before, s.Badge().Mentions, "nothing changes until the reread lands")

	off := crtFixture(false)
	assert.False(t, off.ApplyEvent(threadReadEv("t1", "", "", 0, 0)).RereadThreadCounts, "no CRT: nothing to read")
}

// Review focus 5: turning CRT off leaves no thread mention behind; turning
// it back on reads the totals again.
func TestThreadMentionsVanishWhenCRTTurnsOffByPreference(t *testing.T) {
	b := fixture()
	b.Config.CollapsedThreads = "default_on"
	b.ThreadMentions = map[string]int64{"t1": 2, "": 1}
	s := New(fixedNow)
	s.Bootstrap(b)
	s.ClearGuard()
	withThreads := s.Badge().Mentions

	pref := func(v string) ws.Event {
		pb, _ := json.Marshal([]model.Preference{{UserID: "u1", Category: "display_settings", Name: "collapsed_reply_threads", Value: v}})
		db, _ := json.Marshal(map[string]any{"preferences": string(pb)})
		return ws.Event{Type: "preferences_changed", Data: db}
	}
	eff := s.ApplyEvent(pref("off"))
	assert.True(t, eff.Resync)
	assert.Equal(t, crtFixture(false).Badge().Mentions, s.Badge().Mentions)
	assert.Less(t, s.Badge().Mentions, withThreads)
	s.ApplyEvent(threadUpdatedEv("t1", "r1", "town", 1, 2000, 1, 0))
	assert.Equal(t, crtFixture(false).Badge().Mentions, s.Badge().Mentions, "no CRT: thread events count nothing")

	eff = s.ApplyEvent(pref("on"))
	assert.True(t, eff.RereadThreadCounts)
	assert.Equal(t, crtFixture(true).Badge().Mentions, s.Badge().Mentions, "back on: nothing old, the totals are read again")
}

func TestThreadMentionsFollowTheBootstrap(t *testing.T) {
	s := crtCounts(map[string]int64{"t1": 2})
	base := crtFixture(true).Badge().Mentions

	// A failed totals read keeps what was held.
	b := fixture()
	b.Config.CollapsedThreads = "always_on"
	b.ThreadMentionsFailed = true
	s.Bootstrap(b)
	assert.Equal(t, base+2, s.Badge().Mentions)

	// CRT off at the server: nil totals, nothing held.
	b = fixture()
	s.Bootstrap(b)
	assert.Equal(t, crtFixture(false).Badge().Mentions, s.Badge().Mentions)
	b.Config.CollapsedThreads = "always_on"
	b.ThreadMentionsFailed = true
	s.Bootstrap(b)
	assert.Equal(t, base, s.Badge().Mentions, "the totals of an earlier CRT period are not brought back")
}

// thread_updated carries the thread's reply count and last reply time; a
// root held in the feed takes them only when they are newer (Task 2 rule).
func TestThreadUpdatedMovesTheRootCount(t *testing.T) {
	s := crtCounts(nil)
	root := mkPost("root", "town", "u2", 1000)
	root.ReplyCount, root.LastReplyAt = 1, 1500
	s.SetWindow("town", []model.Post{root}, true, 5, 0)

	eff := s.ApplyEvent(threadUpdatedEv("t1", "root", "town", 3, 3000, 0, 0))
	n, at := rootCount(t, s, "town", "root")
	assert.Equal(t, int64(3), n)
	assert.Equal(t, int64(3000), at)
	assert.Contains(t, eff.Channels, "town")

	s.ApplyEvent(threadUpdatedEv("t1", "root", "town", 2, 2000, 0, 0)) // late
	n, _ = rootCount(t, s, "town", "root")
	assert.Equal(t, int64(3), n, "an older thread_updated does not lower it")
}
