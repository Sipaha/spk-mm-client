package desktop

import (
	"context"
	"sync"
)

// pending is one asynchronous clipboard request: GTK answers on its main
// thread (answer), the asking goroutine waits with a deadline (wait). An
// answer that comes after the asker gave up — a slow or hung clipboard
// owner — is released with free (C memory, a pixbuf reference), never
// leaked; answer never blocks the main thread.
type pending[T any] struct {
	mu   sync.Mutex
	gone bool // the asker gave up
	ch   chan T
	free func(T)
}

func newPending[T any](free func(T)) *pending[T] {
	return &pending[T]{ch: make(chan T, 1), free: free}
}

// answer delivers the result; called once.
func (p *pending[T]) answer(v T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gone {
		if p.free != nil {
			p.free(v)
		}
		return
	}
	p.ch <- v // buffered, answered once: never blocks
}

// wait returns the answer, or ctx's error once it ends first.
func (p *pending[T]) wait(ctx context.Context) (T, error) {
	select {
	case v := <-p.ch:
		return v, nil
	case <-ctx.Done():
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gone = true
	select {
	case v := <-p.ch: // it came in the meantime
		return v, nil
	default:
		var zero T
		return zero, ctx.Err()
	}
}
