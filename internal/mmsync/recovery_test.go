package mmsync

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mm/ws"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/store"
)

// An old request can remain blackholed after connectivity returns. Restoring
// the network lets NEW requests through; it does not finish the old one.
func TestNetworkRecoveryInterruptsConnectionSetup(t *testing.T) {
	for _, path := range []string{"/api/v4/users/me", "/api/v4/websocket", "/api/v4/users/me/channels"} {
		t.Run(path, func(t *testing.T) {
			var armed atomic.Bool
			blocked, cancelled := make(chan struct{}), make(chan struct{})
			h := newHarness(t, mmfake.Options{RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != path || !armed.CompareAndSwap(true, false) {
					return false
				}
				close(blocked)
				<-r.Context().Done()
				close(cancelled)
				return true
			}})
			h.tune = func(c *Config) { c.HTTPClient = &http.Client{} }
			h.start()
			h.live()
			h.eventually(h.allLoaded, "prefetch")
			armed.Store(true)
			h.fake.DropConnections(true)
			select {
			case <-blocked:
			case <-time.After(3 * time.Second):
				t.Fatal("reconnect never reached the blocked request")
			}
			h.fake.PostAs("c-town", "bob", "during network outage")
			h.w.Nudge() // the same signal as Service.NetworkChanged / wake
			require.Eventually(t, func() bool {
				select {
				case <-cancelled:
					return true
				default:
					return false
				}
			}, time.Second, 10*time.Millisecond, "network recovery must cancel the old request immediately")
			h.live()
			h.eventually(func() bool { return h.hasMessage("c-town", "during network outage") }, "missed post not recovered")
		})
	}
}

func TestColdStartSessionCheckTimesOutAndRetries(t *testing.T) {
	var armed atomic.Bool
	armed.Store(true)
	h := newHarness(t, mmfake.Options{RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v4/users/me" || !armed.CompareAndSwap(true, false) {
			return false
		}
		<-r.Context().Done()
		return true
	}})
	h.tune = func(c *Config) {
		c.HTTPClient = &http.Client{} // recovery must not rely on client.Timeout
		c.CallTimeout = 100 * time.Millisecond
	}
	h.start()
	require.Eventually(t, func() bool { return h.w.Status() == StatusLive }, 3*time.Second, 10*time.Millisecond,
		"a hung session check must expire and retry without a network notification")
}

func TestBootstrapTimesOutAndRecoversWithoutNetworkSignal(t *testing.T) {
	var armed atomic.Bool
	blocked := make(chan struct{})
	h := newHarness(t, mmfake.Options{RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v4/users/me/channels" || !armed.CompareAndSwap(true, false) {
			return false
		}
		close(blocked)
		<-r.Context().Done()
		return true
	}})
	h.tune = func(c *Config) {
		c.HTTPClient = &http.Client{}
		c.bootstrapTimeout = 250 * time.Millisecond
	}
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	armed.Store(true)
	h.fake.DropConnections(true)
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("resync never reached the blocked request")
	}
	h.fake.PostAs("c-town", "bob", "missed during stalled bootstrap")
	require.Eventually(t, func() bool {
		return h.w.Status() == StatusLive && h.hasMessage("c-town", "missed during stalled bootstrap")
	}, 3*time.Second, 10*time.Millisecond, "bootstrap must expire and recover without a network notification")
}

type syncBlackholeConn struct {
	client     net.Conn
	backend    net.Conn
	blackholed atomic.Bool
}

type syncBlackholeProxy struct {
	ln          net.Listener
	backendAddr string
	conns       atomic.Int32

	mu     sync.Mutex
	closed bool
	active []*syncBlackholeConn
	wg     sync.WaitGroup
}

func startSyncBlackholeProxy(t *testing.T, backendAddr string) *syncBlackholeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &syncBlackholeProxy{ln: ln, backendAddr: backendAddr}
	p.wg.Add(1)
	go p.acceptLoop()
	t.Cleanup(p.Close)
	return p
}

func (p *syncBlackholeProxy) Addr() string { return p.ln.Addr().String() }
func (p *syncBlackholeProxy) Conns() int32 { return p.conns.Load() }

