package mmsync

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
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
