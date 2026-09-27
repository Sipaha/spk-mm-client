package api

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/attach"
)

func TestParseURIListKeepsLocalFilesOnly(t *testing.T) {
	list := "# a comment\r\n" +
		"file:///home/u/note.txt\r\n" +
		"file:///home/u/%D0%B8%D0%BC%D1%8F%20%D1%81%20%D0%BF%D1%80%D0%BE%D0%B1%D0%B5%D0%BB%D0%BE%D0%BC.png\r\n" +
		"https://example.com/a.png\r\n" +
		"file://localhost/tmp/a%23b.txt\n" +
		"file://otherhost/share/x.txt\n" +
		"file:relative.txt\n" +
		"file:///bad%00name\n" +
		"\n" +
		"  file:///home/u/spaced.txt  \n"
	assert.Equal(t, []string{
		"/home/u/note.txt",
		"/home/u/имя с пробелом.png",
		"/tmp/a#b.txt",
		"/home/u/spaced.txt",
	}, parseURIList(list))
	assert.Empty(t, parseURIList(""))
	assert.Empty(t, parseURIList("https://example.com/a\r\n"))
}

func TestParseGnomeCopiedFilesTreatsCutAsCopy(t *testing.T) {
	assert.Equal(t, []string{"/a/one.txt", "/a/two words.txt"},
		parseGnomeCopiedFiles("copy\nfile:///a/one.txt\nfile:///a/two%20words.txt"))
	assert.Equal(t, []string{"/a/one.txt"}, parseGnomeCopiedFiles("cut\nfile:///a/one.txt\n"))
	assert.Equal(t, []string{"/a/one.txt"}, parseGnomeCopiedFiles("file:///a/one.txt"), "no verb line")
	assert.Empty(t, parseGnomeCopiedFiles("copy\n"))
	assert.Empty(t, parseGnomeCopiedFiles("copy\nsftp://host/x"))
}

// fakeClipboard offers fixed targets; ImagePNG converts the "image/*" one
// (the GTK one converts with gdk-pixbuf).
type fakeClipboard struct {
	mu      sync.Mutex
	data    map[string][]byte
	hang    bool // every request waits for ctx
	asked   []string
	closed  int
	opened  int
	pngFrom string // target ImagePNG "converted"
}

type countingCloser struct {
	io.Reader
	c *fakeClipboard
}

func (r countingCloser) Close() error {
	r.c.mu.Lock()
	r.c.closed++
	r.c.mu.Unlock()
	return nil
}

func (c *fakeClipboard) ask(ctx context.Context, what string) error {
	c.mu.Lock()
	c.asked = append(c.asked, what)
	hang := c.hang
	c.mu.Unlock()
	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (c *fakeClipboard) reader(b []byte) io.ReadCloser {
	c.mu.Lock()
	c.opened++
	c.mu.Unlock()
	return countingCloser{Reader: bytes.NewReader(b), c: c}
}

func (c *fakeClipboard) Targets(ctx context.Context) ([]string, error) {
	if err := c.ask(ctx, "TARGETS"); err != nil {
		return nil, err
	}
	var out []string
	for t := range c.data {
		out = append(out, t)
	}
	return append(out, "TIMESTAMP", "TARGETS"), nil
}

func (c *fakeClipboard) Contents(ctx context.Context, target string) (io.ReadCloser, error) {
	if err := c.ask(ctx, target); err != nil {
		return nil, err
	}
	b, ok := c.data[target]
	if !ok {
		return nil, errors.New("no such target")
	}
	return c.reader(b), nil
}

func (c *fakeClipboard) ImagePNG(ctx context.Context) (io.ReadCloser, error) {
	if err := c.ask(ctx, "image"); err != nil {
		return nil, err
	}
	for t := range c.data {
		if strings.HasPrefix(t, "image/") {
			c.pngFrom = t
			return c.reader(tinyPNG()), nil
		}
	}
	return nil, errors.New("no image")
}

func (c *fakeClipboard) requests() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.asked...)
}

func tinyPNG() []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 2)))
	return buf.Bytes()
}

func fileURI(p string) string {
	return "file://" + strings.ReplaceAll(strings.ReplaceAll(p, "%", "%25"), " ", "%20")
}

func names(list []AttachmentView) []string {
	out := []string{}
	for _, a := range list {
		out = append(out, a.Name)
	}
	return out
}

