package mmsync

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/ws"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
	"github.com/spk/spk-mm-client/internal/store"
)

// Root cause 1a: watchWake passes the pre-sleep timestamp (prev) to fire when
// wall time jumps ahead of monotonic time across a system suspend.
func TestWatchWakePassesPreSleepTimestamp(t *testing.T) {
	assert.True(t, slept(30*time.Minute, 5*time.Second, 15*time.Second))
	assert.False(t, slept(5*time.Second, 5*time.Second, 15*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t0 := time.Date(2026, 11, 20, 11, 35, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Second)  // normal tick right before sleep
	t2 := t1.Add(30 * time.Minute) // wake tick after suspend
	times := []time.Time{t0, t1, t2}
	var idx atomic.Int32
	ticks := make(chan time.Time, 2)
	fired := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWakeLoop(ctx, ticks, func() time.Time {
			i := int(idx.Add(1) - 1)
			return times[min(i, len(times)-1)]
		}, func(prev, now time.Time) bool {
			return now.Sub(prev) > 15*time.Second
		}, func(pre time.Time) {
			fired <- pre
		})
	}()
	ticks <- t1
	ticks <- t2
	select {
	case got := <-fired:
		assert.True(t, got.Equal(t1), "watchWake must report the pre-sleep timestamp %v, got %v", t1, got)
	case <-time.After(2 * time.Second):
		t.Fatal("watchWake did not fire on suspend")
	}
	cancel()
	<-done
}

// Root cause 1b: liveMark / live/at must never jump to post-sleep time.Now()
// during flushLoop ticks or runSession teardown; waking from suspend preserves
// the pre-sleep timestamp and catches up messages posted while asleep.
func TestSleepWakeCatchesUpFromPreSleepNotWakeTime(t *testing.T) {
	var (
		mu          sync.Mutex
		track       bool
		sinceParams []int64
		latestCount int
		clk         *clock
	)
	h := newHarness(t, mmfake.Options{
		RequestHook: func(w http.ResponseWriter, r *http.Request) bool {
			// Advance HTTP Date with clk so real wall-clock passage during
			// sleep is mirrored on both client and server (no artificial
			// clock-skew offset masking a jumped gapStart).
			if clk != nil {
				w.Header().Set("Date", clk.now().UTC().Format(http.TimeFormat))
			}
			if strings.Contains(r.URL.Path, "/channels/c-offtopic/posts") {
				mu.Lock()
				if track {
					if raw := r.URL.Query().Get("since"); raw != "" {
						if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
							sinceParams = append(sinceParams, v)
						}
					} else {
						latestCount++
					}
				}
				mu.Unlock()
			}
			return false
		},
	})
	clk = h.useClock()
	// Disable periodic keepalive Pongs so no server liveness is proven during
	// the simulated suspend window.
	h.tune = func(c *Config) {
		c.WS = func(o *ws.Options) { o.PingInterval, o.PingTimeout = time.Hour, time.Hour }
	}
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.eventually(func() bool { return h.liveAt() > 0 }, "initial live/at flushed")

	preSleep := clk.now()
	liveBeforeSleep := h.liveAt()

	// Simulate system suspend: wall clock jumps +10m (>> sinceMargin 2m) while
	// the worker is still "live" (before watchWake or socket failure fires).
	// Multiple flushLoop ticks run with the post-sleep clock.
	clk.advance(10 * time.Minute)
	time.Sleep(200 * time.Millisecond)

	assert.LessOrEqual(t, h.liveAt(), liveBeforeSleep+time.Second.Milliseconds(),
		"flushLoop must not jump live/at to post-sleep time.Now() without proven server liveness")

	// A message was posted while the laptop was asleep (server lost the old WS
	// session during suspend).
	mu.Lock()
	track = true
	mu.Unlock()
	h.fake.DropConnections(true)
	h.fake.PostAs("c-offtopic", "bob", "posted during sleep")

	// watchWake fires with preSleep: clamps liveMark <= preSleep and nudges.
	h.w.onWake(preSleep)

	h.eventually(func() bool {
		return h.hasMessage("c-offtopic", "posted during sleep") && !h.view("c-offtopic").Syncing
	}, "message posted during sleep must be recovered from pre-sleep timestamp")

	mu.Lock()
	gotSince := slices.Clone(sinceParams)
	gotLatest := latestCount
	mu.Unlock()
	require.NotEmpty(t, gotSince, "catch-up after sleep must issue posts?since=")
	assert.Equal(t, 0, gotLatest, "must recover via pre-sleep posts?since=, not metadata reload fallback")
	assert.LessOrEqual(t, gotSince[0], preSleep.UnixMilli(),
		"posts?since= must anchor to pre-sleep timestamp (%d), not post-sleep wake time", preSleep.UnixMilli())
}

