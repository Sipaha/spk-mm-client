package mmsync

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

// focusHarness: CRT, c-town open, a thread of n replies ("Reply 1"…) in it,
// not opened yet.
func focusHarness(t *testing.T, o mmfake.Options, n int) (*harness, *gate, string) {
	t.Helper()
	o.CRT = true
	h, g := jumpHarness(t, o)
	return h, g, h.fake.SeedThread("c-town", "alice", n)
}

func (h *harness) replyID(n int) string { return h.postID(fmt.Sprintf("Reply %d", n)) }

func replyMsgs(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("Reply %d", i))
	}
	return out
}

func threadMsgs(v state.ThreadView) []string {
	out := []string{}
	for _, p := range v.Posts {
		out = append(out, p.Message)
	}
	return out
}

func withRoot(parts ...[]string) []string {
	out := []string{"Thread root"}
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// threadRequests: the logged thread page requests of root.
func (g *gate) threadRequests(root string) []string {
	var out []string
	for _, r := range g.requests() {
		if strings.Contains(r, "/posts/"+root+"/thread") {
			out = append(out, r)
		}
	}
	return out
}

func (h *harness) focusAt(root string, n int) state.ThreadView {
	h.t.Helper()
	v, err := h.w.OpenThreadAt(context.Background(), "c-town", root, h.replyID(n))
	require.NoError(h.t, err)
	return v
}

func (h *harness) tailLoaded(root string) {
	h.t.Helper()
	h.eventually(func() bool { v := h.thread(root); return v.Loaded && !v.Syncing }, "the tail never loaded")
}

func TestFocusHeldReplyNeedsNoFocus(t *testing.T) {
	h, g, root := focusHarness(t, mmfake.Options{}, 150)
	h.openThread(root, 61)
	require.NoError(t, h.w.LoadOlderReplies(context.Background(), root))
	g.clearLog()
	v := h.focusAt(root, 40)
	assert.Nil(t, v.Focus)
	assert.Empty(t, g.requests(), "held: no request")
	assert.Equal(t, withRoot(replyMsgs(31, 150)), threadMsgs(v))
	v, err := h.w.OpenThreadAt(context.Background(), "c-town", root, root)
	require.NoError(t, err)
	assert.Nil(t, v.Focus, "the root is always shown")
	assert.Empty(t, g.requests())
}

func TestFocusBeyond200LoadsAroundTarget(t *testing.T) {
	h, g, root := focusHarness(t, mmfake.Options{}, 500)
	target := h.replyID(100)
	v, err := h.w.OpenThreadAt(context.Background(), "c-town", root, target)
	require.NoError(t, err)
	require.NotNil(t, v.Focus)
	assert.Equal(t, target, v.Focus.TargetID)
	assert.Contains(t, g.requests(), "/api/v4/posts/"+target+"?")
	reqs := g.threadRequests(root)
	assert.Contains(t, strings.Join(reqs, " "), "direction=down&fromCreateAt=")
	for _, r := range reqs {
		if strings.Contains(r, "fromPost="+target) {
			assert.Contains(t, r, "perPage=30")
		}
	}
	h.tailLoaded(root) // the thread was not cached: its latest page loads meanwhile
	v = h.thread(root)
	assert.Equal(t, withRoot(replyMsgs(70, 130), replyMsgs(441, 500)), threadMsgs(v))
	assert.True(t, v.Focus.HasOlder)
	assert.True(t, v.Focus.HasNewer)
	assert.True(t, v.Focus.Gap.Open)
	assert.Equal(t, h.replyID(441), v.Focus.Gap.BeforeID)
	assert.False(t, v.HasMore)
}

func TestFocusSlidingWindowBothWays(t *testing.T) {
	h, _, root := focusHarness(t, mmfake.Options{}, 500)
	h.focusAt(root, 100)
	h.tailLoaded(root)
	ctx := context.Background()
	load := func(newer bool) { t.Helper(); require.NoError(t, h.w.LoadThreadFocus(ctx, root, newer)) }
	for range 3 {
		load(true)
	}
	assert.Equal(t, withRoot(replyMsgs(111, 310), replyMsgs(441, 500)), threadMsgs(h.thread(root)), "≤ 200: the up edge let go of")
	load(false)
	assert.Equal(t, withRoot(replyMsgs(51, 250), replyMsgs(441, 500)), threadMsgs(h.thread(root)), "back up: the down edge let go of")
	load(true)
	assert.Equal(t, withRoot(replyMsgs(111, 310), replyMsgs(441, 500)), threadMsgs(h.thread(root)))
	for i := 0; i < 5 && h.thread(root).Focus.HasNewer; i++ {
		load(true)
	}
	v := h.thread(root)
	assert.False(t, v.Focus.Gap.Open, "the gap closed on the tail")
	msgs := threadMsgs(v)
	assert.Equal(t, withRoot(replyMsgs(501-(len(msgs)-1), 500)), msgs, "one run up to the latest, nothing twice")
	assert.LessOrEqual(t, len(msgs)-1, state.ThreadMaxReplies+state.ThreadPage)
	for i := 0; i < 10 && h.thread(root).Focus.HasOlder; i++ {
		load(false)
	}
	v = h.thread(root)
	assert.False(t, v.Focus.HasOlder)
	assert.Equal(t, withRoot(replyMsgs(1, 200), replyMsgs(441, 500)), threadMsgs(v))
	assert.True(t, v.Focus.Gap.Open, "the down edge let go of: the gap is back")
}

func TestFocusInvalidatedByCloseAndReopen(t *testing.T) {
	h, g, root := focusHarness(t, mmfake.Options{}, 500)
	h.focusAt(root, 100)
	h.tailLoaded(root)
	g.hold(query("direction", "down"))
	done := make(chan error, 1)
	go func() { done <- h.w.LoadThreadFocus(context.Background(), root, true) }()
	g.wait(t)
	h.w.CloseThread()
	_, ok := h.w.OpenThread("c-town", root)
	require.True(t, ok)
	g.open()
	require.NoError(t, <-done, "dropped, not an error")
	v := h.thread(root)
	assert.Nil(t, v.Focus)
	assert.Equal(t, withRoot(replyMsgs(441, 500)), threadMsgs(v))

	// A focus superseded by the next one while loading.
	g.hold(func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/posts/"+h.replyID(20)) })
	first := make(chan error, 1)
	go func() {
		_, err := h.w.OpenThreadAt(context.Background(), "c-town", root, h.replyID(20))
		first <- err
	}()
	g.wait(t)
	v = h.focusAt(root, 300)
	require.ErrorIs(t, <-first, ErrSuperseded)
	g.open()
	assert.Equal(t, h.replyID(300), v.Focus.TargetID)
	assert.Equal(t, withRoot(replyMsgs(270, 330), replyMsgs(441, 500)), threadMsgs(h.thread(root)))
}

func TestFocusRevalidatedAfterReconnect(t *testing.T) {
	h, _, root := focusHarness(t, mmfake.Options{SinceLimit: 5}, 500)
	h.focusAt(root, 100)
	h.tailLoaded(root)
	edited, deleted := h.replyID(110), h.replyID(120)
	offlineOverflow(h, func() {
		h.fake.EditAs(edited, "edited offline")
		h.fake.DeleteAs(deleted)
		for i := range 70 { // the tail overflows too
			h.fake.ReplyAs("c-town", root, "bob", fmt.Sprintf("offline reply %d", i))
		}
	})
	h.eventually(func() bool {
		v := h.thread(root)
		return v.Focus != nil && !v.Focus.Gap.Stale && !v.Syncing && h.threadHas(root, "offline reply 69") == 1
	}, "the focus reread and the tail read again")
	v := h.thread(root)
	want := withRoot(replyMsgs(70, 109), []string{"edited offline"}, replyMsgs(111, 119), replyMsgs(121, 130))
	assert.Equal(t, want, threadMsgs(v)[:len(want)])
	assert.True(t, v.Focus.Gap.Open)
	assert.Equal(t, "offline reply 10", threadMsgs(v)[len(want)], "the tail: the latest 60")
	assert.Len(t, v.Posts, len(want)+state.ThreadPage)
}

func TestFocusChecksChannelAndRootID(t *testing.T) {
	h, _, root := focusHarness(t, mmfake.Options{}, 300)
	other := h.fake.PostAs("c-town", "bob", "another root")
	foreign := h.fake.ReplyAs("c-town", other.ID, "bob", "a reply of another thread")
	ctx := context.Background()
	_, err := h.w.OpenThreadAt(ctx, "c-town", root, foreign.ID)
	assert.ErrorIs(t, err, ErrWrongThread)
	_, err = h.w.OpenThreadAt(ctx, "c-town", root, h.fake.FindPost("c-offtopic", "Welcome to off-topic"))
	assert.ErrorIs(t, err, ErrWrongChannel)
	gone := h.replyID(5)
	h.fake.DeleteAs(gone)
	_, err = h.w.OpenThreadAt(ctx, "c-town", root, gone)
	assert.ErrorIs(t, err, ErrPostGone)
	_, err = h.w.OpenThreadAt(ctx, "c-town", root, "nosuchpostnosuchpostnosuch")
	assert.ErrorIs(t, err, ErrPostGone)
	private := h.replyID(6)
	h.fake.SetFailure("/posts/"+private, http.StatusForbidden)
	_, err = h.w.OpenThreadAt(ctx, "c-town", root, private)
	assert.ErrorIs(t, err, ErrForbidden)
	_, err = h.w.OpenThreadAt(ctx, "nosuchchannelnosuchchannel", root, private)
	assert.ErrorIs(t, err, ErrNoChannel)
	h.tailLoaded(root)
	assert.Nil(t, h.thread(root).Focus)
	assert.Len(t, h.thread(root).Posts, 1+state.ThreadPage, "the plain thread")
}

// staleFocusHarness: a focus on Reply 100 of a 500-reply thread, then an
// offline edit inside it and a reconnect with a since overflow — the focus
// is stale; its reread pages (perPage=200) go through the gate. revalIdle
// is short.
func staleFocusHarness(t *testing.T, beforeReconnect func(g *gate)) (*harness, *gate, string) {
	t.Helper()
	g := newGate()
	h := newHarness(t, mmfake.Options{CRT: true, SinceLimit: 5, RequestHook: g.hook})
	t.Cleanup(g.open)
	h.tune = func(c *Config) { c.revalIdle = 100 * time.Millisecond }
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.OpenChannel("c-town")
	root := h.fake.SeedThread("c-town", "alice", 500)
	h.focusAt(root, 100)
	h.tailLoaded(root)
	beforeReconnect(g)
	offlineOverflow(h, func() { h.fake.EditAs(h.replyID(110), "edited offline") })
	return h, g, root
}

func (h *harness) focusReread(root string) bool {
	v := h.thread(root)
	return v.Focus != nil && !v.Focus.Gap.Stale && h.threadHas(root, "edited offline") == 1
}

// Fix round 1, item 2: a held jump cancels the running reread (a new
// navigation) — it is begun again, not abandoned.
func TestFocusRereadCancelledByHeldJumpIsRescheduled(t *testing.T) {
	h, g, root := staleFocusHarness(t, func(g *gate) { g.hold(query("perPage", "200")) })
	g.wait(t)
	v, err := h.w.OpenThreadAt(context.Background(), "c-town", root, h.replyID(105))
	require.NoError(t, err)
	require.NotNil(t, v.Focus, "held: the focus stays")
	assert.True(t, v.Focus.Gap.Stale)
	g.open()
	h.eventually(func() bool { return h.focusReread(root) }, "the reread was begun again")
}

// Fix round 1, item 2: a failed reread is retried (bounded); past that the
// user's RetryThreadRevalidation does it.
func TestFocusRereadRetriedAfterFailure(t *testing.T) {
	var mu sync.Mutex
	fails := 1
	h, g, root := staleFocusHarness(t, func(g *gate) {
		g.failing(func(r *http.Request) bool {
			mu.Lock()
			defer mu.Unlock()
			if r.URL.Query().Get("perPage") != "200" || fails == 0 {
				return false
			}
			fails--
			return true
		})
	})
	g.wait(t)
	h.eventually(func() bool { return h.focusReread(root) }, "retried after the failure")

	// Every read fails: the retries give up, the focus stays stale.
	mu.Lock()
	fails = 1000
	mu.Unlock()
	h.fake.SetDown(true)
	h.fake.DropConnections(true)
	h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "offline")
	h.fake.SetDown(false)
	for range 1 + revalRetries {
		g.wait(t)
	}
	select {
	case <-g.arrived:
		t.Fatal("retried past the bound")
	case <-time.After(500 * time.Millisecond):
	}
	assert.True(t, h.thread(root).Focus.Gap.Stale)
	g.failing(nil)
	require.NoError(t, h.w.RetryThreadRevalidation(context.Background(), root))
	assert.False(t, h.thread(root).Focus.Gap.Stale)
}

// Codex review, finding 5: opening a focus registers (cancelling the ones
// before it) and begins in the state in one step — an OpenThreadAt
// cancelled between the two must not begin after the newer one and drop
// its focus.
func TestOpenThreadAtRegistrationAndBeginAreOneStep(t *testing.T) {
	h, g, hook := raceHarness(t, mmfake.Options{CRT: true})
	root := h.fake.SeedThread("c-town", "alice", 500)
	a, b := h.replyID(100), h.replyID(300)
	errA, errB := navRace(t, g, hook, threadLane, a, b, func(id string) error {
		_, err := h.w.OpenThreadAt(context.Background(), "c-town", root, id)
		return err
	})
	require.ErrorIs(t, errA, ErrSuperseded)
	require.NoError(t, errB, "the newer focus applies")
	v := h.thread(root)
	require.NotNil(t, v.Focus)
	assert.Equal(t, b, v.Focus.TargetID)
	assert.Contains(t, threadMsgs(v), "Reply 300")
}
