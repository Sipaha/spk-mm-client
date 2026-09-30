package mmsync

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

// gate is the fake's request hook (Ruling R3): it logs the post requests
// and holds those matching until released — a page in flight, on demand.
type gate struct {
	mu      sync.Mutex
	match   func(r *http.Request) bool
	release chan struct{}
	ignore  bool                       // a held request waits for release even when its client gave up
	fail    func(r *http.Request) bool // answered with a 500 instead
	arrived chan string
	log     []string
}

func newGate() *gate { return &gate{arrived: make(chan string, 64)} }

func (g *gate) hook(w http.ResponseWriter, r *http.Request) bool {
	g.mu.Lock()
	if strings.Contains(r.URL.Path, "/posts") {
		g.log = append(g.log, r.URL.Path+"?"+r.URL.RawQuery)
	}
	m, rel, ignore, fail := g.match, g.release, g.ignore, g.fail
	g.mu.Unlock()
	if fail != nil && fail(r) {
		g.arrived <- r.URL.RawQuery
		http.Error(w, `{"id":"mmfake.injected_failure","status_code":500}`, http.StatusInternalServerError)
		return true
	}
	if m == nil || !m(r) {
		return false
	}
	g.arrived <- r.URL.RawQuery
	if ignore {
		<-rel
		return false
	}
	select {
	case <-rel:
	case <-r.Context().Done():
		return true
	}
	return false
}

// hold holds the requests m matches from now on.
func (g *gate) hold(m func(r *http.Request) bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.match, g.release = m, make(chan struct{})
}

// open lets every held request go, and the next ones through.
func (g *gate) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.release != nil {
		close(g.release)
	}
	g.match, g.release = nil, nil
}

func (g *gate) wait(t *testing.T) string {
	t.Helper()
	select {
	case q := <-g.arrived:
		return q
	case <-time.After(10 * time.Second):
		t.Fatal("the request never came")
		return ""
	}
}

func (g *gate) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.log)
}

func (g *gate) clearLog() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.log = nil
}

// failing answers the requests m matches with a 500 (nil: none).
func (g *gate) failing(m func(r *http.Request) bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fail = m
}

// pageRequests: the logged channel page requests (before=/after=) that
// mention id.
func (g *gate) pageRequests(id string) []string {
	var out []string
	for _, r := range g.requests() {
		if strings.Contains(r, "/channels/") && strings.Contains(r, id) {
			out = append(out, r)
		}
	}
	return out
}

func query(key, val string) func(r *http.Request) bool {
	return func(r *http.Request) bool { return r.URL.Query().Get(key) == val }
}

// jumpHarness: the fake with the gate, the worker live, every channel
// loaded and c-town (150 posts) open.
func jumpHarness(t *testing.T, o mmfake.Options) (*harness, *gate) {
	t.Helper()
	g := newGate()
	o.RequestHook = g.hook
	h := newHarness(t, o)
	t.Cleanup(g.open) // before the fake closes: it waits for held requests
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.OpenChannel("c-town")
	return h, g
}

func (h *harness) postID(msg string) string {
	h.t.Helper()
	id := h.fake.FindPost("c-town", msg)
	require.NotEmpty(h.t, id, msg)
	return id
}

func town(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("Message #%d", i))
	}
	return out
}

func messages(v state.ChannelView) []string {
	out := []string{}
	for _, p := range v.Posts {
		out = append(out, p.Message)
	}
	return out
}

func TestJumpToOldPostBuildsSegment(t *testing.T) {
	h, _ := jumpHarness(t, mmfake.Options{})
	target := h.postID("Message #10")
	res, err := h.w.JumpTo(context.Background(), "c-town", target)
	require.NoError(t, err)
	assert.Equal(t, JumpResult{PostID: target, InFeed: true}, res)
	v := h.view("c-town")
	assert.Equal(t, append(town(1, 40), town(91, 150)...), messages(v))
	assert.True(t, v.Gap.Open)
	assert.Equal(t, h.postID("Message #91"), v.Gap.BeforeID)
	assert.False(t, v.HasMore, "the segment reaches the first post")
}

