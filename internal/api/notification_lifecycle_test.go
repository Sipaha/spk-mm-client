package api

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/ws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retractingNotifier struct {
	RecordingNotifier
	mu      sync.Mutex
	removed []string
}

func (n *retractingNotifier) RemoveNotification(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.removed = append(n.removed, id)
}

func (n *retractingNotifier) removals() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.removed...)
}

func TestReadRetractsOnlyItsNotificationAndSuppressesDelayedBurst(t *testing.T) {
	f := newChatFixture(t)
	f.svc.nq.window = 300 * time.Millisecond
	n := &retractingNotifier{}
	f.svc.SetNotifier(n)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.loaded(id, "c-offtopic") && f.loaded(id, "c-town") }, "prefetch")
	p := fake.PostAs("c-offtopic", "bob", "first @alice")
	fake.PostAs("c-town", "bob", "other @alice")
	f.eventually(func() bool { return len(n.List()) == 2 }, "both OS notifications")
	second := fake.PostAs("c-offtopic", "bob", "second @alice")
	f.eventually(func() bool { return f.has(id, "c-offtopic", second.Message) }, "second post applied")
	w := f.svc.manager().Worker(id)
	data, err := json.Marshal(map[string]any{"channel_times": map[string]int64{"c-offtopic": second.CreateAt}})
	require.NoError(t, err)
	change := w.State().ApplyEvent(ws.Event{Type: "multiple_channels_viewed", Data: data})
	f.svc.onChanged(id, change.Change)
	f.eventually(func() bool { return len(n.removals()) == 1 }, "read notification retracted")
	assert.Equal(t, []string{"mm-" + itoa(id) + "-c-offtopic"}, n.removals())
	assert.True(t, f.server(id).Unread, "the other channel stays unread")
	assert.GreaterOrEqual(t, second.CreateAt, p.CreateAt)
	assert.Never(t, func() bool { return len(n.List()) > 2 }, 500*time.Millisecond, 10*time.Millisecond,
		"the delayed burst must not recreate the read notification")
	require.NoError(t, f.svc.Logout(context.Background(), id))
	f.eventually(func() bool { return len(n.removals()) == 2 }, "logout retracts remaining account notification")
}
