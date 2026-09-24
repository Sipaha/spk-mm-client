package mmsync

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/mm/ws"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/state"
	"github.com/spk/spk-mattermost/internal/store"
)

type harness struct {
	t      *testing.T
	fake   *mmfake.Server
	store  *store.Store
	srv    store.Server
	w      *Worker
	cancel context.CancelFunc
	done   chan struct{}
	// sinceLimit mirrors the fake's since= cap (0 → the real server's).
	sinceLimit int

	mu       sync.Mutex
	statuses []Status
	notes    []state.NotifyCandidate
}

func newHarness(t *testing.T, o mmfake.Options) *harness {
	t.Helper()
	fake := mmfake.Start(o)
	t.Cleanup(fake.Close)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	srv, err := st.AddServer(context.Background(), store.Server{URL: fake.URL(), Name: "Fake"})
	require.NoError(t, err)
	tok, u, err := rest.New(fake.URL(), "", nil).Login(context.Background(), "alice", "secret")
	require.NoError(t, err)
	require.NoError(t, st.SetSession(context.Background(), srv.ID, tok, u.ID, u.Username))
	srv, _ = st.GetServer(context.Background(), srv.ID)
	return &harness{t: t, fake: fake, store: st, srv: srv, sinceLimit: o.SinceLimit}
}

func (h *harness) config() Config {
	return Config{
		Store: h.store,
		Hooks: Hooks{
			Changed: func(int64, state.Change) {},
			Status: func(_ int64, s Status) {
				h.mu.Lock()
				h.statuses = append(h.statuses, s)
				h.mu.Unlock()
			},
			Notify: func(_ int64, c state.NotifyCandidate) {
				h.mu.Lock()
				h.notes = append(h.notes, c)
				h.mu.Unlock()
			},
		},
		FlushEvery: 50 * time.Millisecond, MinBackoff: 20 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
		WakeCheck:  time.Hour,
		WS:         func(o *ws.Options) { o.PingInterval, o.PingTimeout = 200*time.Millisecond, time.Second },
		sinceLimit: h.sinceLimit,
	}
}

func (h *harness) start() {
	h.t.Helper()
	h.w = NewWorker(h.config(), h.srv)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan struct{})
	go func() { h.w.Run(ctx); close(h.done) }()
	h.t.Cleanup(h.stop)
}

func (h *harness) stop() {
	if h.cancel != nil {
		h.cancel()
		<-h.done
		h.cancel = nil
	}
}

func (h *harness) eventually(cond func() bool, msg string) {
	h.t.Helper()
	require.Eventually(h.t, cond, 10*time.Second, 20*time.Millisecond, msg)
}

func (h *harness) live() {
	h.t.Helper()
	h.eventually(func() bool { return h.w.Status() == StatusLive }, "worker never went live")
}

func (h *harness) view(ch string) state.ChannelView {
	v, _ := h.w.State().ChannelView(ch)
	return v
}

func (h *harness) hasMessage(ch, msg string) bool {
	for _, p := range h.view(ch).Posts {
		if p.Message == msg && !p.Pending {
			return true
		}
	}
	return false
}

func (h *harness) allLoaded() bool { return len(h.w.State().SyncItems()) == 0 }