func TestClipboardFileListIsAttachedByPath(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	dir := t.TempDir()
	a := filepath.Join(dir, "note.txt")
	b := filepath.Join(dir, "имя с пробелом.png")
	require.NoError(t, os.WriteFile(a, []byte("note"), 0o600))
	require.NoError(t, os.WriteFile(b, tinyPNG(), 0o600))
	cb := &fakeClipboard{data: map[string][]byte{
		"text/uri-list":                []byte(fileURI(a) + "\r\n" + fileURI(b) + "\r\n"),
		"x-special/gnome-copied-files": []byte("copy\n" + fileURI(a)),
		"image/png":                    tinyPNG(), // a file list wins over an image
		"text/plain":                   []byte(a + "\n" + b),
	}}
	f.svc.SetClipboard(cb)

	n, err := f.svc.AttachFromClipboard(context.Background(), id, "c-offtopic")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	list, err := f.svc.Attachments(context.Background(), id, "c-offtopic")
	require.NoError(t, err)
	assert.Equal(t, []string{"note.txt", "имя с пробелом.png"}, names(list))
	assert.Equal(t, []string{"TARGETS", "text/uri-list"}, cb.requests())
	assert.Equal(t, cb.opened, cb.closed, "every reader closed")
}

func TestClipboardGnomeCopiedFilesWithoutURIList(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	p := writeFile(t, "cut.txt", []byte("cut me"))
	cb := &fakeClipboard{data: map[string][]byte{
		"x-special/gnome-copied-files": []byte("cut\n" + fileURI(p) + "\n"),
		"UTF8_STRING":                  []byte(p),
	}}
	f.svc.SetClipboard(cb)

	n, err := f.svc.AttachFromClipboard(context.Background(), id, "c-offtopic")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	list, _ := f.svc.Attachments(context.Background(), id, "c-offtopic")
	assert.Equal(t, []string{"cut.txt"}, names(list))
}

var screenshotName = regexp.MustCompile(`^Screenshot \d{4}-\d{2}-\d{2} \d{2}-\d{2}-\d{2}\.png$`)

func TestClipboardPNGIsSpooledAsAScreenshot(t *testing.T) {
	f := newChatFixture(t)
	dir := f.withAttachments()
	id := f.live(startFake(t))
	cb := &fakeClipboard{data: map[string][]byte{
		"text/uri-list": []byte("https://example.com/pic.png\r\n"), // a link: not a file
		"image/png":     tinyPNG(),
		"image/bmp":     []byte("BM"),
	}}
	f.svc.SetClipboard(cb)

	n, err := f.svc.AttachFromClipboard(context.Background(), id, "c-offtopic")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	list, _ := f.svc.Attachments(context.Background(), id, "c-offtopic")
	require.Len(t, list, 1)
	assert.Regexp(t, screenshotName, list[0].Name)
	assert.Equal(t, "image/png", list[0].Mime)
	assert.Equal(t, int64(len(tinyPNG())), list[0].Size)
	assert.Equal(t, []string{"TARGETS", "text/uri-list", "image/png"}, cb.requests(), "PNG taken as is")
	spools, _ := filepath.Glob(filepath.Join(dir, "attach-*"))
	assert.Len(t, spools, 1)
	assert.Equal(t, cb.opened, cb.closed)
}

func TestClipboardOtherImageIsConvertedToPNG(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	cb := &fakeClipboard{data: map[string][]byte{"image/jpeg": []byte("\xff\xd8\xff"), "text/html": []byte("<img>")}}
	f.svc.SetClipboard(cb)

	n, err := f.svc.AttachFromClipboard(context.Background(), id, "c-offtopic")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "image/jpeg", cb.pngFrom)
	assert.Equal(t, []string{"TARGETS", "image"}, cb.requests())
	list, _ := f.svc.Attachments(context.Background(), id, "c-offtopic")
	require.Len(t, list, 1)
	assert.Regexp(t, screenshotName, list[0].Name)
	assert.Equal(t, "image/png", list[0].Mime)
}

func TestClipboardTextAttachesNothing(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	cb := &fakeClipboard{data: map[string][]byte{"UTF8_STRING": []byte("hi"), "text/plain": []byte("hi")}}
	f.svc.SetClipboard(cb)

	n, err := f.svc.AttachFromClipboard(context.Background(), id, "c-offtopic")
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, []string{"TARGETS"}, cb.requests())
}

