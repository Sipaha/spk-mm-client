package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationReadSupersedesQueuedSendAndSurvivesFullSender(t *testing.T) {
	u := newNotificationUpdates(context.Background(), 1, time.Second, 10*time.Millisecond)
	defer u.stop()
	started, release := make(chan struct{}), make(chan struct{})
	u.put("block", func() error { close(started); <-release; return nil })
	<-started
	var shown, removed atomic.Int32
	u.put("channel", func() error { shown.Add(1); return nil })
	u.remove("channel", func() error { removed.Add(1); return nil })
	close(release)
	require.Eventually(t, func() bool { return removed.Load() == 1 }, time.Second, time.Millisecond)
	assert.Zero(t, shown.Load(), "a queued stale notification must not reappear after the read")
}

func TestOutstandingNotificationGroupsStayBoundedWhileBackendIsStuck(t *testing.T) {
	u := newNotificationUpdates(context.Background(), 1, time.Second, time.Second)
	defer u.stop()
	started, release := make(chan struct{}), make(chan struct{})
	u.put("block", func() error { close(started); <-release; return nil })
	<-started
	defer close(release)
	for i := range 1000 {
		u.put(fmt.Sprint(i), func() error { return nil })
	}
	u.remove("999", func() error { return nil }) // a rejected send needs no removal
	u.mu.Lock()
	defer u.mu.Unlock()
	assert.Len(t, u.pending, 512)
	assert.Len(t, u.active, 512)
}

func TestNotificationRemovalRetriesAfterFailureAndHungSend(t *testing.T) {
	u := newNotificationUpdates(context.Background(), 1, 20*time.Millisecond, 10*time.Millisecond)
	defer u.stop()
	started, release := make(chan struct{}), make(chan struct{})
	u.put("show", func() error { close(started); <-release; return nil })
	<-started
	var attempts atomic.Int32
	u.put("read", func() error { return nil })
	u.remove("read", func() error {
		if attempts.Add(1) == 1 {
			return errors.New("temporary daemon failure")
		}
		return nil
	})
	require.Eventually(t, func() bool { return u.out.dropped.Load() > 0 }, time.Second, time.Millisecond)
	close(release)
	require.Eventually(t, func() bool { return attempts.Load() == 2 }, time.Second, time.Millisecond)
	assert.Eventually(t, func() bool { u.mu.Lock(); defer u.mu.Unlock(); return len(u.pending) == 0 }, time.Second, time.Millisecond)
}
