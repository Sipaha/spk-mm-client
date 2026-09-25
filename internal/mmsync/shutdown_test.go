package mmsync

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

func TestActionsDuringStopDoNotPanicAndFailPending(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.SetFocused(true)
	var stop atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_ = h.w.Send("c-offtopic", "hammer")
				h.w.OpenChannel("c-town")
				h.w.OpenChannel("c-offtopic")
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	h.stop() // Run returns while the hammering goes on
	time.Sleep(20 * time.Millisecond)
	stop.Store(true)
	wg.Wait()

	require.NoError(t, h.w.Send("c-offtopic", "after stop"))
	var p state.PostView
	for _, q := range h.view("c-offtopic").Posts {
		if q.Message == "after stop" {
			p = q
		}
	}
	assert.True(t, p.Failed, "work refused after stop must not hang as pending")
}

func TestFinalFlushComesAfterBackgroundWork(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	require.True(t, h.w.goBG(func(ctx context.Context) {
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond) // finishes well after the stop began
		h.w.st.SetUsers([]model.User{{ID: "late", Username: "late"}})
	}))
	h.stop()
	entries, err := h.store.LoadCache(context.Background(), h.srv.ID)
	require.NoError(t, err)
	found := false
	for _, e := range entries {
		found = found || (e.Kind == "user" && e.Key == "late")
	}
	assert.True(t, found, "a change made by background work during shutdown must reach the final flush")
}
