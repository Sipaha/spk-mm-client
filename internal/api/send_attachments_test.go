package api

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/attach"
	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))))
	return b.Bytes()
}

// pendingPost is the channel's post being sent (or failed), if any.
func (f *chatFixture) pendingPost(id int64, ch string) (state.PostView, bool) {
	v, err := f.svc.GetChannel(context.Background(), id, ch)
	require.NoError(f.t, err)
	for _, p := range v.Posts {
		if p.PendingPostID == p.ID {
			return p, true
		}
	}
	return state.PostView{}, false
}

// confirmed is the server's post that replaced the pending one.
func (f *chatFixture) confirmed(id int64, ch, pendingID string) (state.PostView, bool) {
	v, err := f.svc.GetChannel(context.Background(), id, ch)
	require.NoError(f.t, err)
	for _, p := range v.Posts {
		if p.PendingPostID == pendingID && p.ID != pendingID {
			return p, true
		}
	}
	return state.PostView{}, false
}

func (f *chatFixture) failedPost(id int64, ch string) state.PostView {
	f.t.Helper()
	var p state.PostView
	f.eventually(func() bool {
		var ok bool
		p, ok = f.pendingPost(id, ch)
		return ok && p.Failed
	}, "the post never failed")
	return p
}

func lastPost(fake *mmfake.Server, ch string) model.Post {
	l := fake.VisiblePosts(ch)
	return l[len(l)-1]
}

func TestSendPostWithAttachments(t *testing.T) {
	f := newChatFixture(t)
	dir := f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	f.offline(fake, id) // the pending post is seen before the uploads
	pic := pngBytes(t, 64, 48)
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "", "Screenshot.png", "image/png", bytes.NewReader(pic), 0)
	require.NoError(t, err)
	b, err := f.svc.AddAttachmentPath(ctx, id, "c-offtopic", "", writeFile(t, "Notes.TXT", []byte("hello")))
	require.NoError(t, err)

	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "look", []string{a.ID, b.ID}))
	p, ok := f.pendingPost(id, "c-offtopic")
	require.True(t, ok, "shown at once")
	assert.True(t, p.Pending)
	assert.Equal(t, "look", p.Message)
	assert.Equal(t, []state.FileView{
		{ID: a.ID, Name: "Screenshot.png", Ext: "png", Size: int64(len(pic)), Mime: "image/png", Width: 64, Height: 48, Staged: true, State: "staged"},
		{ID: b.ID, Name: "Notes.TXT", Ext: "txt", Size: 5, Mime: "text/plain", Staged: true, State: "staged"},
	}, p.Files, "the local files, previews from /media/<srv>/staged/<id>")
	list, err := f.svc.Attachments(ctx, id, "c-offtopic", "")
	require.NoError(t, err)
	assert.Empty(t, list, "gone from the composer")
	for {
		ev := f.nextEvent(EventAttachmentsChanged)
		if items := ev.Payload["items"].([]AttachmentView); len(items) == 0 {
			break // the composer is told
		}
	}
	assert.Equal(t, CodeNotFound, codeOf(f.svc.RemoveAttachment(ctx, id, a.ID)), "no longer the composer's to remove")
	assert.Equal(t, CodeNotFound, codeOf(f.svc.RetryAttachment(ctx, id, a.ID)))
	time.Sleep(200 * time.Millisecond)
	p, _ = f.pendingPost(id, "c-offtopic")
	assert.True(t, p.Pending && !p.Failed, "offline, the post waits for its uploads")
	assert.Zero(t, fake.Hits("POST", "/api/v4/posts"))

	fake.SetDown(false)
	require.NoError(t, f.svc.NetworkChanged(ctx))
	var got state.PostView
	f.eventually(func() bool { got, ok = f.confirmed(id, "c-offtopic", p.ID); return ok }, "never confirmed")
	require.Len(t, got.Files, 2)
	assert.Equal(t, "Screenshot.png", got.Files[0].Name)
	assert.Equal(t, "Notes.TXT", got.Files[1].Name)
	assert.False(t, got.Files[0].Staged, "the server's files now")
	assert.NotEqual(t, a.ID, got.Files[0].ID)
	_, ok = f.pendingPost(id, "c-offtopic")
	assert.False(t, ok)
	sent := lastPost(fake, "c-offtopic")
	assert.Equal(t, "look", sent.Message)
	assert.Len(t, sent.FileIDs, 2)
	f.eventually(func() bool {
		_, okA := f.svc.att.Get(a.ID)
		_, okB := f.svc.att.Get(b.ID)
		des, _ := os.ReadDir(dir)
		return !okA && !okB && len(des) == 0
	}, "the attachments and the spool are let go of")
	assert.Equal(t, 2, fake.Hits("POST", "/api/v4/files"))
	assert.Equal(t, 1, fake.Hits("POST", "/api/v4/posts"))
}

