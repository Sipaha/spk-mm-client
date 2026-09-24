package desktop

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// offerLatest puts v into a 1-slot channel, replacing whatever value is still
// waiting there. It never blocks, whatever the number of producers: the
// consumer only ever sees the newest value (tray badge — latest wins).
func offerLatest[T any](ch chan T, v T) {
	for {
		select {
		case ch <- v:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

// asyncSender runs send one call at a time on its own goroutine, in enqueue
// order (replaces_id needs the previous call's result). enqueue never blocks.
//
// send may hang (a D-Bus call without a timeout): each call runs in its own
// goroutine and is waited for up to timeout, then detached. At most one call
// is detached; while it is still stuck, new items are dropped (one warning
// per episode), and sending resumes once it returns. After stop or ctx
// cancellation no new call starts.
type asyncSender[T any] struct {
	what     string
	ch       chan T
	quit     chan struct{}
	ctxDone  <-chan struct{}
	stopOnce sync.Once
	timeout  time.Duration
	send     func(T)
	dropped  atomic.Int64 // items never sent (queue full or backend stuck)
	full     atomic.Bool  // queue-full warning already logged
}

func newAsyncSender[T any](ctx context.Context, what string, size int, timeout time.Duration, send func(T)) *asyncSender[T] {
	s := &asyncSender[T]{what: what, ch: make(chan T, size), quit: make(chan struct{}), ctxDone: ctx.Done(), timeout: timeout, send: send}
	go s.run()
	return s
}

func (s *asyncSender[T]) stopped() bool {
	select {
	case <-s.quit:
		return true
	case <-s.ctxDone:
		return true
	default:
		return false
	}
}

// stop ends the sender: queued items are discarded, enqueue returns false.
func (s *asyncSender[T]) stop() { s.stopOnce.Do(func() { close(s.quit) }) }

// enqueue reports whether v was queued (false: stopped or queue full).
func (s *asyncSender[T]) enqueue(v T) bool {
	if s.stopped() {
		return false
	}
	select {
	case s.ch <- v:
		s.full.Store(false)
		return true
	default:
		s.dropped.Add(1)
		if !s.full.Swap(true) {
			slog.Warn(s.what+" dropped: queue full", "size", cap(s.ch))
		}
		return false
	}
}

func (s *asyncSender[T]) run() {
	var stuck chan struct{} // closed when the detached call returns
	for {
		var v T
		select {
		case <-s.quit:
			return
		case <-s.ctxDone:
			return
		case v = <-s.ch:
		}
		if stuck != nil {
			select {
			case <-stuck:
				stuck = nil
				slog.Info(s.what + ": backend responds again")
			default:
				s.dropped.Add(1)
				continue
			}
		}
		if s.stopped() {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.send(v)
		}()
		t := time.NewTimer(s.timeout)
		select {
		case <-done:
		case <-t.C:
			stuck = done
			slog.Warn(s.what+": backend call timed out; dropping new items until it returns", "timeout", s.timeout)
		case <-s.quit:
		case <-s.ctxDone:
		}
		t.Stop()
	}
}
