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
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "Screenshot.png", "image/png", bytes.NewReader(pic), 0)
	require.NoError(t, err)
	b, err := f.svc.AddAttachmentPath(ctx, id, "c-offtopic", writeFile(t, "Notes.TXT", []byte("hello")))
	require.NoError(t, err)

	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "look", []string{a.ID, b.ID}))
	p, ok := f.pendingPost(id, "c-offtopic")
	require.True(t, ok, "shown at once")
	assert.True(t, p.Pending)
	assert.Equal(t, "look", p.Message)
	assert.Equal(t, []state.FileView{
		{ID: a.ID, Name: "Screenshot.png", Ext: "png", Size: int64(len(pic)), Mime: "image/png", Width: 64, Height: 48, Staged: true},
		{ID: b.ID, Name: "Notes.TXT", Ext: "txt", Size: 5, Mime: "text/plain", Staged: true},
	}, p.Files, "the local files, previews from /media/<srv>/staged/<id>")
	list, err := f.svc.Attachments(ctx, id, "c-offtopic")
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

func TestSendPostWithOnlyAttachments(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	a, err := f.svc.AddAttachmentPath(ctx, id, "c-offtopic", writeFile(t, "only.txt", []byte("x")))
	require.NoError(t, err)
	other, err := f.svc.AddAttachmentPath(ctx, id, "c-town", writeFile(t, "other.txt", []byte("x")))
	require.NoError(t, err)

	assert.Equal(t, CodeEmptyMessage, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", " ", nil)))
	assert.Equal(t, CodeNotFound, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", "", []string{a.ID, other.ID})), "another channel's")
	assert.Equal(t, CodeNotFound, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", "", []string{"nope"})))
	_, pending := f.pendingPost(id, "c-offtopic")
	assert.False(t, pending, "a refused send shows nothing")
	list, _ := f.svc.Attachments(ctx, id, "c-offtopic")
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
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "a.bin", "", strings.NewReader("aaa"), 0)
	require.NoError(t, err)
	b, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "b.bin", "", strings.NewReader("bbb"), 0)
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
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "a.bin", "", strings.NewReader("aaa"), 0)
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
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "a.bin", "", strings.NewReader("aaa"), 0)
	require.NoError(t, err)
	b, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "b.bin", "", strings.NewReader("bbb"), 0)
	require.NoError(t, err)
	require.NoError(t, f.svc.SendPost(ctx, id, "c-offtopic", "sent as the session dies", []string{a.ID}))
	fake.SetFailure("/api/v4/files", 401)
	fake.SetDown(false)
	require.NoError(t, f.svc.NetworkChanged(ctx))

	f.failedPost(id, "c-offtopic")
	f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "the upload's 401 ends the session")
	assert.Zero(t, fake.Hits("POST", "/api/v4/posts"))
	assert.Equal(t, CodeSessionExpired, codeOf(f.svc.SendPost(ctx, id, "c-offtopic", "", []string{b.ID})), "fails fast")
	list, _ := f.svc.Attachments(ctx, id, "c-offtopic")
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