// Root cause 1c: while genuinely connected and awake, successful keepalive
// Pongs advance live/at so steady-state idle sessions track server-alive time.
func TestKeepalivePongAdvancesLiveMark(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	clk := h.useClock()
	h.tune = func(c *Config) {
		c.WS = func(o *ws.Options) { o.PingInterval, o.PingTimeout = 40*time.Millisecond, time.Second }
	}
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	h.eventually(func() bool { return h.liveAt() > 0 }, "initial live/at flushed")
	before := h.liveAt()

	clk.advance(5 * time.Minute)
	h.eventually(func() bool { return h.liveAt() >= before+4*time.Minute.Milliseconds() },
		"successful keepalive Pong on a live connection must advance live/at")
}

// Root cause 2: a late reset hello arriving AFTER resumeSettle (3 s in prod)
// on a resumed WS session must still use the pre-disconnect gapStart (never
// the post-settle now) when marking windows stale — both in live handle() and
// when drained during session teardown (applyDrained).
func TestLateResetHelloAfterResumeSettleUsesPreDisconnectGapStart(t *testing.T) {
	t.Run("live handle", func(t *testing.T) {
		var (
			mu          sync.Mutex
			track       bool
			sinceParams []int64
			latestCount int
			clk         *clock
		)
		h := newHarness(t, mmfake.Options{
			RequestHook: func(w http.ResponseWriter, r *http.Request) bool {
				// Mirror clk on HTTP Date so server and client clocks advance
				// together across the outage; this ensures Date anchoring
				// cannot mask a post-settle gapStart regression.
				if clk != nil {
					w.Header().Set("Date", clk.now().UTC().Format(http.TimeFormat))
				}
				if strings.Contains(r.URL.Path, "/channels/c-offtopic/posts") {
					mu.Lock()
					if track {
						if raw := r.URL.Query().Get("since"); raw != "" {
							if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
								sinceParams = append(sinceParams, v)
							}
						} else {
							latestCount++
						}
					}
					mu.Unlock()
				}
				return false
			},
		})
		clk = h.useClock()
		h.tune = func(c *Config) {
			c.resumeSettle = 80 * time.Millisecond
			// Keep keepalive quiet so only resumeSettle proves the resumed socket
			// before the delayed reset hello arrives.
			c.WS = func(o *ws.Options) { o.PingInterval, o.PingTimeout = time.Hour, time.Hour }
		}
		h.start()
		h.live()
		h.eventually(h.allLoaded, "prefetch")

		// Drop the session on the server and post a message during the outage.
		h.fake.SetDown(true)
		h.fake.DropConnections(true)
		h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "disconnected")

		preGap := h.w.live.gapStart()
		require.Greater(t, preGap, int64(0))
		h.fake.PostAs("c-offtopic", "bob", "missed before late hello")

		// Advance clock +10m (>> sinceMargin 2m): if MarkStale used post-settle
		// now instead of preGap, posts?since= would be preGap + ~8m.
		clk.advance(10 * time.Minute)
		mu.Lock()
		track = true
		mu.Unlock()

		// Delay the server's reset hello past resumeSettle (80ms) so resumeSettle
		// fires FIRST and proves the stream at post-settle now.
		h.fake.SetHelloDelay(300 * time.Millisecond)
		h.fake.SetDown(false)

		// Wait until resumeSettle has fired while hello is still delayed.
		h.eventually(func() bool {
			return h.w.live.proven() && h.w.live.gapStart() >= preGap+5*time.Minute.Milliseconds()
		}, "resumeSettle should prove at post-settle time before late hello arrives")

		// Then the late reset hello arrives, rewinds to preGap, bootstraps, and catches up.
		h.eventually(func() bool {
			return h.hasMessage("c-offtopic", "missed before late hello") && !h.view("c-offtopic").Syncing
		}, "late reset hello after resumeSettle must catch up from pre-disconnect gapStart")

		mu.Lock()
		gotSince := slices.Clone(sinceParams)
		gotLatest := latestCount
		mu.Unlock()
		require.Len(t, gotSince, 1, "must catch up with a single posts?since= request")
		assert.Equal(t, 0, gotLatest, "must not rely on metadata reload fallback to mask a wrong gapStart")
		assert.LessOrEqual(t, gotSince[0], preGap,
			"posts?since= (%d) must be anchored to pre-disconnect gapStart (%d), not post-settle now", gotSince[0], preGap)
		assert.GreaterOrEqual(t, gotSince[0], preGap-sinceMargin.Milliseconds()-5*time.Second.Milliseconds(),
			"posts?since= (%d) should be near preGap - sinceMargin", gotSince[0])
		assert.Greater(t, h.w.live.gapStart(), preGap+5*time.Minute.Milliseconds(),
			"after resync completes, liveMark advances to current proven time")
	})

	t.Run("drained reset hello during teardown", func(t *testing.T) {
		st, srv := openStore(t)
		w := NewWorker(Config{Store: st}, srv)
		// Previous proven stream ended at 1_000_000.
		w.live.prove(1_000_000)
		require.True(t, w.endLive())
		preGap := w.live.gapStart()
		require.Equal(t, int64(1_000_000), preGap)

		// Resumed session settled 10 minutes later before receiving any event.
		resumeGap := preGap
		resumePending := true
		w.live.prove(preGap + (10 * time.Minute).Milliseconds())
		w.syncLiveAt()
		require.Equal(t, preGap+(10*time.Minute).Milliseconds(), w.live.gapStart())
		require.Equal(t, preGap+(10*time.Minute).Milliseconds(), w.st.LiveAt())

		// Session tears down while a late reset hello is buffered in conn.Events().
		events := make(chan ws.Event, 1)
		events <- ws.Event{Type: "hello", Reset: true}
		close(events)
		w.drainResume(events, &resumePending, resumeGap)
		wasLive := w.endLive()

		assert.False(t, wasLive, "rewound stream must not report proven at teardown")
		assert.True(t, w.needResync, "drained reset hello must schedule resync on next session")
		assert.Equal(t, preGap, w.live.gapStart(), "drained late reset hello must rewind gapStart to pre-disconnect mark")
		assert.Equal(t, preGap, w.st.LiveAt(), "persisted live/at must also rewind to pre-disconnect mark")
	})
}

