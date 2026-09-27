package api

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/attach"
	"github.com/spk/spk-mm-client/internal/events"
	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mmfake"
)

// withAttachments turns attachments on with a spool dir of the test's own.
func (f *chatFixture) withAttachments() string {
	f.t.Helper()
	dir := filepath.Join(f.t.TempDir(), "tmp")
	f.svc.EnableAttachments(dir)
	return dir
}

func (f *chatFixture) live(fake *mmfake.Server) int64 {
	f.t.Helper()
	id := f.signIn(fake, "alice")
	f.eventually(func() bool { return f.server(id).State == "live" && f.loaded(id, "c-offtopic") }, "never live")
	// Before the fake closes (cleanups run in reverse): an upload still
	// running would keep it waiting.
	f.t.Cleanup(func() { f.svc.att.Close() })
	return id
}

// offline takes the server down until the test brings it back.
func (f *chatFixture) offline(fake *mmfake.Server, id int64) {
	f.t.Helper()
	fake.SetDown(true)
	fake.DropConnections(false)
	f.eventually(func() bool { return f.server(id).State == "reconnecting" }, "never lost the connection")
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

func (f *chatFixture) attachment(id int64, ch, attID string) (AttachmentView, bool) {
	list, err := f.svc.Attachments(context.Background(), id, ch)
	require.NoError(f.t, err)
	for _, a := range list {
		if a.ID == attID {
			return a, true
		}
	}
	return AttachmentView{}, false
}

func (f *chatFixture) attachmentState(id int64, ch, attID string, want attach.State) AttachmentView {
	f.t.Helper()
	var a AttachmentView
	f.eventually(func() bool {
		var ok bool
		a, ok = f.attachment(id, ch, attID)
		return ok && a.State == want
	}, fmt.Sprintf("attachment never %s (now %+v)", want, a))
	return a
}

// attachmentEvents collects attachments_changed payloads until one says
// done(item) about attID.
func (f *chatFixture) attachmentEvents(attID string, done func(AttachmentView) bool) []AttachmentView {
	f.t.Helper()
	var seen []AttachmentView
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-f.evs:
			if ev.Type != EventAttachmentsChanged {
				continue
			}
			for _, a := range ev.Payload["items"].([]AttachmentView) {
				if a.ID == attID {
					seen = append(seen, a)
					if done(a) {
						return seen
					}
				}
			}
		case <-timeout:
			f.t.Fatalf("attachment %s never done; saw %+v", attID, seen)
		}
	}
}

func TestAttachmentIsUploadedWithProgressEvents(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	// ~1 s for the file below: more than loopback socket buffers take in
	// at once, so the client sees the server reading it.
	fake.SetUploadThrottle(6 << 20)
	data := bytes.Repeat([]byte("0123456789abcdef"), 6<<16)
	a, err := f.svc.AddAttachmentPath(ctx, id, "c-offtopic", writeFile(t, "report.txt", data))
	require.NoError(t, err)
	assert.Equal(t, "report.txt", a.Name)
	assert.Equal(t, int64(len(data)), a.Size)

	seen := f.attachmentEvents(a.ID, func(x AttachmentView) bool { return x.State == attach.StateUploaded })
	var mid bool
	for _, x := range seen {
		mid = mid || (x.State == attach.StateUploading && x.Sent > 0 && x.Sent < x.Size)
	}
	assert.True(t, mid, "progress mid-way was reported: %+v", seen)
	assert.Less(t, len(seen), 20, "coalesced and throttled")

	done, ok := f.attachment(id, "c-offtopic", a.ID)
	require.True(t, ok)
	assert.Equal(t, int64(len(data)), done.Sent)
	info, err := f.svc.manager().Worker(id).REST().FileInfo(ctx, done.FileID)
	require.NoError(t, err)
	assert.Equal(t, "report.txt", info.Name)
	assert.Equal(t, int64(len(data)), info.Size)
	assert.Equal(t, 1, fake.Hits("POST", "/api/v4/files"))
}

func TestAttachmentEventCarriesTheChannelsItems(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	a, err := f.svc.AddAttachmentBytes(context.Background(), id, "c-offtopic", "note.txt", "text/plain", strings.NewReader("hi"), 0)
	require.NoError(t, err)
	for {
		ev := f.nextEvent(EventAttachmentsChanged)
		assert.Equal(t, id, ev.Payload["server_id"])
		assert.Equal(t, "c-offtopic", ev.Payload["channel_id"])
		items := ev.Payload["items"].([]AttachmentView)
		require.Len(t, items, 1)
		assert.Equal(t, a.ID, items[0].ID)
		if items[0].State == attach.StateUploaded {
			break
		}
	}
}

