package rest

import (
	"context"
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

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "tok", srv.Client())
	var slept []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	return c, &slept
}

func TestRequestCarriesBearerAndXRequestedWith(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		assert.Equal(t, "XMLHttpRequest", r.Header.Get("X-Requested-With"))
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	})
	require.NoError(t, c.Ping(context.Background()))
}

func TestRetries429HonouringRetryAfter(t *testing.T) {
	var n atomic.Int32
	c, slept := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	})
	require.NoError(t, c.Ping(context.Background()))
	assert.Equal(t, int32(2), n.Load())
	assert.Equal(t, []time.Duration{3 * time.Second}, *slept)
}

func TestGives429UpAfterMaxAttempts(t *testing.T) {
	var n atomic.Int32
	c, slept := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	err := c.Ping(context.Background())
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, http.StatusTooManyRequests, e.Status)
	assert.Equal(t, int32(maxAttempts), n.Load())
	assert.Equal(t, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}, *slept)
}

func TestClassifies401AsAuthWithServerErrorID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"id":"api.context.session_expired.app_error","message":"Invalid or expired session","status_code":401}`))
	})
	_, err := c.Me(context.Background())
	assert.True(t, IsAuth(err))
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "api.context.session_expired.app_error", e.ID)
}

func TestClassifies502AsNetwork(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	err := c.Ping(context.Background())
	assert.True(t, IsNetwork(err))
}

func TestTransportErrorIsNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := New(url, "", &http.Client{Timeout: time.Second})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	assert.True(t, IsNetwork(c.Ping(context.Background())))
}

func TestPostIsNotRetriedOnTransportError(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close() // transport error for the client
	})
	_, _, err := c.Login(context.Background(), "a", "b")
	assert.True(t, IsNetwork(err))
	assert.Equal(t, int32(1), n.Load(), "non-idempotent POST must not be replayed")
}

func TestContextCancelStopsRetries(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	err := c.Ping(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}

// stallingServer accepts TCP connections and never answers — the shape of a
// hung server behind a load balancer.
func stallingServer(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var accepted atomic.Int32
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return "http://" + ln.Addr().String(), &accepted
}

func TestTimeoutIsNotRetried(t *testing.T) {
	url, accepted := stallingServer(t)
	c := New(url, "", &http.Client{Timeout: 100 * time.Millisecond})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	err := c.Ping(context.Background())
	assert.True(t, IsNetwork(err))
	assert.Equal(t, int32(1), accepted.Load(), "a timed-out GET must not be replayed")
}