// A sending post's staged file shows live upload progress (State, at
// least), and the UI is told through the same channel_changed path a
// post/window update uses — the controller carry-over from Task 3's
// review. The upload is throttled server-side so the client's UploadFile
// call (and so the attachment's "uploading" state) spans a stretch of real
// time: the fake reads the request body slowly, so its HTTP response (and
// so the attach store's move to "uploaded") does not arrive until then,
// regardless of how fast the small body itself reaches the kernel.
func TestSendPostShowsUploadProgressAndTellsTheUI(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	fake.SetUploadThrottle(2000) // ~1s for 2000 bytes of body
	t.Cleanup(func() { fake.SetUploadThrottle(0) })
	data := bytes.Repeat([]byte{1}, 2000)
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "", "big.bin", "application/octet-stream", bytes.NewReader(data), 0)
	require.NoError(t, err)

	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "", []string{a.ID}))
	pending, ok := f.pendingPost(id, "c-offtopic")
	require.True(t, ok, "shown at once")

	// Drain events in the background looking for a channel_changed of this
	// channel while the file is still uploading (proves onAttachments
	// reused the existing view-update path, not just the composer event).
	sawChannelChangedMidUpload := make(chan bool, 1)
	go func() {
		timeout := time.After(5 * time.Second)
		for {
			select {
			case ev := <-f.evs:
				if ev.Type == EventChannelChanged && ev.Payload["server_id"] == id && ev.Payload["channel_id"] == "c-offtopic" {
					p, ok := f.pendingPost(id, "c-offtopic")
					if ok && len(p.Files) == 1 && p.Files[0].State == "uploading" {
						sawChannelChangedMidUpload <- true
						return
					}
				}
			case <-timeout:
				sawChannelChangedMidUpload <- false
				return
			}
		}
	}()
	f.eventually(func() bool {
		p, ok := f.pendingPost(id, "c-offtopic")
		return ok && len(p.Files) == 1 && p.Files[0].State == "uploading"
	}, "never saw the pending post's file go to uploading")
	assert.True(t, <-sawChannelChangedMidUpload, "channel_changed never told the UI about the progress")

	var got state.PostView
	f.eventually(func() bool { got, ok = f.confirmed(id, "c-offtopic", pending.ID); return ok }, "the post was never confirmed")
	assert.Len(t, got.Files, 1)
	assert.False(t, got.Files[0].Staged, "the confirmed post shows the server's file, not the staged one")
}

