package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmsync"
)

func TestJumpThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	_, err := f.svc.OpenChannel(ctx, id, "c-town")
	require.NoError(t, err)

	target := fake.FindPost("c-town", "Message #10")
	res, err := f.svc.JumpToPost(ctx, id, "c-town", target)
	require.NoError(t, err)
	assert.Equal(t, JumpDTO{PostID: target, InFeed: true}, res)
	v, err := f.svc.GetChannel(ctx, id, "c-town")
	require.NoError(t, err)
	assert.True(t, v.Gap.Open)
	assert.Equal(t, "Message #1", v.Posts[0].Message)

	for i := 0; i < 5 && v.Gap.Open; i++ {
		require.NoError(t, f.svc.LoadNewer(ctx, id, "c-town"))
		v, _ = f.svc.GetChannel(ctx, id, "c-town")
	}
	assert.False(t, v.Gap.Open, "loaded up to the window")
	assert.Len(t, v.Posts, 150)
	require.NoError(t, f.svc.RetryRevalidation(ctx, id, "c-town"), "nothing stale: nothing to do")

	_, err = f.svc.JumpToPost(ctx, id, "c-town", "nosuchpostnosuchpostnosuch")
	assert.Equal(t, CodePostGone, codeOf(err))
	_, err = f.svc.JumpToPost(ctx, id, "c-offtopic", target)
	assert.Equal(t, CodeNoChannel, codeOf(err), "not the open channel")
}

func TestJumpValidatesIDs(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	for _, c := range [][2]string{{"", "p1"}, {"c1", ""}, {"c/1", "p1"}, {"c1", "p 1"}} {
		_, err := f.svc.JumpToPost(ctx, 1, c[0], c[1])
		assert.Equal(t, CodeInvalidArgument, codeOf(err), fmt.Sprint(c))
	}
	assert.Equal(t, CodeInvalidArgument, codeOf(f.svc.LoadNewer(ctx, 1, "c/1")))
	assert.Equal(t, CodeInvalidArgument, codeOf(f.svc.RetryRevalidation(ctx, 1, "")))
}

func TestHistErrorCodes(t *testing.T) {
	for err, code := range map[error]string{
		fmt.Errorf("x: %w", mmsync.ErrPostGone): CodePostGone,
		mmsync.ErrForbidden:                     CodeForbidden,
		mmsync.ErrWrongChannel:                  CodeInvalidArgument,
		mmsync.ErrNoChannel:                     CodeNoChannel,
		mmsync.ErrNoProgress:                    CodeNoProgress,
		mmsync.ErrSuperseded:                    CodeCancelled,
		context.Canceled:                        CodeCancelled,
		errors.New("boom"):                      CodeInternal,
	} {
		assert.Equal(t, code, codeOf(histError(err)), err.Error())
	}
	assert.NoError(t, histError(nil))
}
