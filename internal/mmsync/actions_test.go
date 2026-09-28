package mmsync

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
)

// uploadTo puts a file on the fake as alice, in channelID (files_test.go's
// upload always targets c-offtopic).
func (h *harness) uploadTo(channelID, name string) string {
	h.t.Helper()
	info, err := h.w.REST().UploadFile(context.Background(), channelID, name, "", strings.NewReader("abc"), 3, nil)
	require.NoError(h.t, err)
	return info.ID
}

// crtFilesHarness is crtHarness (CRT on, c-town loaded) with a fakeFiles
// wired in, started before Run (files_test.go's filesHarness for a
// non-CRT server).
func crtFilesHarness(t *testing.T) (*harness, *fakeFiles) {
	h := newHarness(t, mmfake.Options{CRT: true})
	files := newFakeFiles()
	h.tune = func(c *Config) { c.Files = files }
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	return h, files
}

// Review focus 1/3 groundwork: a reply is created with RootID, shown in
// its thread at once (pending) and, under CRT, kept out of the channel
// feed (Task 2's rule) until the confirming echo — where it still only
// shows in the thread, never the feed.
func TestReplyIsCreatedWithRootID(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 2)
	h.openThread(root, 3)

	require.NoError(t, h.w.SendReply("c-town", root, "a reply"))
	// Shown in the panel at once, as pending.
	pend := h.thread(root)
	require.Len(t, pend.Posts, 4)
	last := pend.Posts[len(pend.Posts)-1]
	assert.Equal(t, "a reply", last.Message)
	assert.True(t, last.Pending)
	assert.False(t, h.hasMessage("c-town", "a reply"), "a CRT reply never joins the channel feed, even pending")

	h.eventually(func() bool { return h.threadHas(root, "a reply") == 1 }, "reply not confirmed in the thread")
	assert.False(t, h.hasMessage("c-town", "a reply"), "still out of the feed once confirmed (CRT)")
	v := h.thread(root)
	assert.Equal(t, int64(3), v.Posts[0].ReplyCount, "the root's count follows the reply")
}

// Without CRT, a reply shows in both the channel feed and its thread (T7
// fix in Task 2's terms carried to replies).
func TestReplyIsCreatedWithRootIDWithoutCRT(t *testing.T) {
	h := newHarness(t, mmfake.Options{})
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	root := h.fake.SeedThread("c-town", "alice", 1)
	_, ok := h.w.OpenThread("c-town", root)
	require.True(t, ok)
	h.eventually(func() bool { v := h.thread(root); return v.Loaded && len(v.Posts) == 2 }, "thread never loaded")

	require.NoError(t, h.w.SendReply("c-town", root, "seen everywhere"))
	h.eventually(func() bool { return h.hasMessage("c-town", "seen everywhere") }, "reply missing from the feed")
	assert.Equal(t, 1, h.threadHas(root, "seen everywhere"), "and in the thread")
}

// Review focus 1: the reply's own attachments are taken by (channel,
// root) and reach CreatePost as file ids — the same wait/timeout shape as
// Send (files_test.go TestSendWithFilesWaitsForTheUploadsThenCreates).
func TestReplyWithFilesWaitsAndCreates(t *testing.T) {
	h, files := crtFilesHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 0)
	h.openThread(root, 1)
	f1 := h.uploadTo("c-town", "a.txt")
	files.set(func(f *fakeFiles) { f.fileIDs["a1"] = f1 })

	require.NoError(t, h.w.SendReply("c-town", root, "", staged("a1")...), "files without text")
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, h.fake.Hits("POST", "/api/v4/posts"), "not created while the upload runs")

	files.set(func(f *fakeFiles) { f.open = true })
	h.eventually(func() bool {
		v := h.thread(root)
		for _, p := range v.Posts {
			if len(p.Files) == 1 && p.Files[0].ID == f1 && !p.Pending {
				return true
			}
		}
		return false
	}, "the reply with its file never landed in the thread")
}

// Review focus 1 (400 root_id.app_error): the root is deleted between the
// panel opening it and the reply being sent — the pending fails like any
// other error, and the cached thread is marked RootDeleted (FailThread's
// ThreadNotFound path), not left stuck loading forever.
func TestRootDeletedBeforeSendFailsThePending(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 1)
	h.openThread(root, 2)
	h.fake.DeleteAs(root)

	require.NoError(t, h.w.SendReply("c-town", root, "too late"))
	h.eventually(func() bool {
		for _, p := range h.thread(root).Posts {
			if p.Message == "too late" && p.Failed {
				return true
			}
		}
		return false
	}, "the pending reply was never marked failed")
	h.eventually(func() bool { return h.thread(root).RootDeleted }, "the thread was not marked root-deleted")
}

// session_expired for SendReply: like every other action in actions.go,
// CreatePost's own 401 (here: the reply's async create, not SendReply's
// synchronous return, which never fails) signals auth through
// Worker.actionErr's twin, create()'s inline check. Mirrored at the api
// layer by s.writer() (see internal/api/threads_test.go /
// internal/api/chat_test.go).
func TestSendReplySessionExpiredSignalsAuth(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 0)
	h.openThread(root, 1)
	h.fake.SetFailure("/api/v4/posts", http.StatusUnauthorized)
	require.NoError(t, h.w.SendReply("c-town", root, "after revoke"), "SendReply itself never fails synchronously")
	h.eventually(func() bool { return h.w.Status() == StatusNeedsReauth }, "the reply's 401 did not signal auth")
}
