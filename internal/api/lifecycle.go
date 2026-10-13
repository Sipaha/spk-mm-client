package api

import (
	"context"
	"sync"
)

// lifetime owns calls and transfers that must end before the store is closed.
// Registration and shutdown are serialized: Wait never races a new Add.
type lifetime struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	closing bool
	wg      sync.WaitGroup
}

func newLifetime() *lifetime {
	ctx, cancel := context.WithCancel(context.Background())
	return &lifetime{ctx: ctx, cancel: cancel}
}

func (l *lifetime) start(parent context.Context) (context.Context, func(), error) {
	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		return nil, nil, coded(CodeCancelled, nil)
	}
	l.wg.Add(1)
	l.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(l.ctx, cancel)
	var once sync.Once
	return ctx, func() {
		once.Do(func() { stop(); cancel(); l.wg.Done() })
	}, nil
}

func (l *lifetime) close() {
	l.mu.Lock()
	l.closing = true
	l.cancel()
	l.mu.Unlock()
	l.wg.Wait()
}

// sessionChange orders the local commit of login/logout for one server.
// Network authentication runs outside mu and belongs to a generation.
type sessionChange struct {
	mu     sync.Mutex
	gen    uint64
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *Service) sessionChange(id int64) *sessionChange {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	g := s.sessions[id]
	if g == nil {
		g = &sessionChange{}
		s.sessions[id] = g
	}
	return g
}

// advance is called under mu. It invalidates even a callback that already
// consumed auth.SSO's pending entry and is currently validating its token.
func (g *sessionChange) advance(parent context.Context) (context.Context, uint64) {
	if g.cancel != nil {
		g.cancel()
	}
	g.gen++
	g.ctx, g.cancel = context.WithCancel(parent)
	return g.ctx, g.gen
}

func (g *sessionChange) current(gen uint64) bool {
	return g.gen == gen && g.ctx != nil && g.ctx.Err() == nil
}
