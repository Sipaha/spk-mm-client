package api

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

// The thread cache's memory bound, measured rather than counted (threads
// plan, Task 7; TestThreadCacheStaysBounded counts entries): open 30 threads
// of 200 replies one after another, scroll each to its cap, close it — the
// Go heap after a GC comes back to where it was before the first one, give
// or take 0.5 MB (the cache keeps the open thread plus 2 recent ones, trimmed
// to their last page; the plan allows ±1 MB, but a cache that kept every
// closed thread trimmed to its last page — 30 × 60 replies — adds only
// ~0.7 MB, so ±1 MB would not notice it). Not in CI — a heap figure is too
// noisy under -race and on a loaded host:
//
//	SPK_MM_CLIENT_MEMCHECK=1 go test ./internal/api -run TestThreadCacheHeapReturnsToBaseline -count=1 -v
func TestThreadCacheHeapReturnsToBaseline(t *testing.T) {
	if os.Getenv("SPK_MM_CLIENT_MEMCHECK") == "" {
		t.Skip("memory check: set SPK_MM_CLIENT_MEMCHECK=1")
	}
	const threads, replies = 30, state.ThreadMaxReplies
	f := newChatFixture(t)
	fake := mmfake.Start(mmfake.Options{CRT: true})
	t.Cleanup(fake.Close)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.server(id).State == "live" && f.loaded(id, "c-town") }, "prefetch")

	// The fake shares this process: seed every thread before the baseline,
	// so what the fake holds is in it too.
	roots := make([]string, threads)
	for i := range roots {
		roots[i] = fake.SeedThread("c-town", "bob", replies)
	}
	time.Sleep(2 * time.Second) // the seed's WS events settle
	for len(f.evs) > 0 {
		<-f.evs
	}
	base := settledHeap()

	var peak uint64
	for i, root := range roots {
		_, err := f.svc.OpenThread(ctx, id, "c-town", root)
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			v, err := f.svc.GetThread(ctx, id, root)
			if err != nil || !v.Loaded || v.Syncing {
				return false
			}
			if v.HasMore && !v.Capped {
				_ = f.svc.LoadOlderReplies(ctx, id, root) // scroll up: a page of 60 at a time
				return false
			}
			return true
		}, 20*time.Second, 50*time.Millisecond, "thread %d never reached its cap", i)
		v, err := f.svc.GetThread(ctx, id, root)
		require.NoError(t, err)
		require.Len(t, v.Posts, 1+replies, "the root and all %d replies", replies)
		if i == 0 {
			peak = settledHeap()
		}
		require.NoError(t, f.svc.CloseThread(ctx, id))
		for len(f.evs) > 0 {
			<-f.evs
		}
	}
	after := settledHeap()
	mb := func(b uint64) string { return fmt.Sprintf("%.2f MB", float64(b)/(1<<20)) }
	t.Logf("go heap after GC: baseline %s, one open thread of %d replies %s, after %d threads opened and closed %s",
		mb(base), replies, mb(peak), threads, mb(after))
	assert.InDelta(t, float64(base), float64(after), 1<<19, "the heap comes back to the baseline ±0.5 MB")
}

// settledHeap: live heap bytes after a full collection.
func settledHeap() uint64 {
	var m runtime.MemStats
	for range 3 {
		runtime.GC()
	}
	debug.FreeOSMemory()
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}