// Root cause 3: an in-flight fetch() started before a stream loss / MarkStale
// must not clear Stale=false / GapAfter="" when its stale response lands after
// MarkStale, nor drop the post-resync catch-up.
func TestInFlightFetchBeforeMarkStaleCannotClearStaleAndDropCatchUp(t *testing.T) {
	var (
		mu           sync.Mutex
		firstHit     = make(chan struct{})
		releaseStale = make(chan struct{})
		secondDone   = make(chan struct{})
		intercepted  bool
		seenSecond   bool
	)
	h := newHarness(t, mmfake.Options{
		RequestHook: func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.Contains(r.URL.Path, "/channels/c-offtopic/posts") || r.URL.Query().Get("since") == "" {
				return false
			}
			mu.Lock()
			if !intercepted {
				intercepted = true
				mu.Unlock()
				close(firstHit)
				<-releaseStale
				// Answer the stale pre-outage request with an empty list (as if
				// computed before the second outage's post existed).
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"order":[],"posts":{},"next_post_id":"","prev_post_id":""}`))
				return true
			}
			if !seenSecond {
				seenSecond = true
				defer close(secondDone)
			}
			mu.Unlock()
			return false
		},
	})
	h.tune = func(c *Config) {
		// Single fetcher guarantees the stale fetch lands before the post-resync
		// queue entry is popped, directly exercising the race where an old
		// MergeSince used to clear Stale=false and make SyncItemFor return false.
		c.Fetchers = 1
	}
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")

	// First disconnect starts an in-flight posts?since= for c-offtopic (gen 1).
	h.fake.DropConnections(true)
	select {
	case <-firstHit:
	case <-time.After(5 * time.Second):
		t.Fatal("first since= fetch never started")
	}

	// While that fetch is in flight, a second stream loss happens and a post is
	// created during the second outage.
	h.fake.SetDown(true)
	h.fake.DropConnections(true)
	h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "second disconnect")
	h.fake.PostAs("c-offtopic", "bob", "posted during second outage")
	h.fake.SetDown(false)
	h.live()

	// Wait until the post-resync MarkStale has advanced syncGen (c-offtopic is
	// queued behind the single busy fetcher).
	h.eventually(func() bool {
		_, gen := h.w.State().FetchMode()
		return gen >= 2
	}, "second MarkStale did not advance syncGen")

	// Release the stale pre-MarkStale fetch: it must be dropped and must NOT
	// clear Stale/GapAfter or skip the post-resync catch-up.
	close(releaseStale)

	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("post-resync catch-up was dropped after stale fetch landed")
	}
	h.eventually(func() bool {
		return h.hasMessage("c-offtopic", "posted during second outage") && !h.view("c-offtopic").Syncing
	}, "post from second outage was lost due to in-flight fetch vs MarkStale race")
}

// Root cause 4: posts?since= catch-up must succeed even when the local clock
// is ahead of the server clock by more than sinceMargin (2 min) — both when
// HTTP Date reflects the server clock and when Date is absent/proxy-skewed.
func TestLocalClockAheadOfServerClockCatchesUp(t *testing.T) {
	t.Run("anchored via HTTP Date header", func(t *testing.T) {
		var (
			mu         sync.Mutex
			sinceReqs  int
			latestReqs int
		)
		h := newHarness(t, mmfake.Options{
			RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
				if strings.Contains(r.URL.Path, "/channels/c-offtopic/posts") {
					mu.Lock()
					if r.URL.Query().Get("since") != "" {
						sinceReqs++
					} else {
						latestReqs++
					}
					mu.Unlock()
				}
				return false
			},
		})
		clk := h.useClock()
		clk.advance(10 * time.Minute) // local clock is 10m ahead of server clock
		h.start()
		h.live()
		h.eventually(h.allLoaded, "prefetch")

		mu.Lock()
		beforeSince, beforeLatest := sinceReqs, latestReqs
		mu.Unlock()

		h.fake.SetDown(true)
		h.fake.DropConnections(true)
		h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "disconnected")
		h.fake.PostAs("c-offtopic", "bob", "posted with skewed local clock")
		h.fake.SetDown(false)

		h.eventually(func() bool {
			return h.hasMessage("c-offtopic", "posted with skewed local clock") && !h.view("c-offtopic").Syncing
		}, "catch-up failed when local clock was 10m ahead of server clock")

		mu.Lock()
		gotSince, gotLatest := sinceReqs-beforeSince, latestReqs-beforeLatest
		mu.Unlock()
		assert.Equal(t, 1, gotSince, "server-clock-anchored posts?since= should recover in one request")
		assert.Equal(t, 0, gotLatest, "no fallback loadLatest needed when Date header anchors since=")
	})

	t.Run("proxy-skewed Date falls back via metadata check", func(t *testing.T) {
		var futureDate atomic.Int64
		futureDate.Store(time.Now().Add(10 * time.Minute).Unix())
		h := newHarness(t, mmfake.Options{
			RequestHook: func(w http.ResponseWriter, _ *http.Request) bool {
				// Simulate a reverse proxy whose Date header matches the skewed
				// local clock (+10m) while Mattermost post timestamps remain at
				// real server time.
				w.Header().Set("Date", time.Unix(futureDate.Load(), 0).UTC().Format(http.TimeFormat))
				return false
			},
		})
		clk := h.useClock()
		clk.advance(10 * time.Minute)
		h.start()
		h.live()
		h.eventually(h.allLoaded, "prefetch")

		h.fake.SetDown(true)
		h.fake.DropConnections(true)
		h.eventually(func() bool { return h.w.Status() == StatusReconnecting }, "disconnected")
		h.fake.PostAs("c-offtopic", "bob", "posted behind skewed proxy")
		h.fake.SetDown(false)

		h.eventually(func() bool {
			return h.hasMessage("c-offtopic", "posted behind skewed proxy") && !h.view("c-offtopic").Syncing
		}, "metadata discrepancy check must self-heal even when proxy Date is skewed")
	})
}

// Root cause 5a: an already-holed cached window persisted in SQLite (e.g.
// [11:33, 11:36, 12:46] missing 12:14 with Stale=false and live/at > 12:46)
// must self-heal automatically on cold start while preserving servers, tokens,
// drafts, newer WS posts arriving mid-load, and held history (s.older).
func TestColdStartSelfHealsExistingInteriorHoleInSQLiteCache(t *testing.T) {
	g := newGate()
	h := newHarness(t, mmfake.Options{RequestHook: g.hook})
	t.Cleanup(g.open)

	h.fake.PostAs("c-offtopic", "bob", "msg 11:33")
	h.fake.PostAs("c-offtopic", "bob", "msg 11:36")
	holePost := h.fake.PostAs("c-offtopic", "bob", "msg 12:14")
	h.fake.PostAs("c-offtopic", "bob", "msg 12:46")

	h.start()
	h.live()
	h.eventually(h.allLoaded, "initial prefetch")
	h.w.SaveDraft("c-offtopic", "unsent draft")
	h.stop()

	// Corrupt the cached windows in SQLite to reproduce the exact user state:
	// - c-offtopic holds [..., 11:33, 11:36, 12:46] with 12:14 missing,
	//   Stale=false, GapAfter="", SyncedAt & live/at already past 12:46.
	// - c-town holds [Message #91..#150] with Message #120 missing so we can
	//   also verify held history (s.older) is preserved across self-heal.
	ctx := context.Background()
	entries, err := h.store.LoadCache(ctx, h.srv.ID)
	require.NoError(t, err)
	future := time.Now().Add(5 * time.Minute).UnixMilli()
	var patch []store.CacheEntry
	for _, e := range entries {
		switch {
		case e.Kind == "live" && e.Key == "at":
			b, _ := json.Marshal(future)
			patch = append(patch, store.CacheEntry{Kind: e.Kind, Key: e.Key, Data: b})
		case e.Kind == "posts" && e.Key == "c-offtopic":
			var w state.Window
			require.NoError(t, json.Unmarshal(e.Data, &w))
			w.Posts = slices.DeleteFunc(w.Posts, func(p model.Post) bool { return p.ID == holePost.ID })
			w.Loaded, w.Stale, w.GapAfter, w.SyncedAt = true, false, "", future
			b, _ := json.Marshal(w)
			patch = append(patch, store.CacheEntry{Kind: e.Kind, Key: e.Key, Data: b})
		case e.Kind == "posts" && e.Key == "c-town":
			var w state.Window
			require.NoError(t, json.Unmarshal(e.Data, &w))
			w.Posts = slices.DeleteFunc(w.Posts, func(p model.Post) bool { return p.Message == "Message #120" })
			w.Loaded, w.Stale, w.GapAfter, w.SyncedAt = true, false, "", future
			b, _ := json.Marshal(w)
			patch = append(patch, store.CacheEntry{Kind: e.Kind, Key: e.Key, Data: b})
		}
	}
	require.Len(t, patch, 3, "expected live/at, posts/c-offtopic and posts/c-town in cache")
	require.NoError(t, h.store.SaveCache(ctx, h.srv.ID, patch, nil))

	// Hold c-offtopic and c-town latest-page reloads briefly so we can verify:
	// 1. Offline/restored cache is immediately readable with the draft intact.
	// 2. A newer WS post ("msg 12:50") arriving while loadLatest is in flight
	//    is preserved alongside the recovered "msg 12:14".
	// 3. Held history (s.older via LoadOlder in c-town) survives SetWindow.
	g.hold(func(r *http.Request) bool {
		return (strings.Contains(r.URL.Path, "/channels/c-offtopic/posts") ||
			strings.Contains(r.URL.Path, "/channels/c-town/posts")) &&
			r.URL.Query().Get("before") == ""
	})

	h.start()
	h.live()

	// Cached posts and draft are immediately available before reload finishes.
	vOff := h.view("c-offtopic")
	require.True(t, vOff.Loaded)
	require.True(t, vOff.Syncing)
	assert.Equal(t, "unsent draft", vOff.Draft)
	assert.False(t, h.hasMessage("c-offtopic", "msg 12:14"), "sanity: hole is present before reload")

	// Open c-town and load older history before its latest-page reload lands.
	h.w.OpenChannel("c-town")
	require.NoError(t, h.w.LoadOlder(ctx, "c-town"))
	require.True(t, h.hasMessage("c-town", "Message #50"), "sanity: s.older loaded")

	// Wait until both reloads are in flight, post a newer WS message while
	// c-offtopic's loadLatest is held, then release.
	g.wait(t)
	h.fake.PostAs("c-offtopic", "bob", "msg 12:50")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "msg 12:50") }, "live WS post arrived")
	g.open()

	h.eventually(func() bool {
		return h.hasMessage("c-offtopic", "msg 12:14") &&
			h.hasMessage("c-offtopic", "msg 12:46") &&
			h.hasMessage("c-offtopic", "msg 12:50") &&
			!h.view("c-offtopic").Syncing
	}, "interior hole 12:14 must self-heal while preserving 12:46 and newer WS post 12:50")

	assert.Equal(t, "unsent draft", h.view("c-offtopic").Draft, "draft preserved")

	h.eventually(func() bool {
		return h.hasMessage("c-town", "Message #120") && !h.view("c-town").Syncing
	}, "c-town interior hole must self-heal")
	vTown := h.view("c-town")
	assert.True(t, h.hasMessage("c-town", "Message #50"), "held history s.older preserved across SetWindow")
	assert.False(t, vTown.Gap.Open, "no spurious olderGap when SetWindow reaches old window start")

	// Server record and token remain untouched in SQLite.
	srv, err := h.store.GetServer(ctx, h.srv.ID)
	require.NoError(t, err)
	assert.Equal(t, h.srv.Token, srv.Token)
}

// Root cause 5b: steady-state live traffic and channel switching must NOT
// issue unnecessary full reloads (respecting Mattermost REST rate limits).
func TestSteadyStateLiveTrafficMakesNoExtraReloads(t *testing.T) {
	var postReqs atomic.Int32
	h := newHarness(t, mmfake.Options{
		RequestHook: func(_ http.ResponseWriter, r *http.Request) bool {
			if strings.Contains(r.URL.Path, "/channels/") && strings.HasSuffix(r.URL.Path, "/posts") {
				postReqs.Add(1)
			}
			return false
		},
	})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "initial prefetch")

	baseline := postReqs.Load()
	h.w.OpenChannel("c-offtopic")
	h.w.OpenChannel("c-town")
	h.w.OpenChannel("c-offtopic")
	h.fake.PostAs("c-offtopic", "bob", "steady live 1")
	h.fake.PostAs("c-offtopic", "bob", "steady live 2")
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "steady live 2") }, "live posts arrived")
	// Trigger a metadata refresh while live with no missed posts.
	h.fake.AddChannel("c-extra", "Extra", "alice", "bob")
	h.eventually(func() bool { return h.view("c-extra").Loaded }, "new channel loaded")
	// Only 1 post request should have been made (for the brand-new c-extra).
	assert.Equal(t, baseline+1, postReqs.Load(), "steady-state traffic and channel switching must not reload healthy windows")
}
