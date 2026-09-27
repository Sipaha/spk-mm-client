package desktop

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPendingAnswerArrivesBeforeTheWait(t *testing.T) {
	var freed atomic.Int32
	p := newPending(func(int) { freed.Add(1) })
	p.answer(7)
	v, err := p.wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 7, v)
	assert.Zero(t, freed.Load())
}

func TestPendingAnswerWakesTheWaiter(t *testing.T) {
	p := newPending[string](nil)
	go func() {
		time.Sleep(10 * time.Millisecond)
		p.answer("targets")
	}()
	v, err := p.wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "targets", v)
}

// The asker gives up; the owner's late answer (C memory, a pixbuf) is
// released, not leaked, and answer never blocks the GTK main thread.
func TestPendingLateAnswerIsFreed(t *testing.T) {
	var freed atomic.Int32
	p := newPending(func(v int) { freed.Add(int32(v)) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := p.wait(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	done := make(chan struct{})
	go func() {
		p.answer(5)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("answer blocked")
	}
	assert.EqualValues(t, 5, freed.Load())
}

func TestPendingRacingAnswerAndTimeoutNeitherLeaksNorLoses(t *testing.T) {
	for range 200 {
		var freed, got atomic.Int32
		p := newPending(func(int) { freed.Add(1) })
		ctx, cancel := context.WithCancel(context.Background())
		go p.answer(1)
		go cancel()
		if _, err := p.wait(ctx); err == nil {
			got.Add(1)
		}
		cancel()
		// Whichever way it went, the answer ends up either taken or freed.
		assert.Eventually(t, func() bool { return got.Load()+freed.Load() == 1 }, time.Second, time.Millisecond)
	}
}