func (p *syncBlackholeProxy) BlackholeExisting() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.active {
		c.blackholed.Store(true)
	}
}

func (p *syncBlackholeProxy) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	active := append([]*syncBlackholeConn(nil), p.active...)
	p.mu.Unlock()

	_ = p.ln.Close()
	for _, c := range active {
		_ = c.client.Close()
		_ = c.backend.Close()
	}
	p.wg.Wait()
}

func (p *syncBlackholeProxy) acceptLoop() {
	defer p.wg.Done()
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.conns.Add(1)
		backend, err := net.DialTimeout("tcp", p.backendAddr, 2*time.Second)
		if err != nil {
			_ = client.Close()
			continue
		}
		bc := &syncBlackholeConn{client: client, backend: backend}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = client.Close()
			_ = backend.Close()
			return
		}
		p.active = append(p.active, bc)
		p.wg.Add(2)
		p.mu.Unlock()

		go p.pipe(bc, client, backend, true)
		go p.pipe(bc, backend, client, false)
	}
}

func (p *syncBlackholeProxy) pipe(bc *syncBlackholeConn, src, dst net.Conn, fromClient bool) {
	defer p.wg.Done()
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 && !bc.blackholed.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				if !bc.blackholed.Load() {
					_ = bc.client.Close()
					_ = bc.backend.Close()
					return
				}
			}
		}
		if err != nil {
			if fromClient || !bc.blackholed.Load() {
				_ = bc.client.Close()
				_ = bc.backend.Close()
			}
			return
		}
	}
}