func (f *chatFixture) nextEvent(typ string) events.Event {
	f.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-f.evs:
			if ev.Type == typ {
				return ev
			}
		case <-timeout:
			f.t.Fatalf("no %s event", typ)
		}
	}
}

func TestAttachmentWaitsUntilTheServerIsLive(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	f.offline(fake, id)
	a, err := f.svc.AddAttachmentPath(context.Background(), id, "c-offtopic", writeFile(t, "x.txt", []byte("abc")))
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	got, _ := f.attachment(id, "c-offtopic", a.ID)
	assert.Equal(t, attach.StateStaged, got.State)
	assert.Zero(t, fake.Hits("POST", "/api/v4/files"))
	fake.SetDown(false)
	require.NoError(t, f.svc.NetworkChanged(context.Background()))
	f.attachmentState(id, "c-offtopic", a.ID, attach.StateUploaded)
}

func TestAttachmentUnauthorizedAsksForSignIn(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	fake.SetFailure("/api/v4/files", http.StatusUnauthorized) // only the upload sees the 401
	a, err := f.svc.AddAttachmentPath(context.Background(), id, "c-offtopic", writeFile(t, "x.txt", []byte("abc")))
	require.NoError(t, err)
	got := f.attachmentState(id, "c-offtopic", a.ID, attach.StateFailed)
	assert.Equal(t, CodeSessionExpired, got.Error)
	f.eventually(func() bool { return f.server(id).State == "needs_reauth" }, "the 401 did not end the session")
	assert.Equal(t, 1, fake.Hits("POST", "/api/v4/files"), "not retried")
	assert.Equal(t, CodeSessionExpired, codeOf(f.svc.RetryAttachment(context.Background(), id, a.ID)))
	_, err = f.svc.AddAttachmentPath(context.Background(), id, "c-offtopic", writeFile(t, "y.txt", []byte("abc")))
	assert.Equal(t, CodeSessionExpired, codeOf(err), "adding fails fast like other writes")
}

func TestAttachmentMethodsCheckServerChannelAndLimits(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := mmfake.Start(mmfake.Options{MaxFileSize: 10})
	t.Cleanup(fake.Close)
	id := f.live(fake)
	ctx := context.Background()
	f.eventually(func() bool { return f.svc.manager().Worker(id).State().MaxFileSize() == 10 }, "limits not read")

	_, err := f.svc.AddAttachmentPath(ctx, id, "c-offtopic", writeFile(t, "big.txt", []byte("01234567890")))
	assert.Equal(t, CodeTooLarge, codeOf(err))
	_, err = f.svc.AddAttachmentPath(ctx, id, "c-offtopic", t.TempDir())
	assert.Equal(t, CodeNotAFile, codeOf(err))
	_, err = f.svc.AddAttachmentPath(ctx, id, "c-nope", writeFile(t, "x.txt", []byte("x")))
	assert.Equal(t, CodeNoChannel, codeOf(err))
	_, err = f.svc.AddAttachmentPath(ctx, 999, "c-offtopic", writeFile(t, "x.txt", []byte("x")))
	assert.Equal(t, CodeNotFound, codeOf(err))
	_, err = f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "big.bin", "", strings.NewReader("01234567890"), 0)
	assert.Equal(t, CodeTooLarge, codeOf(err))
	_, err = f.svc.AddAttachmentPath(ctx, id, "c-offtopic", writeFile(t, "empty.txt", nil))
	assert.Equal(t, CodeEmptyFile, codeOf(err))
	var ids []string
	for i := 0; i < attach.MaxPerChannel; i++ {
		a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "x.txt", "", strings.NewReader("x"), 0)
		require.NoError(t, err)
		ids = append(ids, a.ID)
	}
	_, err = f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "x.txt", "", strings.NewReader("x"), 0)
	assert.Equal(t, CodeTooMany, codeOf(err))

	list, err := f.svc.Attachments(ctx, id, "c-offtopic")
	require.NoError(t, err)
	assert.Len(t, list, attach.MaxPerChannel)
	assert.Equal(t, CodeNotFound, codeOf(f.svc.RemoveAttachment(ctx, 999, ids[0])), "another server's attachment")
	require.NoError(t, f.svc.RemoveAttachment(ctx, id, ids[0]))
	assert.Equal(t, CodeNotFound, codeOf(f.svc.RemoveAttachment(ctx, id, ids[0])))
	assert.Equal(t, CodeNotFound, codeOf(f.svc.RetryAttachment(ctx, id, ids[0])))
	list, _ = f.svc.Attachments(ctx, id, "c-offtopic")
	assert.Len(t, list, attach.MaxPerChannel-1)
}

