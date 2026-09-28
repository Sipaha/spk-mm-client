package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThreadThroughService(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	root := fake.SeedThread("c-town", "alice", 3)

	v, err := f.svc.OpenThread(ctx, id, "c-town", root)
	require.NoError(t, err)
	assert.Equal(t, root, v.RootID)
	f.eventually(func() bool {
		v, err := f.svc.GetThread(ctx, id, root)
		return err == nil && v.Loaded && len(v.Posts) == 4
	}, "the thread never loaded")
	require.NoError(t, f.svc.LoadOlderReplies(ctx, id, root))
	require.NoError(t, f.svc.CloseThread(ctx, id))

	_, err = f.svc.OpenThread(ctx, id, "c-nope", root)
	assert.Equal(t, CodeNoChannel, codeOf(err))
	_, err = f.svc.GetThread(ctx, id, "nope")
	assert.Equal(t, CodeNoPost, codeOf(err))
	assert.Equal(t, CodeNoPost, codeOf(f.svc.LoadOlderReplies(ctx, id, "nope")))
	_, err = f.svc.OpenThread(ctx, 999, "c-town", root)
	assert.Error(t, err, "no worker")
}

// A burst of replies in an open thread reaches the UI as one or two
// thread_changed events (coalesced by thread/<srv>/<root>).
func TestThreadChangedIsCoalesced(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	root := fake.SeedThread("c-town", "alice", 2)
	_, err := f.svc.OpenThread(ctx, id, "c-town", root)
	require.NoError(t, err)
	f.eventually(func() bool {
		v, err := f.svc.GetThread(ctx, id, root)
		return err == nil && v.Loaded
	}, "loaded")
	time.Sleep(300 * time.Millisecond)
	for len(f.evs) > 0 {
		<-f.evs
	}
	for i := 0; i < 5; i++ {
		fake.ReplyAs("c-town", root, "bob", "burst")
	}
	deadline := time.After(time.Second)
	n := 0
loop:
	for {
		select {
		case ev := <-f.evs:
			if ev.Type == EventThreadChanged {
				assert.Equal(t, map[string]any{"server_id": id, "root_id": root}, ev.Payload)
				n++
			}
		case <-deadline:
			break loop
		}
	}
	assert.GreaterOrEqual(t, n, 1)
	assert.LessOrEqual(t, n, 2, "5 replies in a burst → 1–2 events, not 5")
}

func TestOpenThreadWithoutARootIsNoPost(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	_, err := f.svc.OpenThread(context.Background(), id, "c-town", "")
	assert.Equal(t, CodeNoPost, codeOf(err))
}

// Task 4: rootID is admitted only for a root this server's thread cache
// holds for that channel (open or recent) — attachTarget/attachRoom,
// SendReply and SaveThreadDraft all refuse an unknown root as no_post,
// the same code OpenThread/GetThread/LoadOlderReplies already use for it.
func TestAttachToUnknownRootIsRefused(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	ctx := context.Background()

	_, err := f.svc.AddAttachmentPath(ctx, id, "c-town", "never-opened", writeFile(t, "x.txt", []byte("x")))
	assert.Equal(t, CodeNoPost, codeOf(err))
	_, err = f.svc.AddAttachmentBytes(ctx, id, "c-town", "never-opened", "x.txt", "", strings.NewReader("x"), 0)
	assert.Equal(t, CodeNoPost, codeOf(err))
	err = f.svc.SendReply(ctx, id, "c-town", "never-opened", "hi", nil)
	assert.Equal(t, CodeNoPost, codeOf(err))
	err = f.svc.SaveThreadDraft(ctx, id, "never-opened", "draft")
	assert.Equal(t, CodeNoPost, codeOf(err))
	// SendReply with no root at all is refused the same way (never a
	// channel post in disguise).
	err = f.svc.SendReply(ctx, id, "c-town", "", "hi", nil)
	assert.Equal(t, CodeNoPost, codeOf(err))

	// Once the thread is open (held), all four work.
	root := fake.SeedThread("c-town", "alice", 1)
	_, err = f.svc.OpenThread(ctx, id, "c-town", root)
	require.NoError(t, err)
	a, err := f.svc.AddAttachmentPath(ctx, id, "c-town", root, writeFile(t, "y.txt", []byte("y")))
	require.NoError(t, err)
	assert.Equal(t, "y.txt", a.Name)
	require.NoError(t, f.svc.SaveThreadDraft(ctx, id, root, "draft text"))
	require.NoError(t, f.svc.SendReply(ctx, id, "c-town", root, "hi", nil))

	// A thread in the *wrong* channel is refused too: ThreadHeld checks
	// both.
	_, err = f.svc.AddAttachmentPath(ctx, id, "c-offtopic", root, writeFile(t, "z.txt", []byte("z")))
	assert.Equal(t, CodeNoPost, codeOf(err))
}

// TestSendReplySessionExpired mirrors TestSetPostSavedSessionExpired: the
// reply's async create() 401 (internal/mmsync/actions.go) signals auth and
// moves the server to needs_reauth; s.writer() then fails the next
// SendReply fast, no network.
func TestSendReplySessionExpired(t *testing.T) {
	f := newChatFixture(t)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.loaded(id, "c-town") }, "prefetch")
	root := fake.SeedThread("c-town", "alice", 1)
	ctx := context.Background()
	_, err := f.svc.OpenThread(ctx, id, "c-town", root)
	require.NoError(t, err)

	fake.SetFailure("/api/v4/posts", http.StatusUnauthorized)
	require.NoError(t, f.svc.SendReply(ctx, id, "c-town", root, "after revoke", nil), "the pending send itself never fails synchronously")
	f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "the reply's 401 did not end the session")
	assert.Equal(t, CodeSessionExpired, codeOf(f.svc.SendReply(ctx, id, "c-town", root, "again", nil)), "fails fast, no network")
}
