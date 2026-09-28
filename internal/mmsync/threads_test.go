package mmsync

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

func (h *harness) thread(root string) state.ThreadView {
	v, _ := h.w.State().ThreadView(root)
	return v
}

func (h *harness) threadHas(root, msg string) int {
	n := 0
	for _, p := range h.thread(root).Posts {
		if p.Message == msg && !p.Pending {
			n++
		}
	}
	return n
}

func (h *harness) threadHits(root string) int {
	return h.fake.Hits("GET", "/api/v4/posts/"+root+"/thread")
}

// crtHarness: a CRT server, alice live with every window loaded.
func crtHarness(t *testing.T) *harness {
	h := newHarness(t, mmfake.Options{CRT: true})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	return h
}

func (h *harness) openThread(root string, posts int) {
	h.t.Helper()
	_, ok := h.w.OpenThread("c-town", root)
	require.True(h.t, ok)
	h.eventually(func() bool { v := h.thread(root); return v.Loaded && !v.Syncing && len(v.Posts) == posts }, "the thread never loaded")
}

// Review focus 3: a reply posted while the stream was lost (the server
// forgot our session: no resume) shows in the open thread once the
// worker is back — exactly once.
func TestOpenThreadCatchesUpAfterGap(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 5)
	h.openThread(root, 6)

	h.fake.SetDown(true)
	h.fake.DropConnections(true)
	h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "the drop went unnoticed")
	h.fake.ReplyAs("c-town", root, "bob", "in the gap")
	h.fake.SetDown(false)
	h.live()
	h.eventually(func() bool { return h.threadHas(root, "in the gap") == 1 && !h.thread(root).Syncing },
		"the reply from the gap never reached the open thread")
	v := h.thread(root)
	assert.Len(t, v.Posts, 7)
	assert.Equal(t, int64(6), v.Posts[0].ReplyCount)
	h.fake.ReplyAs("c-town", root, "carol", "after the gap")
	h.eventually(func() bool { return h.threadHas(root, "after the gap") == 1 }, "live again")
	assert.Equal(t, 1, h.threadHas(root, "in the gap"), "nothing doubled")
}

func TestOlderRepliesStopAtTheCap(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 250)
	h.openThread(root, 1+state.ThreadPage)
	assert.True(t, h.thread(root).HasMore)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		require.NoError(t, h.w.LoadOlderReplies(ctx, root))
	}
	v := h.thread(root)
	require.Len(t, v.Posts, 1+state.ThreadMaxReplies)
	assert.True(t, v.Capped)
	assert.False(t, v.HasMore)
	assert.Equal(t, "Reply 51", v.Posts[1].Message, "the newest 200")
	assert.Equal(t, "Reply 250", v.Posts[state.ThreadMaxReplies].Message)
	hits := h.threadHits(root)
	require.NoError(t, h.w.LoadOlderReplies(ctx, root))
	assert.Equal(t, hits, h.threadHits(root), "nothing is requested past the cap")
	assert.ErrorIs(t, h.w.LoadOlderReplies(ctx, "nope"), ErrNoPost)
}

func TestRootDeletedMarksTheThread(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 2)
	h.openThread(root, 3)
	h.fake.DeleteAs(root)
	h.eventually(func() bool { v := h.thread(root); return v.RootDeleted && len(v.Posts) == 0 }, "post_deleted of the root")

	gone := h.fake.SeedThread("c-town", "alice", 2)
	h.fake.DeleteAs(gone)
	_, ok := h.w.OpenThread("c-town", gone)
	require.True(t, ok)
	h.eventually(func() bool { v := h.thread(gone); return v.RootDeleted && v.Loaded && v.Error == "" }, "404 on load")
}

func TestThreadFetchIsSingleFlight(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 3)
	h.fake.SetLatency("/thread", 300*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.w.OpenThread("c-town", root)
		}()
	}
	wg.Wait()
	h.eventually(func() bool { return h.thread(root).Loaded }, "loaded")
	assert.Equal(t, 1, h.threadHits(root), "one request for all the opens")
	_, ok := h.w.OpenThread("c-nope", root)
	assert.False(t, ok)
}

// Review focus 2 (the worker's part): a stopped worker holds no thread.
func TestStoppedWorkerLetsTheThreadsGo(t *testing.T) {
	h := crtHarness(t)
	var roots []string
	for i := 0; i < 2; i++ {
		root := h.fake.SeedThread("c-town", "alice", 3)
		h.openThread(root, 4)
		roots = append(roots, root)
	}
	require.NotEmpty(t, h.w.State().OpenThreadID())
	h.stop()
	assert.Empty(t, h.w.State().OpenThreadID())
	for _, root := range roots {
		_, ok := h.w.State().ThreadView(root)
		assert.False(t, ok, "thread %s still held", root)
	}
}

// CRT switched without a preferences_changed (the admin changed the
// config): the open thread is read again in the new mode.
func TestCRTSwitchReloadsTheOpenThread(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 2)
	h.openThread(root, 3)
	hits := h.threadHits(root)
	h.fake.SetCollapsedThreads("disabled")
	h.fake.DropConnections(true)
	h.eventually(func() bool {
		v := h.thread(root)
		return !v.CRT && v.Loaded && len(v.Posts) == 3 && h.threadHits(root) > hits
	}, fmt.Sprintf("the open thread was not read again (hits %d)", hits))
	assert.Equal(t, root, h.w.State().OpenThreadID(), "the panel stays open")
}
