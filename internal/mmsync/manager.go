package mmsync

import (
	"context"
	"sync"

	"github.com/spk/spk-mattermost/internal/store"
)

type Manager struct {
	cfg  Config
	root context.Context
	mu   sync.Mutex
	ws   map[int64]*entry
}

type entry struct {
	w      *Worker
	cancel context.CancelFunc
	done   chan struct{}
}

func NewManager(root context.Context, cfg Config) *Manager {
	cfg.defaults()
	return &Manager{cfg: cfg, root: root, ws: map[int64]*entry{}}
}

func (m *Manager) StartAll(ctx context.Context) error {
	list, err := m.cfg.Store.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, srv := range list {
		if srv.SignedIn() {
			m.Start(srv)
		}
	}
	return nil
}

// Start (re)starts the worker of srv — after sign-in, with the new token.
func (m *Manager) Start(srv store.Server) {
	m.Stop(srv.ID)
	w := NewWorker(m.cfg, srv)
	ctx, cancel := context.WithCancel(m.root)
	e := &entry{w: w, cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.ws[srv.ID] = e
	m.mu.Unlock()
	go func() {
		defer close(e.done)
		w.Run(ctx)
	}()
}

// Stop cancels the worker and waits for its final snapshot flush.
func (m *Manager) Stop(id int64) {
	m.mu.Lock()
	e := m.ws[id]
	delete(m.ws, id)
	m.mu.Unlock()
	if e != nil {
		e.cancel()
		<-e.done
	}
}

func (m *Manager) Worker(id int64) *Worker {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.ws[id]; e != nil {
		return e.w
	}
	return nil
}

// Each calls fn for every running worker; fn runs without the manager lock
// (it may call Start/Stop).
func (m *Manager) Each(fn func(id int64, w *Worker)) {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.ws))
	ws := make([]*Worker, 0, len(m.ws))
	for id, e := range m.ws {
		ids, ws = append(ids, id), append(ws, e.w)
	}
	m.mu.Unlock()
	for i, w := range ws {
		fn(ids[i], w)
	}
}

func (m *Manager) NudgeAll() { m.Each(func(_ int64, w *Worker) { w.Nudge() }) }

func (m *Manager) Close() {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.ws))
	for id := range m.ws {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Stop(id)
	}
}
