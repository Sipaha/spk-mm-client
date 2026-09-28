package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUIPrefRoundTrip(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	v, err := st.GetUIPref(ctx, "layout.sidebar_width")
	require.NoError(t, err)
	assert.Equal(t, "", v, "unset key reads as empty, not an error")

	require.NoError(t, st.SetUIPref(ctx, "layout.sidebar_width", "300"))
	v, err = st.GetUIPref(ctx, "layout.sidebar_width")
	require.NoError(t, err)
	assert.Equal(t, "300", v)
}

func TestUIPrefUpsertOverwrites(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	require.NoError(t, st.SetUIPref(ctx, "layout.thread_width", "420"))
	require.NoError(t, st.SetUIPref(ctx, "layout.thread_width", "500"))

	v, err := st.GetUIPref(ctx, "layout.thread_width")
	require.NoError(t, err)
	assert.Equal(t, "500", v, "a later Set replaces the value, not a duplicate row")
}