// TestWorkerRecoversFromHTTP2Blackhole verifies end-to-end that a Worker
// talking over TLS+HTTP/2 (ALPN h2) does not stay stuck in StatusReconnecting
// after a silent TCP blackhole: it evicts the dead HTTP/2 connection, opens a
// new TCP connection, returns to StatusLive and catches up missed messages.
func TestWorkerRecoversFromHTTP2Blackhole(t *testing.T) {
	fake := mmfake.Start(mmfake.Options{})
	t.Cleanup(fake.Close)

	fakeURL, err := url.Parse(fake.URL())
	require.NoError(t, err)
	rp := httputil.NewSingleHostReverseProxy(fakeURL)

	var h2MeHits atomic.Int32
	tlsSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/users/me" && r.TLS != nil && r.TLS.NegotiatedProtocol == "h2" && r.Proto == "HTTP/2.0" {
			h2MeHits.Add(1)
		}
		rp.ServeHTTP(w, r)
	}))
	tlsSrv.EnableHTTP2 = true
	tlsSrv.StartTLS()
	t.Cleanup(tlsSrv.Close)

	proxy := startSyncBlackholeProxy(t, tlsSrv.Listener.Addr().String())
	baseURL := "https://" + proxy.Addr()

	hc := rest.NewHTTPClient(5 * time.Second)
	tr := hc.Transport.(*http.Transport)
	tr.TLSClientConfig = tlsSrv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tr.HTTP2.SendPingTimeout = 100 * time.Millisecond
	tr.HTTP2.PingTimeout = 150 * time.Millisecond
	tr.HTTP2.WriteByteTimeout = 200 * time.Millisecond
	t.Cleanup(hc.CloseIdleConnections)

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	srv, err := st.AddServer(context.Background(), store.Server{URL: baseURL, Name: "TLSFake"})
	require.NoError(t, err)
	tok, u, err := rest.New(baseURL, "", hc).Login(context.Background(), "alice", "secret")
	require.NoError(t, err)
	require.NoError(t, st.SetSession(context.Background(), srv.ID, tok, u.ID, u.Username))
	srv, err = st.GetServer(context.Background(), srv.ID)
	require.NoError(t, err)

	w := NewWorker(Config{
		Store:        st,
		HTTPClient:   hc,
		FlushEvery:   50 * time.Millisecond,
		MinBackoff:   20 * time.Millisecond,
		MaxBackoff:   100 * time.Millisecond,
		WakeCheck:    time.Hour,
		WS:           func(o *ws.Options) { o.PingInterval, o.PingTimeout = 100*time.Millisecond, 150*time.Millisecond },
		revalIdle:    20 * time.Millisecond,
		refreshRetry: 50 * time.Millisecond,
		metaDebounce: 20 * time.Millisecond,
		resumeSettle: 100 * time.Millisecond,
		limiter:      rate.NewLimiter(rate.Inf, 0),
	}, srv)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	require.Eventually(t, func() bool {
		return w.Status() == StatusLive && len(w.State().SyncItems()) == 0
	}, 5*time.Second, 20*time.Millisecond, "worker never went live over TLS+H2")
	require.GreaterOrEqual(t, h2MeHits.Load(), int32(1), "Me must negotiate ALPN h2")

	connsBefore := proxy.Conns()
	h2Before := h2MeHits.Load()

	// Silently blackhole every open TCP connection (both pooled H2 and live WS).
	proxy.BlackholeExisting()
	fake.PostAs("c-town", "bob", "after h2 blackhole")

	require.Eventually(t, func() bool {
		if w.Status() != StatusLive || h2MeHits.Load() <= h2Before {
			return false
		}
		v, _ := w.State().ChannelView("c-town")
		for _, p := range v.Posts {
			if p.Message == "after h2 blackhole" && !p.Pending {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "worker stalled in reconnecting instead of recovering from H2 blackhole")
	assert.GreaterOrEqual(t, proxy.Conns(), connsBefore+1, "recovery must open fresh TCP connections")
}

// TestPreUpgradeWSDialResumeFailuresResetAndResync verifies that repeated
// pre-upgrade ws.Dial failures with a non-empty resume token (after Me
// succeeds) increment resumeFails and drop the stale resume after
// maxResumeFails, allowing a fresh dial and full resync.
func TestPreUpgradeWSDialResumeFailuresResetAndResync(t *testing.T) {
	var armed atomic.Bool
	var rejected atomic.Int32
	var freshDials atomic.Int32

	h := newHarness(t, mmfake.Options{RequestHook: func(w http.ResponseWriter, r *http.Request) bool {
		if !armed.Load() || r.URL.Path != "/api/v4/websocket" {
			return false
		}
		if r.URL.Query().Get("connection_id") != "" {
			rejected.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		freshDials.Add(1)
		return false
	}})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")

	armed.Store(true)
	h.fake.PostAs("c-offtopic", "bob", "after pre-upgrade resume reset")
	h.fake.DropConnections(false)

	h.eventually(func() bool {
		return h.w.Status() == StatusLive && h.hasMessage("c-offtopic", "after pre-upgrade resume reset")
	}, "worker did not reset stale resume after repeated pre-upgrade ws.Dial failures")
	assert.Equal(t, int32(maxResumeFails), rejected.Load(), "must give up on stale resume after maxResumeFails")
	assert.GreaterOrEqual(t, freshDials.Load(), int32(1), "must dial fresh without connection_id after reset")
}

// TestRefreshUsersLifecycle verifies that background refreshUsers:
//  1. Outlives bootstrap's own local timeout context (bound to session sctx),
//  2. Is cancelled immediately when the session ends / is nudged and releases
//     usersSem before the next session starts, on both the primary bootstrap
//     path in runSession and the reset-hello path in handle.
func TestRefreshUsersLifecycle(t *testing.T) {
	t.Run("outlives_bootstrap_timeout", func(t *testing.T) {
		var intercept atomic.Bool
		var canceledEarly atomic.Bool
		refreshStarted := make(chan struct{})
		releaseRefresh := make(chan struct{})
		var startOnce sync.Once

		h := newHarness(t, mmfake.Options{RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
			if !intercept.Load() || r.URL.Path != "/api/v4/users/ids" || r.URL.Query().Get("since") == "" {
				return false
			}
			startOnce.Do(func() { close(refreshStarted) })
			select {
			case <-releaseRefresh:
			case <-r.Context().Done():
				canceledEarly.Store(true)
			}
			return false
		}})
		h.start()
		h.live()
		h.eventually(func() bool { return dmItem(h, "c-dm-bob").Avatar != "" }, "bob profile")
		h.eventually(func() bool { return h.liveAt() > 0 }, "liveAt persisted")
		h.stop()

		at := h.fake.SetPicture("bob")
		intercept.Store(true)
		h.tune = func(c *Config) { c.bootstrapTimeout = 150 * time.Millisecond }
		h.start()
		h.live() // bootstrap has returned and cancelled its local timeout ctx

		select {
		case <-refreshStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("refreshUsers never started after bootstrap")
		}
		// Wait past bootstrapTimeout to prove refreshUsers uses sctx, not bootstrap's ctx.
		time.Sleep(220 * time.Millisecond)
		require.False(t, canceledEarly.Load(), "refreshUsers must not be cancelled by bootstrap's local timeout context")

		close(releaseRefresh)
		h.eventually(func() bool {
			return dmItem(h, "c-dm-bob").Avatar == strconv.FormatInt(at, 10)
		}, "refreshUsers did not finish updating bob's avatar after bootstrap returned")
	})

	for _, tc := range []struct {
		name       string
		resetHello bool
	}{
		{name: "cancels_on_nudge_and_frees_usersSem_primary_bootstrap", resetHello: false},
		{name: "cancels_on_nudge_and_frees_usersSem_reset_hello", resetHello: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var blockNext atomic.Bool
			var checkSemOnMe atomic.Bool
			var semWasFreeOnNextMe atomic.Bool
			var meChecked atomic.Bool

			refreshBlocked := make(chan struct{})
			refreshCanceled := make(chan struct{})
			testDone := make(chan struct{})
			t.Cleanup(func() { close(testDone) })
			var blockOnce sync.Once
			var h *harness

			h = newHarness(t, mmfake.Options{RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
				if checkSemOnMe.Load() && r.URL.Path == "/api/v4/users/me" {
					if meChecked.CompareAndSwap(false, true) {
						// runSession's defer refreshes.Wait() must have cancelled and
						// joined the blocked refreshUsers before the next session begins,
						// so usersSem is available (not held by a leaked refreshUsers).
						lctx, lcancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
						acquired := h.w.lockUsers(lctx)
						lcancel()
						if acquired {
							h.w.unlockUsers()
						}
						semWasFreeOnNextMe.Store(acquired)
					}
					return false
				}
				if r.URL.Path == "/api/v4/users/ids" && r.URL.Query().Get("since") != "" && blockNext.CompareAndSwap(true, false) {
					// Drain the POST body so net/http's HTTP/1.1 server enters
					// backgroundRead and observes client disconnect via r.Context().
					_, _ = io.Copy(io.Discard, r.Body)
					blockOnce.Do(func() { close(refreshBlocked) })
					select {
					case <-r.Context().Done():
						close(refreshCanceled)
					case <-testDone:
					}
					return true
				}
				return false
			}})
			h.start()
			h.live()
			h.eventually(h.allLoaded, "prefetch")
			h.eventually(func() bool { return dmItem(h, "c-dm-bob").Avatar != "" }, "bob profile")
			h.eventually(func() bool { return h.liveAt() > 0 }, "liveAt persisted")

			if tc.resetHello {
				// DropConnections(true) causes the resumed WS to receive a
				// reset hello, triggering handle -> bootstrap(sctx, true, refreshes).
				blockNext.Store(true)
				h.fake.DropConnections(true)
			} else {
				// Restart from snapshot so runSession calls primary bootstrap directly.
				h.stop()
				blockNext.Store(true)
				h.start()
			}

			select {
			case <-refreshBlocked:
			case <-time.After(3 * time.Second):
				t.Fatal("refreshUsers never reached blocked request")
			}
			// Sanity: refreshUsers is currently holding usersSem.
			require.Equal(t, 1, len(h.w.usersSem), "blocked refreshUsers should hold usersSem")

			checkSemOnMe.Store(true)
			h.w.Nudge()

			select {
			case <-refreshCanceled:
			case <-time.After(2 * time.Second):
				t.Fatal("nudging the session must cancel in-flight refreshUsers immediately")
			}

			h.eventually(func() bool { return meChecked.Load() && h.w.Status() == StatusLive }, "next session never reached live")
			assert.True(t, semWasFreeOnNextMe.Load(), "usersSem must be released before the next session begins")
		})
	}
}
