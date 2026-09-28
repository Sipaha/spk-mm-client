package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThreadThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	root := fake.SeedThread("c-town", "alice", 3)

	v, err := f.svc.OpenThread(ctx, id, "c-town", root)
	require.NoError(t, err)
	assert.Equal(t, root, v.RootID)
	f.eventually(func() bool {
		v, err := f.svc.GetThread(ctx, id, root)
		return err == nil && v.Loaded && len(v.Posts) == 4
	}, "the thread never loaded")
	require.NoError(t, f.svc.LoadOlderReplies(ctx, id, root))
	require.NoError(t, f.svc.CloseThread(ctx, id))

	_, err = f.svc.OpenThread(ctx, id, "c-nope", root)
	assert.Equal(t, CodeNoChannel, codeOf(err))
	_, err = f.svc.GetThread(ctx, id, "nope")
	assert.Equal(t, CodeNoPost, codeOf(err))
	assert.Equal(t, CodeNoPost, codeOf(f.svc.LoadOlderReplies(ctx, id, "nope")))
	_, err = f.svc.OpenThread(ctx, 999, "c-town", root)
	assert.Error(t, err, "no worker")
}

// A burst of replies in an open thread reaches the UI as one or two
// thread_changed events (coalesced by thread/<srv>/<root>).
func TestThreadChangedIsCoalesced(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	root := fake.SeedThread("c-town", "alice", 2)
	_, err := f.svc.OpenThread(ctx, id, "c-town", root)
	require.NoError(t, err)
	f.eventually(func() bool {
		v, err := f.svc.GetThread(ctx, id, root)
		return err == nil && v.Loaded
	}, "loaded")
	time.Sleep(300 * time.Millisecond)
	for len(f.evs) > 0 {
		<-f.evs
	}
	for i := 0; i < 5; i++ {
		fake.ReplyAs("c-town", root, "bob", "burst")
	}
	deadline := time.After(time.Second)
	n := 0
loop:
	for {
		select {
		case ev := <-f.evs:
			if ev.Type == EventThreadChanged {
				assert.Equal(t, map[string]any{"server_id": id, "root_id": root}, ev.Payload)
				n++
			}
		case <-deadline:
			break loop
		}
	}
	assert.GreaterOrEqual(t, n, 1)
	assert.LessOrEqual(t, n, 2, "5 replies in a burst → 1–2 events, not 5")
}