func TestJumpToHeldPostMakesNoRequestsButInvalidates(t *testing.T) {
	h, g := jumpHarness(t, mmfake.Options{})
	old := h.postID("Message #10")
	g.hold(query("after", old))
	done := make(chan error, 1)
	go func() { _, err := h.w.JumpTo(context.Background(), "c-town", old); done <- err }()
	g.wait(t)
	held := h.postID("Message #120")
	res, err := h.w.JumpTo(context.Background(), "c-town", held)
	require.NoError(t, err)
	assert.Equal(t, JumpResult{PostID: held, InFeed: true}, res)
	for _, r := range g.requests() {
		assert.NotContains(t, r, held, "a held post is jumped to without a request")
	}
	g.open()
	require.ErrorIs(t, <-done, ErrSuperseded)
	v := h.view("c-town")
	assert.Equal(t, town(91, 150), messages(v), "the old jump's pages were dropped")
	assert.False(t, v.Gap.Open)
}

func TestStalePagesAreDroppedAcrossJumps(t *testing.T) {
	t.Run("A→B→A", func(t *testing.T) {
		h, g := jumpHarness(t, mmfake.Options{})
		old := h.postID("Message #10")
		g.hold(query("after", old))
		done := make(chan error, 1)
		go func() { _, err := h.w.JumpTo(context.Background(), "c-town", old); done <- err }()
		g.wait(t)
		h.w.OpenChannel("c-offtopic")
		h.w.OpenChannel("c-town")
		g.open()
		require.ErrorIs(t, <-done, ErrSuperseded)
		assert.Equal(t, town(91, 150), messages(h.view("c-town")))
	})
	t.Run("jump1→jump2", func(t *testing.T) {
		h, g := jumpHarness(t, mmfake.Options{})
		first := h.postID("Message #10")
		g.hold(query("after", first))
		done := make(chan error, 1)
		go func() { _, err := h.w.JumpTo(context.Background(), "c-town", first); done <- err }()
		g.wait(t)
		_, err := h.w.JumpTo(context.Background(), "c-town", h.postID("Message #50"))
		require.NoError(t, err)
		require.ErrorIs(t, <-done, ErrSuperseded)
		g.open()
		assert.Equal(t, append(town(20, 80), town(91, 150)...), messages(h.view("c-town")))
	})
	t.Run("LoadOlder in flight, then a jump", func(t *testing.T) {
		h, g := jumpHarness(t, mmfake.Options{})
		g.hold(query("before", h.postID("Message #91")))
		done := make(chan error, 1)
		go func() { done <- h.w.LoadOlder(context.Background(), "c-town") }()
		g.wait(t)
		_, err := h.w.JumpTo(context.Background(), "c-town", h.postID("Message #10"))
		require.NoError(t, err)
		require.NoError(t, <-done, "a load the jump superseded is not an error")
		g.open()
		assert.Equal(t, append(town(1, 40), town(91, 150)...), messages(h.view("c-town")))
	})
}

// A history operation stuck in its request holds the channel's history
// lock: the jump cancels it instead of waiting.
func TestJumpCancelsHolderOfHistMu(t *testing.T) {
	h, g := jumpHarness(t, mmfake.Options{})
	g.mu.Lock()
	g.ignore = true // the server never answers it
	g.mu.Unlock()
	g.hold(query("before", h.postID("Message #91")))
	done := make(chan error, 1)
	go func() { done <- h.w.LoadOlder(context.Background(), "c-town") }()
	g.wait(t)
	jumped := make(chan error, 1)
	go func() { _, err := h.w.JumpTo(context.Background(), "c-town", h.postID("Message #10")); jumped <- err }()
	select {
	case err := <-jumped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the jump waited for the stuck LoadOlder")
	}
	require.NoError(t, <-done)
	assert.Equal(t, append(town(1, 40), town(91, 150)...), messages(h.view("c-town")))
}

