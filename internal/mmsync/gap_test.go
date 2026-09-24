package mmsync

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mmfake"
)

// Offline longer than the since= margin, the server lost our stream: the
// catch-up must start where the old stream ended, not where the new one
// began (item 1 of the stage-2 final review).
func TestLongOfflineGapWithLossCatchesUpFromStreamEnd(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	clk := h.useClock()
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.SetDown(true)
	h.fake.DropConnections(true)
	h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "the drop went unnoticed")
	streamEnd := clk.now().UnixMilli()
	h.fake.PostAs("c-offtopic", "bob", "sent during the gap")
	clk.advance(10 * time.Minute)
	h.fake.SetLatency("/categories", time.Second) // keep the resync in flight for a while
	h.fake.SetDown(false)
	assert.Never(t, func() bool { return h.liveAt() > streamEnd+time.Minute.Milliseconds() }, 700*time.Millisecond, 20*time.Millisecond,
		"live/at must not move past the stream end before the new stream is proven continuous")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "sent during the gap") }, "the post sent during the gap was never fetched")
	h.eventually(func() bool { return !h.view("c-offtopic").Syncing }, "window stayed stale")
	h.eventually(func() bool { return h.liveAt() > streamEnd+5*time.Minute.Milliseconds() }, "live/at advances once the stream is proven")
}

// A metadata refresh must not roll back counters raised by events that
// arrived while it was being fetched (item 2).
func TestMetaRefreshKeepsCountersRaisedByEvents(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.SetLatency("/categories", 300*time.Millisecond)
	// The refresh (debounced, then slowed down) overlaps the posts; the
	// posts go on well after it, so no later refresh repairs the counts.
	h.fake.AddChannel("c-extra", "Extra", "alice", "bob")
	for i := 0; i < 120; i++ {
		h.fake.PostAs("c-offtopic", "bob", fmt.Sprintf("hey @alice %d", i))
		time.Sleep(10 * time.Millisecond)
	}
	want := int(h.fake.Member("c-offtopic", "alice").MentionCount)
	require.Equal(t, 120, want)
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "hey @alice 119") && h.view("c-extra").Loaded }, "posts and refresh landed")
	h.eventually(func() bool { return h.mentions("c-offtopic") == want },
		fmt.Sprintf("mentions diverged from the server: want %d", want))
}

// The first message in a channel we were just added to notifies (item 3).
func TestFirstPostInNewChannelNotifies(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.AddChannel("c-new", "Brand New", "alice", "bob")
	h.fake.PostAs("c-new", "bob", "welcome @alice")
	h.eventually(func() bool { return h.notified("welcome @alice") }, "no notification for the first post")
	h.eventually(func() bool { return h.hasMessage("c-new", "welcome @alice") }, "post not in the window")
	assert.Equal(t, 1, h.mentions("c-new"), "counted once")
}

// A server that keeps refusing our resume (bad connection_id) must not be
// retried forever: after a few attempts the worker reconnects fresh and
// resyncs (item 6).
func TestRefusedResumeFallsBackToFreshConnection(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.fake.RejectResumes(true)
	h.fake.DropConnections(false)
	h.fake.PostAs("c-offtopic", "bob", "while refused")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "while refused") }, "the worker kept retrying the refused resume")
	h.eventually(func() bool { return !h.view("c-offtopic").Syncing }, "window stayed stale")
}

func TestLiveMarkMovesOnlyWhileProven(t *testing.T) {
	var m liveMark
	assert.True(t, m.prove(100))
	assert.False(t, m.prove(150), "already proven")
	assert.True(t, m.advance(200))
	assert.True(t, m.end(300), "stream was proven: the gap starts at its end")
	assert.Equal(t, int64(300), m.gapStart())
	assert.False(t, m.advance(900), "not proven: the flush loop must not move it")
	assert.False(t, m.end(950), "an unproven stream does not move the gap start")
	assert.Equal(t, int64(300), m.gapStart())
	m.prove(1000)
	assert.Equal(t, int64(1000), m.gapStart())
}