// Fix round 1 (review item 3): onAttachments used to emit only
// thread_changed for a reply's composer (root != ""), but without CRT a
// pending reply is also shown inline in the channel feed (T7) — so a
// reply's upload progress must reach the channel view too, not just the
// thread panel's. onAttachments now always emits both when the pending
// post's progress actually changed.
//
// Fix round 2 (re-review of 29904b4): SendReply's own pending-add Change
// always carries both Channels and Threads (internal/mmsync/actions.go
// send()), independent of onAttachments entirely — so the first version of
// this test passed even with onAttachments reverted to its old
// thread-only behavior: it was really observing SendReply's own event,
// never onAttachments' progress-driven one.
//
// Two timing-based fixes (drain-then-wait-for-one-more, then count-over-a-
// window) both turned out to be flaky or outright broken against the real
// fake server: a small file's progress callback reports "fully sent"
// essentially in one shot (the body is read from a spooled file in one or
// two Read calls well under any buffering threshold, regardless of how
// slowly mmfake's copyBodyThrottled then drains the *server* side of the
// connection) — so onAttachments' own progress-driven call and SendReply's
// own pending-add call land within the same ~100ms coalescing window
// essentially every time, and a second, later, distinguishable occurrence
// of either event may simply never happen, at any file size or throttle
// this test controls from the outside.
//
// The reliable fix is to stop relying on that timing at all: take the
// server offline (f.offline, the same helper
// TestFailedUploadFailsThePostAndRetryKeepsWhatWasUploaded uses) before
// attaching anything, so the attachment provably stays staged — nothing
// can progress it — and SendReply's own pending-add burst is the only
// thing that can happen. Once that is drained, coming back online is the
// one and only thing left that can produce a channel_changed/
// thread_changed pair: onAttachments' progress path, deterministically —
// not a race against how fast a small file's Read calls happen to land
// (client-side, a small spooled file is read to completion in one or two
// Reads regardless of how slowly the *server* then drains the
// connection, so a real upload's progress callback fires once, not many
// times), and not contaminated by an unrelated resync: going back online
// after f.offline also lets the worker reconnect, and a reconnect's own
// metadata refresh can touch channel_changed for reasons that have
// nothing to do with this attachment (confirmed: that alone was still
// enough to pass with onAttachments reverted to thread-only). Staying
// offline throughout and driving the one call under test directly — the
// same call attach.Store's own OnChange hook makes on a real progress
// report — removes every source of that but the fix itself.
// TestSendReplyUploadProgressTellsBothChannelAndThread proves onAttachments'
// "always both" behaviour (attachments.go) for a reply's progress with a
// change RefreshPendingProgress must actually notice on its own — not just
// piggy-backing on some other, unrelated Change that happens to touch the
// same (channel, thread) coalescing keys.
//
// That's a real trap here, not a hypothetical one: onChanged (sync.go, for
// a state.Change) and onAttachments schedule the *same* coalescer keys
// ("channel/%d/%s", "thread/%d/%s" — see attachments.go). SendReply's own
// queueing of the pending post already schedules those keys once, and a
// Coalescer.Schedule call before a job fires only replaces its function —
// so a first attempt at this test (naively sending, uploading, and
// collecting events soon after) kept "passing" under a thread-only
// onAttachments too: whatever fired for the channel key traced back to
// SendReply's own schedule, never disproving anything about onAttachments.
//
// This version forces the post's own lifecycle to fully settle (Failed,
// permanently — no automatic retry) and every one of its Changes to be
// drained *before* causing a second, isolated attachment-state change:
// attach.Store.Retry after the file itself also failed. From that point
// nothing but onAttachments schedules "channel/…"/"thread/…" for this
// thread — the Worker never touches a settled Failed pending post on its
// own — so any channel_changed/thread_changed seen afterwards can only come
// from onAttachments noticing the retried upload's progress.
func TestSendReplyUploadProgressTellsBothChannelAndThread(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	f.eventually(func() bool { _, err := f.svc.GetChannel(ctx, id, "c-town"); return err == nil }, "c-town never loaded")
	root := fake.SeedThread("c-town", "alice", 1)
	_, err := f.svc.OpenThread(ctx, id, "c-town", root)
	require.NoError(t, err)
	f.eventually(func() bool { v, err := f.svc.GetThread(ctx, id, root); return err == nil && v.Loaded }, "thread never loaded")

	fake.FailUploads(1) // exactly one upload attempt fails; the next succeeds
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-town", root, "big.bin", "application/octet-stream", strings.NewReader("abc"), 0)
	require.NoError(t, err)

	require.NoError(t, f.svc.SendReply(ctx, id, "c-town", root, "", []string{a.ID}))
	var pendingID string
	f.eventually(func() bool {
		v, err := f.svc.GetThread(ctx, id, root)
		if err != nil {
			return false
		}
		for _, p := range v.Posts {
			if p.Failed {
				pendingID = p.ID
				return true
			}
		}
		return false
	}, "the reply was never marked failed")
	defer func() { _ = f.svc.DiscardPost(ctx, id, "c-town", pendingID) }()
	f.eventually(func() bool {
		got, ok := f.svc.att.Get(a.ID)
		return ok && got.State == attach.StateFailed
	}, "the attachment itself never failed")

	// The whole initial send/fail settled — drain every Change it produced
	// (including onChanged's own channel/thread schedule for this reply)
	// before the isolated retry below.
	for drained := true; drained; {
		select {
		case <-f.evs:
		case <-time.After(200 * time.Millisecond):
			drained = false
		}
	}

	// Retry only touches attach.Store — the pending post stays Failed and
	// settled, so the Worker never schedules these keys again on its own
	// from here. FailUploads(1) already spent its one failure, so this
	// attempt succeeds.
	require.NoError(t, f.svc.att.Retry(a.ID))

	var sawChannel, sawThread bool
	timeout := time.After(3 * time.Second)
collect:
	for {
		select {
		case ev := <-f.evs:
			if ev.Type == EventChannelChanged && ev.Payload["server_id"] == id && ev.Payload["channel_id"] == "c-town" {
				sawChannel = true
			}
			if ev.Type == EventThreadChanged && ev.Payload["server_id"] == id && ev.Payload["root_id"] == root {
				sawThread = true
			}
			if sawChannel && sawThread {
				break collect
			}
		case <-timeout:
			break collect
		}
	}
	assert.True(t, sawChannel, "onAttachments never told the channel about the reply's progress")
	assert.True(t, sawThread, "onAttachments never told the thread about the reply's progress")
}

