package desktop

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
// order. enqueue never blocks: send may hang (a D-Bus call without a
// timeout), and once the bounded queue is full new items are dropped rather
// than stalling the caller. The goroutine lives as long as the process.
type asyncSender[T any] struct{ ch chan T }

func newAsyncSender[T any](size int, send func(T)) *asyncSender[T] {
	s := &asyncSender[T]{ch: make(chan T, size)}
	go func() {
		for v := range s.ch {
			send(v)
		}
	}()
	return s
}

// enqueue reports whether v was queued (false: dropped, queue full).
func (s *asyncSender[T]) enqueue(v T) bool {
	select {
	case s.ch <- v:
		return true
	default:
		return false
	}
}
