package desktop

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfferLatestKeepsOnlyTheNewestAndNeverBlocks(t *testing.T) {
	ch := make(chan int, 1)
	var wg sync.WaitGroup
	for i := 1; i <= 50; i++ { // several producers, nobody reads: must not block
		wg.Add(1)
		go func() { defer wg.Done(); offerLatest(ch, i) }()
	}
	wg.Wait()
	offerLatest(ch, 100)
	assert.Equal(t, 100, <-ch)
	assert.Empty(t, ch)
}

func TestAsyncSenderRunsInOrderOffTheCaller(t *testing.T) {
	var mu sync.Mutex
	var got []int
	done := make(chan struct{})
	s := newAsyncSender(context.Background(), "test", 4, time.Second, func(v int) {
		mu.Lock()
		got = append(got, v)
		n := len(got)
		mu.Unlock()
		if n == 3 {
			close(done)
		}
	})
	defer s.stop()
	for i := 1; i <= 3; i++ {
		assert.True(t, s.enqueue(i))
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sends did not run")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []int{1, 2, 3}, got)
}

// A hung backend (a D-Bus Notify with no timeout) is detached after the
// timeout; while it stays stuck new items are dropped (no second stuck call,
// callers never wait), and once it returns sending resumes.
func TestAsyncSenderDetachesAHungSendAndResumes(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var got []int
	s := newAsyncSender(context.Background(), "test", 4, 50*time.Millisecond, func(v int) {
		mu.Lock()
		got = append(got, v)
		mu.Unlock()
		if v == 1 {
			<-release
		}
	})
	defer s.stop()
	sent := func(v int) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(got, v)
	}

	require.True(t, s.enqueue(1))
	require.Eventually(t, func() bool { return sent(1) }, time.Second, 5*time.Millisecond)
	start := time.Now()
	assert.True(t, s.enqueue(2))
	assert.Less(t, time.Since(start), 20*time.Millisecond, "enqueue never waits")
	require.Eventually(t, func() bool { return s.dropped.Load() == 1 }, time.Second, 5*time.Millisecond,
		"2 is dropped while 1 is stuck")

	close(release)
	require.Eventually(t, func() bool { s.enqueue(3); return sent(3) }, 2*time.Second, 20*time.Millisecond,
		"sending resumes once the stuck call returns")
	assert.False(t, sent(2))
}

func TestAsyncSenderStops(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	s := newAsyncSender(ctx, "test", 4, time.Second, func(int) { calls.Add(1) })
	s.stop()
	s.stop() // idempotent
	assert.False(t, s.enqueue(1))

	s2 := newAsyncSender(ctx, "test", 4, time.Second, func(int) { calls.Add(1) })
	cancel()
	assert.False(t, s2.enqueue(1))
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, calls.Load())
}
