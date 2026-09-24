package desktop

import (
	"sync"
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
	s := newAsyncSender(4, func(v int) {
		mu.Lock()
		got = append(got, v)
		n := len(got)
		mu.Unlock()
		if n == 3 {
			close(done)
		}
	})
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

// A hung backend (a D-Bus Notify with no timeout) must not stall callers:
// once the queue is full, new items are dropped immediately.
func TestAsyncSenderDropsWhenTheBackendHangs(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	started := make(chan struct{}, 1)
	s := newAsyncSender(2, func(int) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
	})
	require.True(t, s.enqueue(1))
	<-started // 1 is stuck in send
	assert.True(t, s.enqueue(2))
	assert.True(t, s.enqueue(3))
	start := time.Now()
	assert.False(t, s.enqueue(4), "queue full → dropped")
	assert.Less(t, time.Since(start), 100*time.Millisecond)
}
