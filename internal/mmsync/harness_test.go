package mmsync

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mm/ws"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
	"github.com/spk/spk-mm-client/internal/store"
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
	clock      *clock // nil → the real clock
	tune       func(*Config)

	mu       sync.Mutex
	statuses []Status
	notes    []state.NotifyCandidate
	changes  []state.Change // every non-empty Hooks.Changed call, in order
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
	var now func() time.Time
	if h.clock != nil {
		now = h.clock.now
	}
	return Config{
		Store: h.store,
		Now:   now,
		Hooks: Hooks{
			Changed: func(_ int64, c state.Change) {
				h.mu.Lock()
				h.changes = append(h.changes, c)
				h.mu.Unlock()
			},
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
		// test seams: production defaults (revalRetryIn 2s, refreshRetryFirst
		// 5s, metaDebounce 300ms, resumeSettle 3s) would make every
		// reread/refresh/meta-debounce/resume-settle wait a real-time wait —
		// harmless in prod, ruinous under -race across ~170 tests. Short by
		// default here; individual tests still override via h.tune when they
		// need a specific value (e.g. TestRefreshDeadlineDuringCategoriesKeepsThemAndRetries,
		// TestSettleDoesNotProveWhileRefreshHoldsTheHello).
		revalIdle:    20 * time.Millisecond,
		refreshRetry: 50 * time.Millisecond,
		metaDebounce: 20 * time.Millisecond,
		resumeSettle: 100 * time.Millisecond,
		// test seam: the production per-server budget (10 req/s, burst 20)
		// throttles a fully-loaded fake to a crawl — a bootstrap or
		// catch-up cycle alone issues ~20-30 requests, so every request
		// past the burst waits a genuine 100ms for its token. That's the
		// single largest contributor to package wall time (most of it is
		// idle wait, not CPU — confirmed via `go test` without -race still
		// taking ~125s). Effectively unlimited by default; a test that
		// specifically exercises the limiter can still tune it.
		limiter: rate.NewLimiter(rate.Inf, 0),
	}
}

func (h *harness) start() {
	h.t.Helper()
	cfg := h.config()
	if h.tune != nil {
		h.tune(&cfg)
	}
	h.w = NewWorker(cfg, h.srv)
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

// clock is the real clock shifted by an offset the test can move forward
// (the worker's view of time; the fake server keeps the real one).
type clock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.off += d
	c.mu.Unlock()
}

// useClock gives the worker a clock the test can jump forward.
func (h *harness) useClock() *clock {
	h.clock = &clock{}
	return h.clock
}

// liveAt is the persisted "stream proven continuous until" mark.
func (h *harness) liveAt() int64 {
	h.t.Helper()
	entries, err := h.store.LoadCache(context.Background(), h.srv.ID)
	require.NoError(h.t, err)
	for _, e := range entries {
		if e.Kind == "live" && e.Key == "at" {
			n, err := strconv.ParseInt(string(e.Data), 10, 64)
			require.NoError(h.t, err)
			return n
		}
	}
	return 0
}

func (h *harness) mentions(ch string) int {
	for _, c := range h.w.State().Sidebar("").Categories {
		for _, it := range c.Channels {
			if it.ID == ch {
				return it.Mentions
			}
		}
	}
	return -1
}

func (h *harness) notified(msg string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, n := range h.notes {
		if n.Post.Message == msg {
			return true
		}
	}
	return false
}

// resetChanges clears the recorded Hooks.Changed calls, so a later check
// (e.g. threadChanged) only sees what happens from here on.
func (h *harness) resetChanges() {
	h.mu.Lock()
	h.changes = nil
	h.mu.Unlock()
}

// threadChanged reports whether any recorded Hooks.Changed call so far
// carried root in its Change.Threads.
func (h *harness) threadChanged(root string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.changes {
		if slices.Contains(c.Threads, root) {
			return true
		}
	}
	return false
}

func (h *harness) categoryIDs() []string {
	var out []string
	for _, c := range h.w.State().Sidebar("").Categories {
		out = append(out, c.ID)
	}
	return out
}

func (h *harness) inSidebar(ch string) bool {
	for _, c := range h.w.State().Sidebar("").Categories {
		for _, it := range c.Channels {
			if it.ID == ch {
				return true
			}
		}
	}
	return false
}
