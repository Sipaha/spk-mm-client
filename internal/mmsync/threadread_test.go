package mmsync

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
)

// reads: successful PUT …/threads/{root}/read/{ts} calls for root.
func (h *harness) reads(root string) int {
	n := 0
	for _, r := range h.fake.ThreadReads() {
		if r.RootID == root {
			n++
		}
	}
	return n
}

// exactlyReads waits for n reads of root and checks no more follow.
func (h *harness) exactlyReads(root string, n int, msg string) {
	h.t.Helper()
	h.eventually(func() bool { return h.reads(root) >= n }, msg)
	require.Never(h.t, func() bool { return h.reads(root) > n }, 400*time.Millisecond, 20*time.Millisecond, msg+": one read, not more")
}

func (h *harness) noReads(root string, msg string) {
	h.t.Helper()
	require.Never(h.t, func() bool { return h.fake.ThreadReadTries() > 0 || h.reads(root) > 0 }, 400*time.Millisecond, 20*time.Millisecond, msg)
}

// Review focus 4: a thread is marked read on the server only while its
// panel is open and the window focused (the api layer focuses only the
// server on screen — a background server's worker is unfocused) — and only
// under CRT. Each occasion (opened while focused, focus came later, a new
// reply by someone else) sends exactly one read.
func TestThreadReadOnlyWhileOpenAndFocused(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 2)
	h.w.OpenChannel("c-town")

	// Unfocused (window in the background, or a background server).
	h.openThread(root, 3)
	h.noReads(root, "read while the window is not focused")
	h.fake.ReplyAs("c-town", root, "bob", "while away")
	h.eventually(func() bool { return h.threadHas(root, "while away") == 1 }, "reply")
	h.noReads(root, "a reply read while the window is not focused")

	// Focus later: one read.
	h.w.SetFocused(true)
	h.exactlyReads(root, 1, "focus came with the thread open")
	h.w.SetFocused(true) // focus again with nothing new: nothing to send
	h.exactlyReads(root, 1, "focused again")

	// A new reply by someone else while open and focused: one read (its
	// posted and thread_updated both ask; the second is covered).
	h.fake.ReplyAs("c-town", root, "carol", "while looking")
	h.exactlyReads(root, 2, "a new reply in the open thread")
	// Our own reply: the server reads it for us.
	require.NoError(t, h.w.SendReply("c-town", root, "mine"))
	h.eventually(func() bool { return h.threadHas(root, "mine") == 1 }, "own reply")
	h.exactlyReads(root, 2, "our own reply")

	// Unfocused again: nothing.
	h.w.SetFocused(false)
	h.fake.ReplyAs("c-town", root, "bob", "unfocused")
	h.eventually(func() bool { return h.threadHas(root, "unfocused") == 1 }, "reply")
	h.exactlyReads(root, 2, "unfocused")

	// Panel closed, focused: nothing.
	h.w.CloseThread()
	h.w.SetFocused(true)
	h.fake.ReplyAs("c-town", root, "bob", "closed")
	h.eventually(func() bool { return h.rootReplies("c-town", root) == 7 }, "reply")
	h.exactlyReads(root, 2, "panel closed")

	// Opened again while focused: one read.
	h.openThread(root, 8)
	h.exactlyReads(root, 3, "opened while focused")
}

func TestThreadIsNotReadWithoutCRT(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	root := h.fake.SeedThread("c-town", "alice", 2)
	h.w.OpenChannel("c-town")
	h.w.SetFocused(true)
	h.openThread(root, 3)
	h.fake.ReplyAs("c-town", root, "bob", "no crt")
	h.eventually(func() bool { return h.threadHas(root, "no crt") == 1 }, "reply")
	require.Never(t, func() bool { return h.fake.ThreadReadTries() > 0 }, 400*time.Millisecond, 20*time.Millisecond,
		"without CRT the channel view reads threads")
}

