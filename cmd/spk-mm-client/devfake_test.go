package main

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/api"
	"github.com/spk/spk-mm-client/internal/events"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
	"github.com/spk/spk-mm-client/internal/store"
)

// Each dev run starts the fake on a new port; reusing SPK_MM_CLIENT_HOME
// must not pile up fake servers (the old ones point at dead ports), and a
// real server stays untouched.
func TestSignInToFakeKeepsOneFakeServer(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	svc := api.NewService(st, events.NewEmitter(), func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
	require.NoError(t, svc.Start(ctx))
	t.Cleanup(svc.Close)

	other := mmfake.Start(mmfake.Options{SiteName: "Real MM"})
	t.Cleanup(other.Close)
	_, err = svc.AddServer(ctx, other.URL())
	require.NoError(t, err)

	first := mmfake.Start(mmfake.Options{})
	_, err = signInToFake(ctx, svc, first.URL())
	require.NoError(t, err)
	first.Close() // the previous dev run is over

	second := mmfake.Start(mmfake.Options{})
	t.Cleanup(second.Close)
	_, err = signInToFake(ctx, svc, second.URL())
	require.NoError(t, err)

	list, err := svc.ListServers(ctx)
	require.NoError(t, err)
	var urls []string
	for _, s := range list {
		urls = append(urls, s.URL)
		if s.URL == second.URL() {
			assert.True(t, s.SignedIn)
			assert.Equal(t, "alice", s.Username)
		}
	}
	assert.ElementsMatch(t, []string{other.URL(), second.URL()}, urls)
}

// --mm-fake-servers N: every fake is added and signed in, ids in order.
func TestSignInToFakeSeveral(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	svc := api.NewService(st, events.NewEmitter(), func(string) error { return nil }, &http.Client{Timeout: 5 * time.Second})
	require.NoError(t, svc.Start(ctx))
	t.Cleanup(svc.Close)

	a, b := mmfake.Start(mmfake.Options{}), mmfake.Start(mmfake.Options{})
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	ids, err := signInToFake(ctx, svc, a.URL(), b.URL())
	require.NoError(t, err)
	require.Len(t, ids, 2)

	list, err := svc.ListServers(ctx)
	require.NoError(t, err)
	byID := map[int64]api.ServerDTO{}
	for _, s := range list {
		byID[s.ID] = s
	}
	assert.Equal(t, a.URL(), byID[ids[0]].URL)
	assert.Equal(t, b.URL(), byID[ids[1]].URL)
	assert.True(t, byID[ids[0]].SignedIn)
	assert.True(t, byID[ids[1]].SignedIn)
}

// --mm-fake-churn: posts land in the fakes and the UI is asked to switch
// channels (open_channel), so a long dev run exercises feed/channel churn.
// Every third post is a reply to a recent root of its channel and every
// fifth switch opens a thread (a reply notification's click: channel +
// root), so the soak also drives the thread panel and the thread cache.
func TestFakeChurnPostsRepliesAndOpensThreads(t *testing.T) {
	fake := mmfake.Start(mmfake.Options{ExtraChannels: 3})
	t.Cleanup(fake.Close)
	type opening struct{ ch, root string }
	var mu sync.Mutex
	var opened []opening
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runFakeChurn(ctx, fakeChurn{
			every: time.Millisecond, fakes: []*mmfake.Server{fake}, serverIDs: []int64{7}, channels: 3,
			open: func(id int64, ch, root string) {
				assert.Equal(t, int64(7), id)
				mu.Lock()
				opened = append(opened, opening{ch, root})
				mu.Unlock()
			},
		})
	}()
	channels := []string{"c-town", "c-load-001", "c-load-002", "c-load-003"}
	posts := func() (roots, replies int) {
		for _, ch := range channels {
			for _, p := range fake.VisiblePosts(ch) {
				if p.RootID == "" {
					roots++
				} else {
					replies++
				}
			}
		}
		return roots, replies
	}
	threadOpens := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, o := range opened {
			if o.root != "" {
				n++
			}
		}
		return n
	}
	require.Eventually(t, func() bool { _, r := posts(); return r >= 30 && threadOpens() >= 3 }, 10*time.Second, 5*time.Millisecond)
	cancel()
	<-done

	// Every reply went to a root of its own channel; every thread opening
	// names a root of the channel it opens.
	rootOf := map[string]string{} // root id -> channel
	for _, ch := range channels {
		for _, p := range fake.VisiblePosts(ch) {
			if p.RootID == "" {
				rootOf[p.ID] = ch
			}
		}
	}
	for _, ch := range channels {
		for _, p := range fake.VisiblePosts(ch) {
			if p.RootID != "" {
				assert.Equal(t, ch, rootOf[p.RootID], "a reply goes to a root of its channel")
			}
		}
	}
	threads := threadOpens()
	mu.Lock()
	defer mu.Unlock()
	channelOpens := 0
	for _, o := range opened {
		if o.root == "" {
			channelOpens++
			continue
		}
		assert.Equal(t, o.ch, rootOf[o.root], "a thread opening names a root of its channel")
	}
	assert.Greater(t, channelOpens, threads, "most switches still open a channel")
}

// A churn (soak) run starts the fakes with full client windows in every load
// channel and a capped history, so neither the fake's store nor the client's
// windows grow during the soak: what grows is a real leak. A plain --mm-fake
// run keeps the usual seed.
func TestFakeOptionsForSoak(t *testing.T) {
	// CRT alternates by fake: the first (and every even one) has it on — the
	// soak's replies go through the thread panel, its reads and the thread
	// badges —, the next off — replies inline in the feed with their context
	// line —, so a run with 2+ servers drives both modes.
	churn := desktopOpts{MMFake: true, FakeChannels: 50, FakeChurn: 2 * time.Second}
	assert.Equal(t, mmfake.Options{ExtraChannels: 50, ExtraChannelPosts: state.WindowSize, KeepPosts: state.WindowSize, FilesDir: "/d/tmp/mmfake", CRT: true},
		fakeOptions(churn, 0, "/d/tmp/mmfake"))
	assert.Equal(t, mmfake.Options{ExtraChannels: 50, ExtraChannelPosts: state.WindowSize, KeepPosts: state.WindowSize, FilesDir: "/d/tmp/mmfake"},
		fakeOptions(churn, 1, "/d/tmp/mmfake"))
	assert.True(t, fakeOptions(churn, 2, "/d/tmp/mmfake").CRT)
	// Uploads go to disk: the fake shares the client's process, and memory
	// checks must not count it keeping pasted pictures.
	assert.Equal(t, mmfake.Options{ExtraChannels: 50, FilesDir: "/d/tmp/mmfake"}, fakeOptions(desktopOpts{MMFake: true, FakeChannels: 50}, 0, "/d/tmp/mmfake"))
}

// SPK_MM_CLIENT_SOAK_PROFILES: a soak run leaves heap profiles for
// `go tool pprof -base` diffs.
func TestWriteHeapProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap-001.pb.gz")
	writeHeapProfile(path)
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	zr, err := gzip.NewReader(f) // a pprof profile is a gzipped protobuf
	require.NoError(t, err)
	b, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Contains(t, string(b), "inuse_space")
}