func TestSendPostWithOnlyAttachments(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	a, err := f.svc.AddAttachmentPath(ctx, id, "c-offtopic", "", writeFile(t, "only.txt", []byte("x")))
	require.NoError(t, err)
	other, err := f.svc.AddAttachmentPath(ctx, id, "c-town", "", writeFile(t, "other.txt", []byte("x")))
	require.NoError(t, err)

	assert.Equal(t, CodeEmptyMessage, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", " ", nil)))
	assert.Equal(t, CodeNotFound, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", "", []string{a.ID, other.ID})), "another channel's")
	assert.Equal(t, CodeNotFound, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", "", []string{"nope"})))
	_, pending := f.pendingPost(id, "c-offtopic")
	assert.False(t, pending, "a refused send shows nothing")
	list, _ := f.svc.Attachments(ctx, id, "c-offtopic", "")
	require.Len(t, list, 1, "and keeps the composer")

	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "", []string{a.ID}))
	f.eventually(func() bool { return fake.Hits("POST", "/api/v4/posts") == 1 }, "not sent")
	f.eventually(func() bool {
		sent := lastPost(fake, "c-offtopic")
		return sent.Message == "" && len(sent.FileIDs) == 1
	}, "a post of just a file")
}

func TestFailedUploadFailsThePostAndRetryKeepsWhatWasUploaded(t *testing.T) {
	f := newChatFixture(t)
	dir := f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	f.offline(fake, id)
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "", "a.bin", "", strings.NewReader("aaa"), 0)
	require.NoError(t, err)
	b, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "", "b.bin", "", strings.NewReader("bbb"), 0)
	require.NoError(t, err)
	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "two files", []string{a.ID, b.ID}))
	fake.FailUploads(1)
	fake.SetDown(false)
	require.NoError(t, f.svc.NetworkChanged(ctx))

	p := f.failedPost(id, "c-offtopic")
	f.eventually(func() bool { return fake.Hits("POST", "/api/v4/files") == 2 }, "both tried")
	assert.Zero(t, fake.Hits("POST", "/api/v4/posts"))
	assert.Len(t, p.Files, 2, "the failed post keeps its files")

	require.NoError(t, f.svc.RetryPost(ctx, id, "c-offtopic", p.ID))
	f.eventually(func() bool { _, ok := f.confirmed(id, "c-offtopic", p.ID); return ok }, "retry never sent")
	assert.Equal(t, 3, fake.Hits("POST", "/api/v4/files"), "only the failed upload is sent again")
	assert.Len(t, lastPost(fake, "c-offtopic").FileIDs, 2)
	f.eventually(func() bool { des, _ := os.ReadDir(dir); return len(des) == 0 }, "spools deleted")
}