// Review focus 4: a thread we do not follow answers 404 — once per
// opening, not once per reply; our own reply subscribes us and reads resume.
func TestThreadReadNotFollowingIsNotRepeated(t *testing.T) {
	h := crtHarness(t)
	rootPost := h.fake.PostAs("c-town", "bob", "bob's root")
	h.fake.ReplyAs("c-town", rootPost.ID, "carol", "carol's reply")
	root := rootPost.ID
	require.False(t, h.fake.Following(root, "alice"))
	h.w.OpenChannel("c-town")
	h.w.SetFocused(true)
	h.eventually(func() bool { return h.hasMessage("c-town", "bob's root") }, "root")
	h.openThread(root, 2)
	h.eventually(func() bool { return h.fake.ThreadReadTries() == 1 }, "the first read (404)")
	for i := 0; i < 3; i++ {
		h.fake.ReplyAs("c-town", root, "bob", fmt.Sprintf("more %d", i))
	}
	h.eventually(func() bool { return h.threadHas(root, "more 2") == 1 }, "replies")
	require.Never(t, func() bool { return h.fake.ThreadReadTries() > 1 }, 400*time.Millisecond, 20*time.Millisecond,
		"not following: no read per reply")

	require.NoError(t, h.w.SendReply("c-town", root, "now I follow"))
	h.eventually(func() bool { return h.fake.Following(root, "alice") }, "subscribed by replying")
	h.fake.ReplyAs("c-town", root, "carol", "after mine")
	h.eventually(func() bool { return h.reads(root) == 1 }, "reads resume once we follow")
}

// badge: the server badge's mentions (channels + thread mentions).
func (h *harness) badge() int { return h.w.State().Badge().Mentions }

// Review focus 5: a thread mention arriving while the metadata (and the
// thread totals with it) is being read counts once — whether the totals
// read already saw it ("in the read": it lands while that request is
// served) or not ("after the read": it lands while the categories are).
// The fake counts a request on arrival, before its latency.
func TestThreadMentionsSurviveRefreshWithoutDoubleCount(t *testing.T) {
	for _, c := range []struct{ name, slow, then string }{
		{"in the read", "/teams/unread", "/api/v4/users/me/teams/unread"},
		{"after the read", "/channels/categories", "/api/v4/users/me/teams/t-fake/channels/categories"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := crtHarness(t)
			root := h.fake.SeedThread("c-town", "alice", 1)
			h.eventually(func() bool { return h.rootReplies("c-town", root) == 1 }, "seeded")
			base := h.badge()
			hits := h.fake.Hits("GET", c.then)
			h.fake.SetLatency(c.slow, time.Second)
			h.fake.AddChannel("c-extra", "Extra", "alice", "bob") // user_added → a refresh
			h.eventually(func() bool { return h.fake.Hits("GET", c.then) > hits }, "the refresh reached "+c.then)
			h.fake.ReplyAs("c-town", root, "bob", "@alice look")
			h.eventually(func() bool { return h.view("c-extra").Loaded }, "the refresh landed")
			h.eventually(func() bool { return h.badge() == base+1 }, fmt.Sprintf("the badge: want %d", base+1))
			require.Never(t, func() bool { return h.badge() != base+1 }, 800*time.Millisecond, 20*time.Millisecond,
				"counted once")
		})
	}
}

// Review focus 5: "all threads read" (another device) has no thread id to
// take a delta from: the totals are read again.
func TestAllThreadsReadRereadsTotals(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 1)
	h.eventually(func() bool { return h.rootReplies("c-town", root) == 1 }, "seeded")
	base := h.badge()
	h.fake.ReplyAs("c-town", root, "bob", "@alice one")
	h.fake.ReplyAs("c-town", root, "carol", "@alice two")
	h.eventually(func() bool { return h.badge() == base+2 }, "two thread mentions")
	h.fake.MarkAllThreadsReadAs("alice", "t-fake")
	h.eventually(func() bool { return h.badge() == base }, "all read elsewhere: nothing left")
}

// Review focus 5: CRT turned off at the server leaves no thread mention.
func TestThreadMentionsVanishWhenCRTTurnsOff(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 1)
	h.eventually(func() bool { return h.rootReplies("c-town", root) == 1 }, "seeded")
	h.fake.ReplyAs("c-town", root, "bob", "@alice before")
	h.eventually(func() bool { return h.teamMentions("t-fake") == h.channelRows("t-fake")+1 }, "a thread mention on the team")
	h.fake.SetCollapsedThreads("disabled")
	h.fake.AddChannel("c-extra", "Extra", "alice", "bob") // a refresh reads the new config
	h.eventually(func() bool { return !h.w.State().CRT() && h.view("c-extra").Loaded }, "CRT off")
	// Without CRT the reply's mention is its channel's again (the REST
	// counters say so) — and nothing of the thread's is left on top.
	h.eventually(func() bool { return h.mentions("c-town") == 1 }, "the channel's mention")
	require.Never(t, func() bool { return h.teamMentions("t-fake") != h.channelRows("t-fake") }, 600*time.Millisecond, 20*time.Millisecond,
		"no thread mention left on top of the channels'")
}

