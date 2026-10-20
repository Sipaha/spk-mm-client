package desktop

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type notificationUpdate struct {
	id     string
	remove bool
	run    func() error
}

// notificationUpdates retains the latest desired operation for each OS ID.
// In particular, a read cannot be lost when the bounded async sender is full
// or temporarily dropping work behind a hung D-Bus call. A retry reoffers
// pending updates; it never starts a second OS call while one is stuck.
type notificationUpdates struct {
	mu      sync.Mutex
	pending map[string]*notificationUpdate
	active  map[string]bool // accepted OS groups, including a send still in flight
	full    bool
	out     *asyncSender[*notificationUpdate]
	cancel  context.CancelFunc
}

func newNotificationUpdates(ctx context.Context, size int, timeout, retry time.Duration) *notificationUpdates {
	ctx, cancel := context.WithCancel(ctx)
	u := &notificationUpdates{pending: map[string]*notificationUpdate{}, active: map[string]bool{}, cancel: cancel}
	u.out = newAsyncSender(ctx, "notification", size, timeout, func(job *notificationUpdate) {
		u.mu.Lock()
		current := u.pending[job.id] == job
		u.mu.Unlock()
		if !current {
			return
		}
		if err := job.run(); err != nil {
			slog.Warn("notification update failed", "id", job.id, "err", err)
			return
		}
		u.mu.Lock()
		if u.pending[job.id] == job {
			delete(u.pending, job.id)
			if job.remove {
				delete(u.active, job.id)
				u.full = false
			}
		}
		u.mu.Unlock()
	})
	go func() {
		ticker := time.NewTicker(retry)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				u.mu.Lock()
				for _, job := range u.pending {
					u.out.enqueue(job)
				}
				u.mu.Unlock()
			}
		}
	}()
	return u
}

func (u *notificationUpdates) put(id string, run func() error) {
	u.update(id, false, run)
}

func (u *notificationUpdates) remove(id string, run func() error) {
	u.update(id, true, run)
}

func (u *notificationUpdates) update(id string, remove bool, run func() error) {
	job := &notificationUpdate{id: id, remove: remove, run: run}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.out.stopped() {
		return
	}
	if remove && !u.active[id] {
		return // a rejected send never created an OS notification to retract
	}
	if !remove && !u.active[id] {
		if len(u.active) >= 512 {
			if !u.full {
				slog.Warn("notification dropped: outstanding OS group limit reached", "limit", 512)
				u.full = true
			}
			return
		}
		u.active[id] = true
	}
	u.pending[id] = job
	u.out.enqueue(job)
}

func (u *notificationUpdates) stop() { u.cancel(); u.out.stop() }