func TestClipboardFolderIsRefusedOthersAttached(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	p := writeFile(t, "ok.txt", []byte("ok"))
	cb := &fakeClipboard{data: map[string][]byte{
		"text/uri-list": []byte(fileURI(t.TempDir()) + "\r\n" + fileURI(p) + "\r\n"),
	}}
	f.svc.SetClipboard(cb)

	n, err := f.svc.AttachFromClipboard(context.Background(), id, "c-offtopic")
	assert.Equal(t, 1, n)
	var ce *CodedError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CodeNotAFile, ce.Code)
	list, _ := f.svc.Attachments(context.Background(), id, "c-offtopic")
	assert.Equal(t, []string{"ok.txt"}, names(list))
}

// A clipboard owner that never answers blocks nothing: the request gives
// up after clipboardTimeout.
func TestHungClipboardOwnerTimesOut(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	defer func(d time.Duration) { clipboardTimeout = d }(clipboardTimeout)
	clipboardTimeout = 100 * time.Millisecond
	f.svc.SetClipboard(&fakeClipboard{hang: true})

	start := time.Now()
	n, err := f.svc.AttachFromClipboard(context.Background(), id, "c-offtopic")
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Zero(t, n)
	var ce *CodedError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CodeClipboardFailed, ce.Code)
}

func TestNoClipboardOrPickerIsUnsupported(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	for _, fn := range []func(context.Context, int64, string) (int, error){f.svc.AttachFromClipboard, f.svc.PickAttachments} {
		n, err := fn(context.Background(), id, "c-offtopic")
		assert.Zero(t, n)
		var ce *CodedError
		require.ErrorAs(t, err, &ce)
		assert.Equal(t, CodeUnsupported, ce.Code)
	}
}

type fakePicker struct {
	paths []string
	err   error
	calls int
}

func (p *fakePicker) PickFiles(context.Context) ([]string, error) {
	p.calls++
	return p.paths, p.err
}

func TestPickAttachmentsAttachesTheChosenFiles(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	a := writeFile(t, "a.txt", []byte("a"))
	b := writeFile(t, "b.pdf", []byte("%PDF-1.4"))
	p := &fakePicker{paths: []string{a, b}}
	f.svc.SetFilePicker(p)

	n, err := f.svc.PickAttachments(context.Background(), id, "c-offtopic")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	list, _ := f.svc.Attachments(context.Background(), id, "c-offtopic")
	assert.Equal(t, []string{"a.txt", "b.pdf"}, names(list))

	p.paths = nil // cancelled
	n, err = f.svc.PickAttachments(context.Background(), id, "c-offtopic")
	require.NoError(t, err)
	assert.Zero(t, n)
}

// The dialog is not opened when nothing could be attached anyway.
func TestPickAttachmentsChecksBeforeTheDialog(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	p := &fakePicker{}
	f.svc.SetFilePicker(p)

	_, err := f.svc.PickAttachments(context.Background(), id, "c-nope")
	var ce *CodedError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CodeNoChannel, ce.Code)

	for i := range attach.MaxPerChannel {
		_, err := f.svc.AddAttachmentBytes(context.Background(), id, "c-offtopic", "n.txt", "", strings.NewReader(strings.Repeat("x", i+1)), 0)
		require.NoError(t, err)
	}
	_, err = f.svc.PickAttachments(context.Background(), id, "c-offtopic")
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CodeTooMany, ce.Code)
	assert.Zero(t, p.calls)
}

func TestDroppedFilesAreAttachedAndRefusalsReported(t *testing.T) {
	f := newChatFixture(t)
	f.withAttachments()
	id := f.live(startFake(t))
	a := writeFile(t, "dropped.txt", []byte("d"))

	assert.Equal(t, 1, f.svc.AttachDropped(context.Background(), id, "c-offtopic", []string{a}))
	list, _ := f.svc.Attachments(context.Background(), id, "c-offtopic")
	assert.Equal(t, []string{"dropped.txt"}, names(list))

	// A folder (or anything but a regular file) is refused with a message.
	assert.Zero(t, f.svc.AttachDropped(context.Background(), id, "c-offtopic", []string{t.TempDir()}))
	ev := f.nextEvent(EventAttachmentRefused)
	assert.Equal(t, map[string]any{"server_id": id, "channel_id": "c-offtopic", "code": CodeNotAFile}, ev.Payload)
}
