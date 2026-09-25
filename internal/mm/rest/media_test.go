package rest

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestStreamReturnsBodyAndHeadersAndSendsAuth(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/users/u1/image", r.URL.Path)
		assert.Equal(t, "5", r.URL.Query().Get("_"))
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		assert.Equal(t, "bytes=0-9", r.Header.Get("Range"))
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNGDATA"))
	})
	resp, err := c.Stream(context.Background(), "/api/v4/users/u1/image?_=5", http.Header{"Range": {"bytes=0-9"}})
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "PNGDATA", string(b))
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
}

func TestStreamErrorStatusIsClassified(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"id":"app.file_info.get.app_error","message":"nope"}`))
	})
	_, err := c.Stream(context.Background(), "/api/v4/files/f1", nil)
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, http.StatusNotFound, e.Status)
	assert.Equal(t, KindAPI, e.Kind)
	assert.Equal(t, "app.file_info.get.app_error", e.ID)
}

func TestStreamWaitsOut429AndTakesALimiterTokenPerAttempt(t *testing.T) {
	var n atomic.Int32
	c, slept := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	lim := rate.NewLimiter(rate.Every(time.Hour), 2)
	resp, err := c.WithLimiter(lim).Stream(context.Background(), "/x", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, int32(2), n.Load())
	assert.Equal(t, []time.Duration{2 * time.Second}, *slept)
	assert.InDelta(t, 0, lim.Tokens(), 0.01, "each attempt took a token")
}

func TestWithHTTPClientKeepsTokenAndLimiter(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("x"))
	})
	lim := rate.NewLimiter(rate.Every(time.Hour), 1)
	resp, err := c.WithLimiter(lim).WithHTTPClient(&http.Client{}).Stream(context.Background(), "/y", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.InDelta(t, 0, lim.Tokens(), 0.01)
}
