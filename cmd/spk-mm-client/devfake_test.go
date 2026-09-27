package main

import (
	"compress/gzip"
	"context"
	"fmt"
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
func TestFakeChurnPostsAndSwitchesChannels(t *testing.T) {
	fake := mmfake.Start(mmfake.Options{ExtraChannels: 3})
	t.Cleanup(fake.Close)
	var mu sync.Mutex
	var opened []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runFakeChurn(ctx, fakeChurn{
			every: time.Millisecond, fakes: []*mmfake.Server{fake}, serverIDs: []int64{7}, channels: 3,
			open: func(id int64, ch string) {
				assert.Equal(t, int64(7), id)
				mu.Lock()
				opened = append(opened, ch)
				mu.Unlock()
			},
		})
	}()
	posted := func() int {
		n := 0
		for i := 1; i <= 3; i++ {
			n += len(fake.VisiblePosts(fmt.Sprintf("c-load-%03d", i)))
		}
		return n
	}
	require.Eventually(t, func() bool { return posted() > 3*20+20 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	<-done
	assert.NotEmpty(t, opened)
}

// A churn (soak) run starts the fakes with full client windows in every load
// channel and a capped history, so neither the fake's store nor the client's
// windows grow during the soak: what grows is a real leak. A plain --mm-fake
// run keeps the usual seed.
func TestFakeOptionsForSoak(t *testing.T) {
	assert.Equal(t, mmfake.Options{ExtraChannels: 50, ExtraChannelPosts: state.WindowSize, KeepPosts: state.WindowSize, FilesDir: "/d/tmp/mmfake"},
		fakeOptions(desktopOpts{MMFake: true, FakeChannels: 50, FakeChurn: 2 * time.Second}, "/d/tmp/mmfake"))
	// Uploads go to disk: the fake shares the client's process, and memory
	// checks must not count it keeping pasted pictures.
	assert.Equal(t, mmfake.Options{ExtraChannels: 50, FilesDir: "/d/tmp/mmfake"}, fakeOptions(desktopOpts{MMFake: true, FakeChannels: 50}, "/d/tmp/mmfake"))
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