func TestAttachmentsDisabledOnTheServer(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := mmfake.Start(mmfake.Options{DisableFileAttachments: true})
	t.Cleanup(fake.Close)
	id := f.live(fake)
	_, err := f.svc.AddAttachmentPath(context.Background(), id, "c-offtopic", writeFile(t, "x.txt", []byte("x")))
	assert.Equal(t, CodeAttachmentsDisabled, codeOf(err))
}

func TestFailedAttachmentIsRetriedByTheUser(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	fake.FailUploads(1)
	a, err := f.svc.AddAttachmentPath(ctx, id, "c-offtopic", writeFile(t, "x.txt", []byte("abc")))
	require.NoError(t, err)
	assert.Equal(t, CodeUnreachable, f.attachmentState(id, "c-offtopic", a.ID, attach.StateFailed).Error, "a 5xx")
	require.NoError(t, f.svc.RetryAttachment(ctx, id, a.ID))
	f.attachmentState(id, "c-offtopic", a.ID, attach.StateUploaded)
	assert.Equal(t, 2, fake.Hits("POST", "/api/v4/files"))
}

func TestSignOutDropsTheServersAttachments(t *testing.T) {
	f := newChatFixture(t)
	dir := f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	f.offline(fake, id) // stays staged
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "x.bin", "", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	require.NoError(t, f.svc.Logout(ctx, id))
	_, ok := f.svc.att.Get(a.ID)
	assert.False(t, ok)
	des, _ := os.ReadDir(dir)
	assert.Empty(t, des, "its spool is deleted")
}

func TestServiceCloseDeletesSpools(t *testing.T) {
	f := newChatFixture(t)
	dir := f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	f.offline(fake, id)
	_, err := f.svc.AddAttachmentBytes(context.Background(), id, "c-offtopic", "x.bin", "", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	des, _ := os.ReadDir(dir)
	require.Len(t, des, 1)
	f.svc.Close()
	des, _ = os.ReadDir(dir)
	assert.Empty(t, des)
}

func TestStagedPicturesAreServedByTheMediaCache(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	var pic bytes.Buffer
	require.NoError(t, png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 3, 2))))
	img, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "Screenshot.png", "image/png", bytes.NewReader(pic.Bytes()), 0)
	require.NoError(t, err)
	txt, err := f.svc.AddAttachmentPath(ctx, id, "c-offtopic", writeFile(t, "notes.txt", []byte("hello")))
	require.NoError(t, err)

	mc, err := media.New(media.Options{Dir: t.TempDir(), Origin: f.svc, Staged: f.svc})
	require.NoError(t, err)
	ts := httptest.NewServer(mc)
	defer ts.Close()
	get := func(path string) (int, []byte) {
		resp, err := http.Get(ts.URL + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	code, body := get(fmt.Sprintf("/media/%d/staged/%s", id, img.ID))
	assert.Equal(t, 200, code)
	assert.Equal(t, pic.Bytes(), body)
	code, _ = get(fmt.Sprintf("/media/%d/staged/%s", id, txt.ID))
	assert.Equal(t, 404, code, "not a picture")
	code, _ = get(fmt.Sprintf("/media/%d/staged/%s", id+1, img.ID))
	assert.Equal(t, 404, code, "another server's key")
}

func TestSignInAsAnotherUserDropsTheAttachments(t *testing.T) {
	f := newChatFixture(t)
	dir := f.withAttachments()
	fake := startFake(t)
	id := f.live(fake)
	ctx := context.Background()
	fake.FailUploads(2)
	a, err := f.svc.AddAttachmentBytes(ctx, id, "c-offtopic", "x.bin", "", strings.NewReader("abc"), 0)
	require.NoError(t, err)
	f.attachmentState(id, "c-offtopic", a.ID, attach.StateFailed)

	// The same user again: kept.
	_, err = f.svc.LoginWithPassword(ctx, id, "alice", "secret")
	require.NoError(t, err)
	_, ok := f.svc.att.Get(a.ID)
	assert.True(t, ok, "alice's own attachment stays")

	// Another user: alice's files must not be sent as bob's.
	_, err = f.svc.LoginWithPassword(ctx, id, "bob", "secret")
	require.NoError(t, err)
	_, ok = f.svc.att.Get(a.ID)
	assert.False(t, ok, "alice's attachment is gone")
	des, _ := os.ReadDir(dir)
	assert.Empty(t, des, "and its spool")
}
