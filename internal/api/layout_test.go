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
