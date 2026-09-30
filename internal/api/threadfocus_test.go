package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/mmsync"
)

func TestOpenThreadAtThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := mmfake.Start(mmfake.Options{CRT: true})
	t.Cleanup(fake.Close)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	_, err := f.svc.OpenChannel(ctx, id, "c-town")
	require.NoError(t, err)
	root := fake.SeedThread("c-town", "alice", 300)
	target := fake.FindPost("c-town", "Reply 50")

	v, err := f.svc.OpenThreadAt(ctx, id, "c-town", root, target)
	require.NoError(t, err)
	require.NotNil(t, v.Focus)
	assert.Equal(t, target, v.Focus.TargetID)
	assert.True(t, v.Focus.HasOlder)
	f.eventually(func() bool {
		v, _ = f.svc.GetThread(ctx, id, root)
		return v.Loaded && !v.Syncing
	}, "the tail")
	for i := 0; i < 5 && v.Focus.HasNewer; i++ {
		require.NoError(t, f.svc.LoadThreadFocus(ctx, id, root, true))
		v, _ = f.svc.GetThread(ctx, id, root)
	}
	assert.False(t, v.Focus.Gap.Open, "loaded up to the tail")
	require.NoError(t, f.svc.LoadThreadFocus(ctx, id, root, false))
	v, _ = f.svc.GetThread(ctx, id, root)
	assert.Equal(t, "Reply 1", v.Posts[1].Message)

	_, err = f.svc.OpenThreadAt(ctx, id, "c-town", root, "nosuchpostnosuchpostnosuch")
	assert.Equal(t, CodePostGone, codeOf(err))
	other := fake.PostAs("c-town", "bob", "another root")
	_, err = f.svc.OpenThreadAt(ctx, id, "c-town", other.ID, target)
	assert.Equal(t, CodeInvalidArgument, codeOf(err), "a reply of another thread")
}

func TestOpenThreadAtValidatesIDs(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	for _, c := range [][3]string{{"", "r1", "p1"}, {"c1", "", "p1"}, {"c1", "r1", ""}, {"c/1", "r1", "p1"}, {"c1", "r 1", "p1"}, {"c1", "r1", "p?"}} {
		_, err := f.svc.OpenThreadAt(ctx, 1, c[0], c[1], c[2])
		assert.Equal(t, CodeInvalidArgument, codeOf(err), fmt.Sprint(c))
	}
	assert.Equal(t, CodeInvalidArgument, codeOf(f.svc.LoadThreadFocus(ctx, 1, "r/1", true)))
	assert.Equal(t, CodeInvalidArgument, codeOf(histError(mmsync.ErrWrongThread)))
}