func TestJumpToCRTReplyFetchesOnlyThePost(t *testing.T) {
	h, g := jumpHarness(t, mmfake.Options{CRT: true})
	root := h.postID("Message #10")
	reply := h.fake.ReplyAs("c-town", root, "bob", "an old reply")
	res, err := h.w.JumpTo(context.Background(), "c-town", reply.ID)
	require.NoError(t, err)
	assert.Equal(t, JumpResult{PostID: reply.ID, RootID: root, InFeed: false}, res)
	assert.Contains(t, g.requests(), "/api/v4/posts/"+reply.ID+"?")
	assert.Empty(t, g.pageRequests(reply.ID), "the post only: no channel pages")
	assert.False(t, h.view("c-town").Gap.Open)
}

func TestJumpToGoneOrForeignPost(t *testing.T) {
	h, _ := jumpHarness(t, mmfake.Options{})
	gone := h.postID("Message #5")
	h.fake.DeleteAs(gone)
	_, err := h.w.JumpTo(context.Background(), "c-town", gone)
	assert.ErrorIs(t, err, ErrPostGone)
	_, err = h.w.JumpTo(context.Background(), "c-town", "nosuchpostnosuchpostnosuch")
	assert.ErrorIs(t, err, ErrPostGone)
	foreign := h.fake.FindPost("c-offtopic", "Welcome to off-topic")
	_, err = h.w.JumpTo(context.Background(), "c-town", foreign)
	assert.ErrorIs(t, err, ErrWrongChannel)
	private := h.postID("Message #7")
	h.fake.SetFailure("/posts/"+private, http.StatusForbidden)
	_, err = h.w.JumpTo(context.Background(), "c-town", private)
	assert.ErrorIs(t, err, ErrForbidden)
	_, err = h.w.JumpTo(context.Background(), "c-offtopic", foreign)
	assert.ErrorIs(t, err, ErrNoChannel, "not the open channel")
	assert.Equal(t, town(91, 150), messages(h.view("c-town")), "nothing loaded")
}

// Spec regression: jump → a live stream longer than the window → the gap
// loaded up to the window, nothing lost, nothing twice.
func TestJumpThenBurstThenFillGapLosesNothing(t *testing.T) {
	h, _ := jumpHarness(t, mmfake.Options{})
	_, err := h.w.JumpTo(context.Background(), "c-town", h.postID("Message #10"))
	require.NoError(t, err)
	var burst []string
	for i := 0; i < 100; i++ {
		burst = append(burst, fmt.Sprintf("burst %d", i))
		h.fake.PostAs("c-town", "bob", burst[i])
	}
	h.eventually(func() bool { return h.hasMessage("c-town", "burst 99") }, "the burst arrives")
	closed := false
	for i := 0; i < 10 && !closed; i++ {
		closed, err = h.w.LoadNewer(context.Background(), "c-town")
		require.NoError(t, err)
	}
	require.True(t, closed, "the gap closes")
	v := h.view("c-town")
	assert.False(t, v.Gap.Open)
	assert.Equal(t, append(town(1, 150), burst...), messages(v))
}

func TestLoadNewerNoProgress(t *testing.T) {
	h, g := jumpHarness(t, mmfake.Options{})
	_, err := h.w.JumpTo(context.Background(), "c-town", h.postID("Message #10"))
	require.NoError(t, err)
	// The window is not caught up after a reconnect (its since= is held),
	// and everything after the segment is gone: the gap's pages are empty,
	// and "nothing newer" proves nothing while the window is stale.
	g.hold(func(r *http.Request) bool { return r.URL.Query().Get("since") != "" })
	h.fake.DropConnections(true)
	h.eventually(func() bool { return h.view("c-town").Syncing }, "the window goes stale")
	for i := 41; i <= 150; i++ {
		h.fake.DeleteAsQuiet(h.postID(fmt.Sprintf("Message #%d", i)))
	}
	require.True(t, h.view("c-town").Gap.Open)
	g.clearLog()
	closed, err := h.w.LoadNewer(context.Background(), "c-town")
	assert.ErrorIs(t, err, ErrNoProgress)
	assert.False(t, closed)
	n := 0
	for _, r := range g.requests() {
		if strings.Contains(r, "after=") {
			n++
		}
	}
	assert.Equal(t, 2, n, "two pages without progress, then the error (no retry loop)")
	g.open()
	h.eventually(func() bool { return !h.view("c-town").Syncing }, "caught up")
	closed, err = h.w.LoadNewer(context.Background(), "c-town")
	require.NoError(t, err)
	assert.True(t, closed, "the live window proves the end")
	assert.Equal(t, town(1, 40), messages(h.view("c-town")))
}

