package mmsync

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/ws"
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
	// Shown in the panel at once, as pending — and the pending add's own
	// Change carries Threads, not just Channels, so the panel actually
	// gets told to refresh (fix round 1 review).
	h.eventually(func() bool { return h.threadChanged(root) }, "SendReply's pending add never emitted Change.Threads")
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
	// A deterministic sync point instead of a sleep-and-hope: create()
	// calls Files.Wait strictly before CreatePost, in the same goroutine,
	// so once Wait has been entered (and is still blocked — f.open is
	// still false) no POST /api/v4/posts can have happened yet.
	h.eventually(func() bool {
		_, _, waits := files.get()
		return waits >= 1
	}, "create() never reached the upload wait")
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

// Review focus 1 (400 root_id.app_error), fix round 1: the root is deleted
// between the panel opening it and the reply being sent — the pending
// fails like any other error, and the cached thread eventually learns of
// the deletion and marks itself RootDeleted, without a second POST.
//
// DeleteAsQuiet (not DeleteAs) is load-bearing: DeleteAs's post_deleted
// broadcast would mark the thread RootDeleted by itself, through the
// ordinary live-update path (Task 2/3), making this test pass whether or
// not create()'s own root_id.app_error handling does anything at all — a
// reviewer confirmed exactly that by mutation (isRootDeletedErr forced
// false still passed). DeleteAsQuiet deletes in the store without telling
// the client's event stream, so the *only* way the client can learn of the
// deletion is by asking on its own: create()'s MarkThreadStale + reload
// (internal/mmsync/actions.go), whose reread's own 404 is what actually
// flips RootDeleted (internal/state/threads.go FailThread/rootGoneLocked).
func TestRootDeletedBeforeSendFailsThePending(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 1)
	h.openThread(root, 2)
	h.fake.DeleteAsQuiet(root)

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
	assert.Equal(t, 1, h.fake.Hits("POST", "/api/v4/posts"), "the failed send must not be retried automatically")
}

// Fix round 1 (review item 4, ruling): the same 400
// api.post.create_post.root_id.app_error also fires when "the root" is
// itself a reply (mm-10.11 post.go:305) — a live post, not a deleted one.
// isRootDeletedErr alone must never call rootGoneLocked; only the
// confirming reread's own verdict does. Here the reread (GET
// .../thread on the supposed "root") comes back 200 and reveals it was a
// reply all along, so the existing redirect machinery (Task 3
// RedirectThread) takes over — nothing ends up wrongly RootDeleted, and
// the bogus reply-keyed cache entry does not linger.
func TestAmbiguousRootErrorConfirmsInsteadOfAssumingDeleted(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 0)
	reply := h.fake.ReplyAs("c-town", root, "bob", "a reply")

	// A defensive edge case: force the cache to hold a "thread" keyed by
	// the reply's own id — Worker.OpenThread's ThreadRootOf guard would
	// never do this on its own (it maps a held reply to its root first),
	// so this goes straight to state.Server to set the scene.
	_, _, ok := h.w.State().OpenThread("c-town", reply.ID)
	require.True(t, ok)

	require.NoError(t, h.w.SendReply("c-town", reply.ID, "reply to a reply"))
	h.eventually(func() bool {
		v, ok := h.w.State().ThreadView(reply.ID)
		// RedirectThread drops the reply-keyed entry; ThreadView resolves
		// it through the redirect to the real root once that lands.
		return ok && v.RootID == root
	}, "the confirming reread never redirected to the real root")
	assert.False(t, h.thread(root).RootDeleted, "a live root must never end up RootDeleted from an ambiguous error alone")
	assert.False(t, h.w.State().ThreadHeld("c-town", reply.ID), "the bogus reply-keyed entry does not linger")
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

