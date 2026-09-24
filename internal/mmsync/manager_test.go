package mmsync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mattermost/internal/mmfake"
	"github.com/spk/spk-mattermost/internal/store"
)

func TestManagerStartsSignedInServersOnly(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	other, err := h.store.AddServer(context.Background(), store.Server{URL: "https://signed-out.example", Name: "Out"})
	require.NoError(t, err)
	m := NewManager(context.Background(), h.config())
	defer m.Close()
	require.NoError(t, m.StartAll(context.Background()))
	require.NotNil(t, m.Worker(h.srv.ID))
	assert.Nil(t, m.Worker(other.ID))
	h.w = m.Worker(h.srv.ID)
	h.live()
	m.Stop(h.srv.ID)
	assert.Nil(t, m.Worker(h.srv.ID))
	entries, err := h.store.LoadCache(context.Background(), h.srv.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "stop flushes the snapshot")
}
