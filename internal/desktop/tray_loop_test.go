package desktop

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/api"
)

func TestRunTrayBadgeWaitsForReadyCoalescesBurstsAndSkipsRedundantIconDecodes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan api.Badge, 1)
	readyGate := make(chan struct{})

	var (
		mu          sync.Mutex
		texts       []string
		icons       []trayIcon
		blockUnread chan struct{}
		inUnread    chan struct{}
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		runTrayBadge(ctx, ch, "en", trayApplier{
			waitReady: func(c context.Context) bool {
				select {
				case <-c.Done():
					return false
				case <-readyGate:
					return true
				}
			},
			setText: func(s string) {
				mu.Lock()
				texts = append(texts, s)
				mu.Unlock()
			},
			setIcon: func(ic trayIcon) {
				mu.Lock()
				icons = append(icons, ic)
				blk := blockUnread
				sig := inUnread
				mu.Unlock()
				if ic == iconUnread && blk != nil {
					close(sig)
					<-blk
				}
			},
		})
	}()

	// Initial callback before tray is ready is held in ch without touching tray.
	offerLatest(ch, api.Badge{})
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	assert.Empty(t, texts)
	assert.Empty(t, icons)
	mu.Unlock()

	close(readyGate)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(icons) == 1 && icons[0] == iconPlain && len(texts) == 1 && texts[0] == "spk-mm-client"
	}, time.Second, 5*time.Millisecond)

	// Consecutive mention count updates change hover text every time without re-decoding iconMention.
	offerLatest(ch, api.Badge{Unread: true, Mentions: 1})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(icons) == 2 && icons[1] == iconMention && texts[len(texts)-1] == "spk-mm-client — 1 mention"
	}, time.Second, 5*time.Millisecond)

	offerLatest(ch, api.Badge{Unread: true, Mentions: 3})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return texts[len(texts)-1] == "spk-mm-client — 3 mentions"
	}, time.Second, 5*time.Millisecond)
	mu.Lock()
	assert.Equal(t, []trayIcon{iconPlain, iconMention}, icons, "iconMention PNG must not be re-pushed when only count changes")
	mu.Unlock()

	// Rapid burst while iconUnread is being applied on GTK/D-Bus must coalesce and still apply final plain state.
	mu.Lock()
	blockUnread = make(chan struct{})
	inUnread = make(chan struct{})
	mu.Unlock()

	offerLatest(ch, api.Badge{Unread: true, Mentions: 0})
	<-inUnread

	// Producer fires mention -> unread -> plain while consumer is blocked inside setIcon(iconUnread).
	offerLatest(ch, api.Badge{Unread: true, Mentions: 2})
	offerLatest(ch, api.Badge{Unread: true, Mentions: 0})
	offerLatest(ch, api.Badge{Unread: false, Mentions: 0})

	mu.Lock()
	blk := blockUnread
	blockUnread = nil
	mu.Unlock()
	close(blk)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return icons[len(icons)-1] == iconPlain && texts[len(texts)-1] == "spk-mm-client"
	}, time.Second, 5*time.Millisecond, "final plain badge must never be lost after burst coalescing")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runTrayBadge did not exit on context cancel")
	}
}

// R1 regression: readiness absent past the warning threshold must NOT kill the
// badge consumer; once readiness appears later, the held non-zero badge applies
// and subsequent read returns the tray to plain without requiring a restart.
func TestRunTrayBadgeSurvivesDelayedReadinessPastWarningThreshold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan api.Badge, 1)
	var ready atomic.Bool
	var warns atomic.Int32

	var (
		mu    sync.Mutex
		texts []string
		icons []trayIcon
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		runTrayBadge(ctx, ch, "en", trayApplier{
			waitReady: func(c context.Context) bool {
				return waitUntilReady(c, 25*time.Millisecond, 5*time.Millisecond, 10*time.Millisecond,
					func(context.Context) bool { return ready.Load() },
					func() { warns.Add(1) },
				)
			},
			setText: func(s string) {
				mu.Lock()
				texts = append(texts, s)
				mu.Unlock()
			},
			setIcon: func(ic trayIcon) {
				mu.Lock()
				icons = append(icons, ic)
				mu.Unlock()
			},
		})
	}()

	// Badge updates arrive while tray readiness is still absent.
	offerLatest(ch, api.Badge{Unread: true, Mentions: 0})
	offerLatest(ch, api.Badge{Unread: true, Mentions: 2})

	// Wait well past warnAfter: warning fires once, consumer stays alive.
	require.Eventually(t, func() bool { return warns.Load() == 1 }, time.Second, 5*time.Millisecond)
	time.Sleep(40 * time.Millisecond)
	assert.Equal(t, int32(1), warns.Load(), "warning logs only once")
	select {
	case <-done:
		t.Fatal("consumer exited permanently on delayed readiness")
	default:
	}

	// Readiness finally arrives: latest held mention badge applies immediately.
	ready.Store(true)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(icons) == 1 && icons[0] == iconMention &&
			len(texts) == 1 && texts[0] == "spk-mm-client — 2 mentions"
	}, time.Second, 5*time.Millisecond)

	// Reading clears back to plain.
	offerLatest(ch, api.Badge{})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(icons) == 2 && icons[1] == iconPlain &&
			len(texts) == 2 && texts[1] == "spk-mm-client"
	}, time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not exit on context cancel")
	}
}

func TestRunTrayBadgeExitsOnContextCancelDuringWaitReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan api.Badge, 1)
	offerLatest(ch, api.Badge{Unread: true, Mentions: 1})
	called := false
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	runTrayBadge(ctx, ch, "ru", trayApplier{
		waitReady: func(c context.Context) bool {
			return waitUntilReady(c, time.Second, 5*time.Millisecond, 10*time.Millisecond,
				func(context.Context) bool { return false },
				nil,
			)
		},
		setText: func(string) { called = true },
		setIcon: func(trayIcon) { called = true },
	})
	assert.False(t, called)
}
