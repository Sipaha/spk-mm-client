package mmsync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/state"
)

func TestFetchQueuePriorityDedupAndCancel(t *testing.T) {
	q := newFetchQueue()
	q.push(state.SyncItem{ChannelID: "a", Priority: 3})
	q.push(state.SyncItem{ChannelID: "b", Priority: 1})
	q.push(state.SyncItem{ChannelID: "c", Priority: 3})
	q.push(state.SyncItem{ChannelID: "a", Priority: 0}) // boost
	q.push(state.SyncItem{ChannelID: "b", Priority: 4}) // never demote
	ctx := context.Background()
	var got []string
	for i := 0; i < 3; i++ {
		it, ok := q.pop(ctx)
		require.True(t, ok)
		got = append(got, it.ChannelID)
	}
	assert.Equal(t, []string{"a", "b", "c"}, got)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	_, ok := q.pop(cctx)
	assert.False(t, ok, "empty queue blocks until ctx is done")
}
