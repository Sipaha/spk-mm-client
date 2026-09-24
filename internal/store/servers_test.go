package store

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerCRUD(t *testing.T) {
	ctx := context.Background()
	st := openTest(t)

	a, err := st.AddServer(ctx, Server{URL: "https://mm.example.com", SiteURL: "https://mm.example.com", Name: "Example", GitLab: true})
	require.NoError(t, err)
	assert.NotZero(t, a.ID)
	assert.NotZero(t, a.CreatedAt)
	assert.False(t, a.SignedIn())

	_, err = st.AddServer(ctx, Server{URL: "https://mm.example.com", Name: "dup"})
	assert.ErrorIs(t, err, ErrServerExists)

	require.NoError(t, st.SetSession(ctx, a.ID, "tok-1", "u1", "alice"))
	got, err := st.GetServer(ctx, a.ID)
	require.NoError(t, err)
	assert.True(t, got.SignedIn())
	assert.Equal(t, "tok-1", got.Token)
	assert.Equal(t, "alice", got.Username)
	assert.True(t, got.GitLab)

	require.NoError(t, st.ClearSession(ctx, a.ID))
	got, err = st.GetServer(ctx, a.ID)
	require.NoError(t, err)
	assert.False(t, got.SignedIn())
	assert.Empty(t, got.UserID)

	b, err := st.AddServer(ctx, Server{URL: "https://b.example.com", Name: "B"})
	require.NoError(t, err)
	list, err := st.ListServers(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, a.ID, list[0].ID)
	assert.Equal(t, b.ID, list[1].ID)
	assert.Equal(t, 1, list[1].Sort, "new servers go to the end")

	require.NoError(t, st.DeleteServer(ctx, a.ID))
	_, err = st.GetServer(ctx, a.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, st.SetSession(ctx, a.ID, "t", "u", "n"), ErrNotFound)
	assert.ErrorIs(t, st.DeleteServer(ctx, a.ID), ErrNotFound)
}

func TestServerLogValueHidesToken(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("server", "srv", Server{ID: 7, URL: "https://mm", Token: "super-secret-token"})
	assert.NotContains(t, buf.String(), "super-secret-token")
	assert.Contains(t, buf.String(), `"signed_in":true`)
	assert.Contains(t, buf.String(), "https://mm")
}
