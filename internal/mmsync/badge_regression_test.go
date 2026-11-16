package mmsync

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

func TestColdStartCacheRestoreViewsAlreadyFocusedActiveChannelAfterBootstrap(t *testing.T) {
	var holdBootstrap atomic.Bool
	inBootstrap := make(chan struct{})
	releaseBootstrap := make(chan struct{})
	var inOnce, relOnce sync.Once
	defer relOnce.Do(func() { close(releaseBootstrap) })

	h := newHarness(t, mmfake.Options{RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v4/users/me/channels" || !holdBootstrap.Load() {
			return false
		}
		inOnce.Do(func() { close(inBootstrap) })
		select {
		case <-releaseBootstrap:
			return false
		case <-r.Context().Done():
			return true
		}
	}})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.SetFocused(true)
	h.w.OpenChannel("c-dm-bob")
	h.eventually(func() bool { return h.liveAt() > 0 }, "snapshot flushed")
	h.stop()

	// While offline, bob sends a message in the personal DM that was active before shutdown.
	h.fake.PostAs("c-dm-bob", "bob", "offline dm message")

	// Cold start: hold bootstrap so the UI opens and focuses the cached read DM first.
	holdBootstrap.Store(true)
	h.start()
	select {
	case <-inBootstrap:
	case <-time.After(3 * time.Second):
		t.Fatal("cold-start bootstrap did not reach /users/me/channels")
	}

	h.w.SetFocused(true)
	_, ok := h.w.OpenChannel("c-dm-bob")
	require.True(t, ok, "channel restored from snapshot")
	assert.Equal(t, state.Badge{}, h.w.State().Badge(), "snapshot itself still has pre-offline read counts")

	// Let bootstrap land with the new server unread counts for the already-open focused DM.
	holdBootstrap.Store(false)
	relOnce.Do(func() { close(releaseBootstrap) })
	h.live()

	h.eventually(func() bool {
		return h.w.State().Badge() == (state.Badge{}) &&
			h.fake.Member("c-dm-bob", "alice").MsgCount == h.fake.Channel("c-dm-bob").TotalMsgCount
	}, "bootstrap must mark the already-open focused channel read instead of leaving badge unread")
}

func TestReconnectViewsActiveFocusedChannelAfterOfflineFailure(t *testing.T) {
	var blockWS atomic.Bool
	h := newHarness(t, mmfake.Options{RequestHook: func(w http.ResponseWriter, r *http.Request) bool {
		if blockWS.Load() && (r.URL.Path == "/api/v4/websocket" || r.URL.Path == "/api/v4/channels/members/me/view") {
			w.WriteHeader(http.StatusBadGateway)
			return true
		}
		return false
	}})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.SetFocused(true)
	h.w.OpenChannel("c-town")

	h.fake.PostAs("c-offtopic", "bob", "arrived before outage")
	h.eventually(func() bool { return h.w.State().Badge().Unread }, "c-offtopic unread")

	// Outage starts: WS drops and ViewChannel fails while user switches to c-offtopic.
	blockWS.Store(true)
	h.fake.DropConnections(false)
	h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "worker reconnecting")

	h.w.OpenChannel("c-offtopic")
	time.Sleep(80 * time.Millisecond)
	assert.True(t, h.w.State().Badge().Unread, "view failed while offline")

	// Network recovers: going StatusLive retries viewing the open focused channel.
	blockWS.Store(false)
	h.w.Nudge()
	h.live()
	h.eventually(func() bool {
		return !h.w.State().Badge().Unread &&
			h.fake.Member("c-offtopic", "alice").MsgCount == h.fake.Channel("c-offtopic").TotalMsgCount
	}, "reconnect must mark the open focused channel read")
}

func TestFastSwitchAwayStillMarksOpenedUnreadChannelRead(t *testing.T) {
	entered := make(chan struct{})
	proceed := make(chan struct{})
	var entOnce, procOnce sync.Once
	defer procOnce.Do(func() { close(proceed) })

	h := newHarness(t, mmfake.Options{RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v4/channels/members/me/view" {
			return false
		}
		entOnce.Do(func() { close(entered) })
		<-proceed
		return false
	}})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.w.SetFocused(true)
	h.w.OpenChannel("c-town")

	h.fake.PostAs("c-offtopic", "bob", "quick glance")
	h.eventually(func() bool { return h.w.State().Badge() == (state.Badge{Unread: true, Mentions: 0}) }, "plain unread")

	// User clicks unread c-offtopic and immediately switches to personal DM c-dm-bob.
	h.w.OpenChannel("c-offtopic")
	h.w.OpenChannel("c-dm-bob")

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("initial ViewChannel for c-offtopic was skipped on fast channel switch")
	}
	procOnce.Do(func() { close(proceed) })

	h.eventually(func() bool {
		return h.w.State().Badge() == (state.Badge{}) &&
			h.fake.Member("c-offtopic", "alice").MsgCount == h.fake.Channel("c-offtopic").TotalMsgCount
	}, "fast switch to DM after opening unread channel must not leave a yellow unread badge")
}

func TestCRTDeletedReplyMentionClearsBadgeViaTotalsReread(t *testing.T) {
	h := crtHarness(t)
	h.w.SetFocused(false)
	root := h.fake.SeedThread("c-town", "alice", 0)
	rep := h.fake.ReplyAs("c-town", root, "bob", "hey @alice check this")
	h.eventually(func() bool {
		return h.w.State().Badge() == (state.Badge{Unread: true, Mentions: 1})
	}, "thread mention raised badge")

	// Deleting the mentioning reply emits post_deleted without thread_updated on MM 10.11.
	h.fake.DeleteAs(rep.ID)
	h.eventually(func() bool {
		return h.w.State().Badge() == (state.Badge{})
	}, "deleting the sole unread thread mention must reread totals and clear badge")
}
