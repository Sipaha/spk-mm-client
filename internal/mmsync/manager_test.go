package mmsync

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/store"
)

func TestManagerStartsSignedInServersOnly(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	other, err := h.store.AddServer(context.Background(), store.Server{URL: "https://signed-out.example", Name: "Out"})
	require.NoError(t, err)
	m := NewManager(context.Background(), h.config())
	defer m.Close()
	require.NoError(t, m.StartAll(context.Background()))
	require.NotNil(t, m.Worker(h.srv.ID))
	assert.Nil(t, m.Worker(other.ID))
	h.w = m.Worker(h.srv.ID)
	h.live()
	m.Stop(h.srv.ID)
	assert.Nil(t, m.Worker(h.srv.ID))
	entries, err := h.store.LoadCache(context.Background(), h.srv.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "stop flushes the snapshot")
}

func TestManagerStartIsAtomicAndNoopAfterClose(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	var started, ended atomic.Int64
	cfg := h.config()
	cfg.Hooks.Status = func(_ int64, s Status) {
		switch s {
		case StatusConnecting:
			started.Add(1)
		case StatusOff:
			ended.Add(1)
		}
	}
	m := NewManager(context.Background(), cfg)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m.Start(h.srv) }()
	}
	wg.Wait()
	require.NotNil(t, m.Worker(h.srv.ID))
	require.Eventually(t, func() bool { return started.Load()-ended.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int64(1), started.Load()-ended.Load(), "exactly one worker runs")
	m.Close()
	assert.Equal(t, started.Load(), ended.Load(), "no orphaned worker survives Close")

	m.Start(h.srv)
	assert.Nil(t, m.Worker(h.srv.ID), "Start after Close is a no-op")
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, started.Load(), ended.Load())
}
