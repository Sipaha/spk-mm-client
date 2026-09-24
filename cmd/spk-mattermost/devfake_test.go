package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/api"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/store"
)

// Each dev run starts the fake on a new port; reusing SPK_MATTERMOST_HOME
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
	require.NoError(t, signInToFake(ctx, svc, first.URL()))
	first.Close() // the previous dev run is over

	second := mmfake.Start(mmfake.Options{})
	t.Cleanup(second.Close)
	require.NoError(t, signInToFake(ctx, svc, second.URL()))

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