// offlineOverflow takes the worker offline, runs change on the server, and
// posts enough to overflow the catch-up (SinceLimit 5) before it comes back.
func offlineOverflow(h *harness, change func()) {
	h.t.Helper()
	h.fake.SetDown(true)
	h.fake.DropConnections(true)
	h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "offline")
	change()
	for i := 0; i < 10; i++ {
		h.fake.PostAs("c-town", "bob", fmt.Sprintf("offline %d", i))
	}
	h.fake.SetDown(false)
}

func TestStaleSegmentIsRevalidated(t *testing.T) {
	h, _ := jumpHarness(t, mmfake.Options{SinceLimit: 5})
	_, err := h.w.JumpTo(context.Background(), "c-town", h.postID("Message #10"))
	require.NoError(t, err)
	edited, deleted := h.postID("Message #12"), h.postID("Message #15")
	offlineOverflow(h, func() {
		h.fake.EditAs(edited, "edited offline")
		h.fake.DeleteAs(deleted)
	})
	h.eventually(func() bool { return h.hasMessage("c-town", "offline 9") }, "caught up")
	h.eventually(func() bool { return !h.view("c-town").Gap.Stale }, "the segment is reread")
	v := h.view("c-town")
	want := append(town(1, 11), "edited offline", "Message #13", "Message #14")
	want = append(want, town(16, 40)...)
	assert.Equal(t, want, messages(v)[:len(want)])
	assert.True(t, v.Gap.Open)
}

func TestRevalidationPartialFailureKeepsSegment(t *testing.T) {
	h, g := jumpHarness(t, mmfake.Options{SinceLimit: 5})
	// History scrolled up to #1: then an overflow makes it stale.
	for h.view("c-town").HasMore {
		require.NoError(t, h.w.LoadOlder(context.Background(), "c-town"))
	}
	deleted := h.postID("Message #3")
	// The reread pages down from the newest held post (#100 once the old
	// window joined the history): its second page, before=#40, fails.
	g.failing(query("before", h.postID("Message #40")))
	offlineOverflow(h, func() { h.fake.DeleteAs(deleted) })
	g.wait(t)
	v := h.view("c-town")
	assert.True(t, v.Gap.Stale, "not covered: stays stale")
	assert.Contains(t, messages(v), "Message #3", "nothing removed")
	g.failing(nil)
	require.NoError(t, h.w.RetryRevalidation(context.Background(), "c-town"))
	v = h.view("c-town")
	assert.False(t, v.Gap.Stale)
	assert.NotContains(t, messages(v), "Message #3")
	assert.Equal(t, "Message #1", v.Posts[0].Message)
}

// One history operation at a time per channel: a second LoadOlder waits
// for the first and continues from the cursor its page moved.
func TestHistoryOperationsRunOneAtATime(t *testing.T) {
	h, g := jumpHarness(t, mmfake.Options{})
	g.hold(query("before", h.postID("Message #91")))
	errs := make(chan error, 2)
	go func() { errs <- h.w.LoadOlder(context.Background(), "c-town") }()
	g.wait(t)
	go func() { errs <- h.w.LoadOlder(context.Background(), "c-town") }()
	time.Sleep(200 * time.Millisecond)
	n := 0
	for _, r := range g.requests() {
		if strings.Contains(r, "before=") {
			n++
		}
	}
	assert.Equal(t, 1, n, "the second waits for the first")
	g.open()
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	v := h.view("c-town")
	assert.Equal(t, town(1, 150), messages(v))
	assert.False(t, v.HasMore)
}
