package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Composer brief 2026-09-28: the Aa toggle (show/hide the formatting bar)
// persists app-wide, the same ui_prefs mechanism as the layout splitters
// (internal/store's uiprefs.go). Unset ("never saved") must read back as
// "shown" -- the webapp's own default.

func TestGetFormattingBarHiddenDefaultsToShown(t *testing.T) {
	f := newFixture(t)
	hidden, err := f.svc.GetFormattingBarHidden(context.Background())
	require.NoError(t, err)
	assert.False(t, hidden, "unset: shown by default, like the webapp")
}

func TestSetFormattingBarHiddenPersistsAcrossReads(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	require.NoError(t, f.svc.SetFormattingBarHidden(ctx, true))
	hidden, err := f.svc.GetFormattingBarHidden(ctx)
	require.NoError(t, err)
	assert.True(t, hidden)

	require.NoError(t, f.svc.SetFormattingBarHidden(ctx, false))
	hidden, err = f.svc.GetFormattingBarHidden(ctx)
	require.NoError(t, err)
	assert.False(t, hidden)
}