// Fix round 1 (review item 2): Worker.Retry used to emit only
// Change{Channels}, so retrying a failed reply never told the thread
// panel to refresh — only the (CRT-hidden) channel feed. RetryPending now
// derives Threads the same way FailPending/DropPending do.
func TestRetryOfAReplyRefreshesTheThreadToo(t *testing.T) {
	h := crtHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 0)
	h.openThread(root, 1)
	h.fake.FailPosts(1)
	require.NoError(t, h.w.SendReply("c-town", root, "flaky"))
	var failedID string
	h.eventually(func() bool {
		for _, p := range h.thread(root).Posts {
			if p.Failed {
				failedID = p.ID
				return true
			}
		}
		return false
	}, "the reply should have failed")

	// Slow the retry's own CreatePost down: its response (and PostCreated's
	// own, separate Change) cannot land before the plain (non-eventually)
	// assert below runs — otherwise that later Change could carry Threads
	// on its own and mask Retry's not doing so (mutation-tested: without
	// the fix, this assert passed anyway once it raced PostCreated).
	h.fake.SetLatency("/api/v4/posts", time.Second)
	h.resetChanges()
	h.w.Retry("c-town", failedID)
	assert.True(t, h.threadChanged(root), "Retry's own Change never carried Threads")
	h.fake.SetLatency("/api/v4/posts", 0)
	h.eventually(func() bool { return h.threadHas(root, "flaky") == 1 }, "retry did not send")
}

// Fix round 1 (review item 2): Discard already routed through
// state.DropPending, whose Change already carries Threads (like
// FailPending's) — this pins that down with a test, as asked, rather than
// just trusting it stayed that way.
func TestDiscardOfAReplyRefreshesTheThreadToo(t *testing.T) {
	h, files := crtFilesHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 0)
	h.openThread(root, 1)
	f1 := h.uploadTo("c-town", "a.txt")
	files.set(func(f *fakeFiles) { f.fileIDs["a1"] = f1 })
	require.NoError(t, h.w.SendReply("c-town", root, "", staged("a1")...))
	// Files.Wait blocks (the gate is still closed): the pending reply
	// stays pending, deterministically, until Discard.
	h.eventually(func() bool { _, _, waits := files.get(); return waits >= 1 }, "create() never reached the upload wait")
	var pendingID string
	for _, p := range h.thread(root).Posts {
		if p.Pending {
			pendingID = p.ID
		}
	}
	require.NotEmpty(t, pendingID, "the pending reply is not in the thread")

	h.resetChanges()
	h.w.Discard("c-town", pendingID)
	h.eventually(func() bool { return h.threadChanged(root) }, "Discard never emitted Change.Threads")
	h.eventually(func() bool {
		for _, p := range h.thread(root).Posts {
			if p.ID == pendingID {
				return false
			}
		}
		return true
	}, "the discarded reply is still in the thread")
	h.eventually(func() bool { released, _, _ := files.get(); return slices.Equal(released, []string{"a1"}) }, "its file is not released")
}

// Fix round 1 (review item 5): leaving a channel reports its own composer
// and its held threads' as forgotten (internal/state
// TakeForgottenComposers — see also TestTakeForgottenComposersOnChannelLeave
// in internal/state); Worker.releaseForgottenComposers drains that lazily,
// the same way releaseFiles already drains TakeReleased — on the next
// Hooks.Changed-driving action, not necessarily the leave itself.
func TestChannelLeaveReleasesForgottenComposers(t *testing.T) {
	h, files := crtFilesHarness(t)
	root := h.fake.SeedThread("c-town", "alice", 0)
	h.openThread(root, 1)
	h.w.CloseThread() // "recent": still cached, still a forgettable composer

	d, _ := json.Marshal(map[string]any{"channel_id": "c-town"})
	h.w.State().ApplyEvent(ws.Event{Type: "user_removed", Data: d, Broadcast: ws.Broadcast{UserID: h.w.State().Me().ID}})

	// Any later action that calls changed() drains TakeForgottenComposers;
	// a plain Send to an unrelated channel is a convenient trigger.
	require.NoError(t, h.w.Send("c-offtopic", "trigger"))
	h.eventually(func() bool {
		got := files.composersReleased()
		return slices.Contains(got, "c-town/") && slices.Contains(got, "c-town/"+root)
	}, "leaving c-town never released its composer or its thread's")
}
