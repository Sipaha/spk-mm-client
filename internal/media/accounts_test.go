package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type scopedOrigin struct {
	*testOrigin
	mu    sync.Mutex
	scope string
	ctx   context.Context
}

func (o *scopedOrigin) MediaAccount(int64) (string, context.Context, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.scope == "" {
		return "", nil, ErrNoServer
	}
	return o.scope, o.ctx, nil
}

func TestAccountSwitchCancelsInflightMedia(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer up.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := &scopedOrigin{testOrigin: &testOrigin{url: up.URL}, scope: "alice", ctx: ctx}
	c, err := New(Options{Dir: t.TempDir(), Origin: o})
	require.NoError(t, err)
	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		c.ServeHTTP(rec, httptest.NewRequest("GET", "/media/1/thumb/f1?account=alice", nil))
		done <- rec.Code
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("fetch not started")
	}
	o.mu.Lock()
	cancel()
	o.scope, o.ctx = "bob", context.Background()
	o.mu.Unlock()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("old account fetch not canceled")
	}
	select {
	case code := <-done:
		require.NotEqual(t, 200, code)
	case <-time.After(3 * time.Second):
		t.Fatal("old read not joined")
	}
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest("GET", "/media/1/thumb/f1?account=alice", nil))
	require.Equal(t, 404, rec.Code)
	require.Zero(t, c.Size())
}
