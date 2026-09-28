package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLayoutDefaultsToZero(t *testing.T) {
	f := newFixture(t)
	l, err := f.svc.GetLayout(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, l.SidebarWidth, "unset: the frontend applies its own default")
	assert.Equal(t, 0, l.ThreadWidth)
}

func TestSetLayoutPersistsAcrossReads(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.svc.SetSidebarWidth(ctx, 300))
	require.NoError(t, f.svc.SetThreadWidth(ctx, 500))

	l, err := f.svc.GetLayout(ctx)
	require.NoError(t, err)
	assert.Equal(t, 300, l.SidebarWidth)
	assert.Equal(t, 500, l.ThreadWidth)
}

func TestSetLayoutIgnoresNonPositiveWidths(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.svc.SetSidebarWidth(ctx, 300))
	require.NoError(t, f.svc.SetSidebarWidth(ctx, 0))
	require.NoError(t, f.svc.SetSidebarWidth(ctx, -5))

	l, err := f.svc.GetLayout(ctx)
	require.NoError(t, err)
	assert.Equal(t, 300, l.SidebarWidth, "a bogus width never overwrites the last good one")
}

// TestSetSidebarWidthClampsToAbsoluteBounds: the 50%-of-window ceiling is
// client-only (Go has no window size), so the server independently enforces
// the absolute bounds (180-480) on every write -- an over/under value is
// clamped, not merely ignored (review follow-up 2026-09-29).
func TestSetSidebarWidthClampsToAbsoluteBounds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	require.NoError(t, f.svc.SetSidebarWidth(ctx, 9000))
	l, err := f.svc.GetLayout(ctx)
	require.NoError(t, err)
	assert.Equal(t, 480, l.SidebarWidth, "clamped to the max")

	require.NoError(t, f.svc.SetSidebarWidth(ctx, 1))
	l, err = f.svc.GetLayout(ctx)
	require.NoError(t, err)
	assert.Equal(t, 180, l.SidebarWidth, "clamped to the min (1 is positive, not ignored like 0)")
}

func TestSetThreadWidthClampsToAbsoluteBounds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	require.NoError(t, f.svc.SetThreadWidth(ctx, 9000))
	l, err := f.svc.GetLayout(ctx)
	require.NoError(t, err)
	assert.Equal(t, 800, l.ThreadWidth, "clamped to the max")

	require.NoError(t, f.svc.SetThreadWidth(ctx, 1))
	l, err = f.svc.GetLayout(ctx)
	require.NoError(t, err)
	assert.Equal(t, 320, l.ThreadWidth, "clamped to the min")
}

// TestGetLayoutClampsBadPersistedValues: a value written before the bounds
// existed (or edited by hand) must never reach the UI unclamped.
func TestGetLayoutClampsBadPersistedValues(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	require.NoError(t, f.svc.st.SetUIPref(ctx, prefSidebarWidth, "99999"))
	require.NoError(t, f.svc.st.SetUIPref(ctx, prefThreadWidth, "5"))

	l, err := f.svc.GetLayout(ctx)
	require.NoError(t, err)
	assert.Equal(t, 480, l.SidebarWidth)
	assert.Equal(t, 320, l.ThreadWidth)
}