// Review focus 5: a DM thread's mention counts once — in the server badge,
// on no team — and reading it (through the current team) takes it off.
func TestDMThreadMentionsCountOnce(t *testing.T) {
	h := crtHarness(t)
	rootPost := h.fake.PostAs("c-dm-bob", "bob", "dm root")
	h.eventually(func() bool { return h.hasMessage("c-dm-bob", "dm root") }, "dm root")
	h.w.OpenChannel("c-dm-bob")
	h.w.SetFocused(true) // the DM itself is read: only the thread's mention is left to watch
	h.eventually(func() bool {
		return h.fake.Member("c-dm-bob", "alice").MentionCountRoot == 0 && h.mentions("c-dm-bob") == 0
	}, "the DM read")
	base, team := h.badge(), h.teamMentions("t-fake")
	h.fake.ReplyAs("c-dm-bob", rootPost.ID, "bob", "in the dm thread") // a DM mentions alice
	h.eventually(func() bool { return h.badge() == base+1 }, "the DM thread mention")
	assert.Equal(t, team, h.teamMentions("t-fake"), "on no team")

	// A refresh reads it back from the totals: still once.
	h.fake.AddChannel("c-extra", "Extra", "alice", "bob")
	h.eventually(func() bool { return h.view("c-extra").Loaded }, "the refresh landed")
	require.Never(t, func() bool { return h.badge() != base+1 }, 600*time.Millisecond, 20*time.Millisecond, "once after a refresh")

	// Reading it: the read goes through the current team, the event says
	// that team — the mention still comes off the DM part.
	_, ok := h.w.OpenThread("c-dm-bob", rootPost.ID)
	require.True(t, ok)
	// (Two reads if the page brings replies past our clock: the fake's
	// clock runs ahead, like a skewed server's.)
	h.eventually(func() bool { return h.reads(rootPost.ID) >= 1 }, "read")
	h.eventually(func() bool { return h.badge() == base }, "read: gone")
	assert.Equal(t, team, h.teamMentions("t-fake"))
}

// rootReplies: the reply count of root as the channel feed shows it.
func (h *harness) rootReplies(ch, root string) int64 {
	for _, p := range h.view(ch).Posts {
		if p.ID == root {
			return p.ReplyCount
		}
	}
	return -1
}

// channelRows: the mentions of team's channel rows (not muted, not DM/GM) —
// what the team's row shows without thread mentions.
func (h *harness) channelRows(team string) int {
	n := 0
	for _, c := range h.w.State().Sidebar(team).Categories {
		for _, it := range c.Channels {
			if !it.Muted && it.Type != "D" && it.Type != "G" {
				n += it.Mentions
			}
		}
	}
	return n
}

func (h *harness) teamMentions(team string) int {
	for _, t := range h.w.State().Sidebar(team).Teams {
		if t.ID == team {
			return t.Mentions
		}
	}
	return -1
}

// A failed read of the thread totals does not fail the metadata refresh
// (like the categories): the totals held so far stay.
func TestThreadCountsFailureKeepsThePreviousOnes(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 1)
	h.eventually(func() bool { return h.rootReplies("c-town", root) == 1 }, "seeded")
	base := h.badge()
	h.fake.ReplyAs("c-town", root, "bob", "@alice kept")
	h.eventually(func() bool { return h.badge() == base+1 }, "a thread mention")
	h.fake.SetFailure("/teams/unread", 500)
	h.fake.AddChannel("c-extra", "Extra", "alice", "bob")
	h.eventually(func() bool { return h.view("c-extra").Loaded }, "the refresh still landed")
	require.Never(t, func() bool { return h.badge() != base+1 }, 400*time.Millisecond, 20*time.Millisecond, "kept")
}

// A reread that fails ("all read" on another device while the server
// errs) leaves the totals stale; the worker reads them again once live
// again — a resumed stream included (no metadata refresh then).
func TestFailedRereadIsRetriedWhenLiveAgain(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 1)
	h.eventually(func() bool { return h.rootReplies("c-town", root) == 1 }, "seeded")
	base := h.badge()
	h.fake.ReplyAs("c-town", root, "bob", "@alice stale")
	h.eventually(func() bool { return h.badge() == base+1 }, "a thread mention")
	h.fake.SetFailure("/teams/unread", 500)
	h.fake.MarkAllThreadsReadAs("alice", "t-fake")
	h.eventually(func() bool { return h.w.State().ThreadCountsDirty() }, "the reread failed")
	assert.Equal(t, base+1, h.badge())
	h.fake.SetFailure("/teams/unread", 0)
	h.fake.DropConnections(false) // a resume: no bootstrap
	h.eventually(func() bool { return h.badge() == base }, "read again once live")
}
