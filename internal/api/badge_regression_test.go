package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
)

func TestMultiServerBadgeAggregationReadLogoutAndRemove(t *testing.T) {
	f := newFixture(t)
	fakeB := mmfake.Start(mmfake.Options{SiteName: "Second MM"})
	t.Cleanup(fakeB.Close)

	ctx := context.Background()
	require.NoError(t, f.svc.Start(ctx))

	var (
		mu      sync.Mutex
		history []Badge
	)
	f.svc.OnBadge(func(b Badge) {
		mu.Lock()
		history = append(history, b)
		mu.Unlock()
	})
	latest := func() Badge {
		mu.Lock()
		defer mu.Unlock()
		return history[len(history)-1]
	}

	mu.Lock()
	require.Equal(t, []Badge{{}}, history, "OnBadge immediately delivers initial plain total")
	mu.Unlock()

	srvA, err := f.svc.AddServer(ctx, f.fake.URL())
	require.NoError(t, err)
	_, err = f.svc.LoginWithPassword(ctx, srvA.ID, "alice", "secret")
	require.NoError(t, err)

	srvB, err := f.svc.AddServer(ctx, fakeB.URL())
	require.NoError(t, err)
	_, err = f.svc.LoginWithPassword(ctx, srvB.ID, "alice", "secret")
	require.NoError(t, err)

	// Wait for both workers to become live.
	require.Eventually(t, func() bool {
		list, _ := f.svc.ListServers(ctx)
		return len(list) == 2 && list[0].State == "live" && list[1].State == "live"
	}, 5*time.Second, 20*time.Millisecond)

	// 1. Plain unread on server A -> Unread: true, Mentions: 0.
	f.fake.PostAs("c-offtopic", "bob", "plain unread on A")
	require.Eventually(t, func() bool {
		return latest() == Badge{Unread: true, Mentions: 0}
	}, 5*time.Second, 20*time.Millisecond)

	// Late subscriber receives current non-zero total synchronously.
	var lateGot Badge
	f.svc.OnBadge(func(b Badge) { lateGot = b })
	assert.Equal(t, Badge{Unread: true, Mentions: 0}, lateGot)

	// 2. Mention on server B -> Unread: true, Mentions: 1 (OR Unread, SUM Mentions).
	fakeB.PostAs("c-offtopic", "bob", "ping @alice on B")
	require.Eventually(t, func() bool {
		return latest() == Badge{Unread: true, Mentions: 1}
	}, 5*time.Second, 20*time.Millisecond)

	// 3. Reading server B clears B's mention while preserving server A's plain unread.
	require.NoError(t, f.svc.SelectServer(ctx, srvB.ID))
	require.NoError(t, f.svc.SetFocused(ctx, true))
	_, err = f.svc.OpenChannel(ctx, srvB.ID, "c-offtopic")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return latest() == Badge{Unread: true, Mentions: 0}
	}, 5*time.Second, 20*time.Millisecond, "reading server B must preserve server A's unread")

	// 4. Reading server A clears the aggregated badge to plain.
	require.NoError(t, f.svc.SelectServer(ctx, srvA.ID))
	_, err = f.svc.OpenChannel(ctx, srvA.ID, "c-offtopic")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return latest() == Badge{}
	}, 5*time.Second, 20*time.Millisecond)

	// 5. Logout of an unread server drops its contribution from total.
	fakeB.PostAs("c-offtopic", "bob", "unread before logout")
	require.Eventually(t, func() bool {
		return latest() == Badge{Unread: true, Mentions: 0}
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, f.svc.Logout(ctx, srvB.ID))
	require.Eventually(t, func() bool {
		return latest() == Badge{}
	}, 5*time.Second, 20*time.Millisecond)

	// 6. Re-login and RemoveServer of an unread server also clears total.
	_, err = f.svc.LoginWithPassword(ctx, srvB.ID, "alice", "secret")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		list, _ := f.svc.ListServers(ctx)
		return len(list) == 2 && list[1].State == "live" && latest() == (Badge{Unread: true, Mentions: 0})
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, f.svc.RemoveServer(ctx, srvB.ID))
	require.Eventually(t, func() bool {
		return latest() == Badge{}
	}, 5*time.Second, 20*time.Millisecond)
}
