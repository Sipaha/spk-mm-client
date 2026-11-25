package rest

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blackholeConn struct {
	client     net.Conn
	backend    net.Conn
	blackholed atomic.Bool
}

// blackholeTCPProxy forwards TCP connections to backendAddr until
// BlackholeExisting is called, at which point all currently open connections
// silently swallow client bytes and send nothing back (no FIN/RST), while any
// new TCP connection continues forwarding normally.
type blackholeTCPProxy struct {
	ln          net.Listener
	backendAddr string

	conns atomic.Int32

	mu     sync.Mutex
	closed bool
	active []*blackholeConn
	wg     sync.WaitGroup
}

func startBlackholeTCPProxy(t *testing.T, backendAddr string) *blackholeTCPProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &blackholeTCPProxy{ln: ln, backendAddr: backendAddr}
	p.wg.Add(1)
	go p.acceptLoop()
	t.Cleanup(p.Close)
	return p
}

func (p *blackholeTCPProxy) Addr() string { return p.ln.Addr().String() }
func (p *blackholeTCPProxy) Conns() int32 { return p.conns.Load() }

func (p *blackholeTCPProxy) BlackholeExisting() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.active {
		c.blackholed.Store(true)
	}
}

func (p *blackholeTCPProxy) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	active := append([]*blackholeConn(nil), p.active...)
	p.mu.Unlock()

	_ = p.ln.Close()
	for _, c := range active {
		_ = c.client.Close()
		_ = c.backend.Close()
	}
	p.wg.Wait()
}

func (p *blackholeTCPProxy) acceptLoop() {
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
		bc := &blackholeConn{client: client, backend: backend}
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

func (p *blackholeTCPProxy) pipe(bc *blackholeConn, src, dst net.Conn, fromClient bool) {
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
			// When blackholed, backend EOF must never propagate FIN/RST to client.
			if fromClient || !bc.blackholed.Load() {
				_ = bc.client.Close()
				_ = bc.backend.Close()
			}
			return
		}
	}
}

func TestNewHTTPTransportConfiguresHTTP2HealthChecks(t *testing.T) {
	tr := NewHTTPTransport()
	require.NotNil(t, tr)
	assert.True(t, tr.ForceAttemptHTTP2)
	require.NotNil(t, tr.HTTP2)
	assert.Equal(t, 10*time.Second, tr.HTTP2.SendPingTimeout)
	assert.Equal(t, 10*time.Second, tr.HTTP2.PingTimeout)
	assert.Equal(t, 15*time.Second, tr.HTTP2.WriteByteTimeout)

	// Must return an independent clone of http.DefaultTransport.
	tr.HTTP2.SendPingTimeout = time.Second
	tr2 := NewHTTPTransport()
	assert.Equal(t, 10*time.Second, tr2.HTTP2.SendPingTimeout)

	hc := NewHTTPClient(30 * time.Second)
	assert.Equal(t, 30*time.Second, hc.Timeout)
	hcTr, ok := hc.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, hcTr.HTTP2)
	assert.True(t, hcTr.ForceAttemptHTTP2)
}

// TestHTTP2BlackholeEvictsDeadConnAndRecovers proves that over real TLS+HTTP/2
// (ALPN h2), a silently blackholed TCP connection (no FIN/RST) is detected via
// HTTP/2 PING health-checks configured by NewHTTPClient, evicted, and replaced
// with a fresh TCP connection (conns >= 2).
func TestHTTP2BlackholeEvictsDeadConnAndRecovers(t *testing.T) {
	var h2Hits atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && r.TLS.NegotiatedProtocol == "h2" && r.Proto == "HTTP/2.0" {
			h2Hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"OK"}`)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	tlsCfg := srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()

	t.Run("default_http2_without_health_check_stalls", func(t *testing.T) {
		proxy := startBlackholeTCPProxy(t, srv.Listener.Addr().String())
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.ForceAttemptHTTP2 = true
		tr.TLSClientConfig = tlsCfg.Clone()
		hc := &http.Client{Transport: tr}
		t.Cleanup(hc.CloseIdleConnections)

		rc := New("https://"+proxy.Addr(), "tok", hc)
		require.NoError(t, rc.Ping(context.Background()))
		require.Equal(t, int32(1), proxy.Conns())

		proxy.BlackholeExisting()

		ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
		defer cancel()
		err := rc.Ping(ctx)
		require.Error(t, err, "without HTTP2Config PING timeouts, blackholed H2 conn stalls")
		assert.Equal(t, int32(1), proxy.Conns(), "dead H2 connection was never evicted")
	})

	t.Run("NewHTTPClient_evicts_blackholed_h2_and_opens_new_conn", func(t *testing.T) {
		h2Hits.Store(0)
		proxy := startBlackholeTCPProxy(t, srv.Listener.Addr().String())

		hc := NewHTTPClient(5 * time.Second)
		tr := hc.Transport.(*http.Transport)
		tr.TLSClientConfig = tlsCfg.Clone()
		// Shorten health-check timers so the test finishes in < 2s while
		// exercising the exact NewHTTPTransport HTTP/2 PING eviction path.
		tr.HTTP2.SendPingTimeout = 100 * time.Millisecond
		tr.HTTP2.PingTimeout = 150 * time.Millisecond
		tr.HTTP2.WriteByteTimeout = 200 * time.Millisecond
		t.Cleanup(hc.CloseIdleConnections)

		rc := New("https://"+proxy.Addr(), "tok", hc)
		require.NoError(t, rc.Ping(context.Background()))
		require.Equal(t, int32(1), h2Hits.Load(), "initial request must negotiate ALPN h2")
		require.Equal(t, int32(1), proxy.Conns())

		proxy.BlackholeExisting()

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, rc.Ping(ctx), "client must detect blackholed H2 conn, close it and recover")
		assert.GreaterOrEqual(t, proxy.Conns(), int32(2), "recovery must open a new TCP connection")
		assert.GreaterOrEqual(t, h2Hits.Load(), int32(2), "recovered request must also use ALPN h2")
	})
}