func TestDiscardPostDropsItsAttachments(t *testing.T) {
	f := newChatFixture(t)
	dir := f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	f.offline(fake, id)
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "", "a.bin", "", strings.NewReader("aaa"), 0)
	require.NoError(t, err)
	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "never mind", []string{a.ID}))
	p, ok := f.pendingPost(id, "c-offtopic")
	require.True(t, ok)

	require.NoError(t, f.svc.DiscardPost(ctx, id, "c-offtopic", p.ID))
	_, ok = f.pendingPost(id, "c-offtopic")
	assert.False(t, ok)
	_, ok = f.svc.att.Get(a.ID)
	assert.False(t, ok, "the attachment is removed")
	des, _ := os.ReadDir(dir)
	assert.Empty(t, des, "with its spool")

	fake.SetDown(false)
	require.NoError(t, f.svc.NetworkChanged(ctx))
	f.eventually(func() bool { return f.server(id).State == "live" }, "live again")
	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, fake.Hits("POST", "/api/v4/files"), "nothing uploaded")
	assert.Zero(t, fake.Hits("POST", "/api/v4/posts"), "nothing sent")
}

func TestSendPostWithAttachmentsAndAnExpiredSession(t *testing.T) {
	f := newChatFixture(t)
	dir := f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	f.offline(fake, id)
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "", "a.bin", "", strings.NewReader("aaa"), 0)
	require.NoError(t, err)
	b, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "", "b.bin", "", strings.NewReader("bbb"), 0)
	require.NoError(t, err)
	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "sent as the session dies", []string{a.ID}))
	fake.SetFailure("/api/v4/files", 401)
	fake.SetDown(false)
	require.NoError(t, f.svc.NetworkChanged(ctx))

	f.failedPost(id, "c-offtopic")
	f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "the upload's 401 ends the session")
	assert.Zero(t, fake.Hits("POST", "/api/v4/posts"))
	assert.Equal(t, CodeSessionExpired, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", "", []string{b.ID})), "fails fast")
	list, _ := f.svc.Attachments(ctx, id, "c-offtopic", "")
	require.Len(t, list, 1, "the refused attachment stays in the composer")
	assert.Equal(t, b.ID, list[0].ID)

	// Signing in again replaces the worker: its pending posts are gone, and
	// so are their attachments; the composer's stay.
	fake.SetFailure("/api/v4/files", 0)
	_, err = f.svc.LoginWithPassword(ctx, id, "alice", "secret")
	require.NoError(t, err)
	f.eventually(func() bool { _, ok := f.svc.att.Get(a.ID); return !ok }, "the lost post's attachment is let go of")
	_, ok := f.svc.att.Get(b.ID)
	assert.True(t, ok)
	f.eventually(func() bool {
		des, _ := os.ReadDir(dir)
		return len(des) == 1 && des[0].Name() == "attach-"+b.ID
	}, "only the composer's spool is left")
	// b may have met the 401 too (it uploads as soon as it is live); it is
	// never retried by itself.
	require.NoError(t, f.svc.RetryAttachment(ctx, id, b.ID))
	f.attachmentState(id, "c-offtopic", b.ID, attach.StateUploaded)
}
