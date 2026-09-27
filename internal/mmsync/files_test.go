package mmsync

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mmfake"
	"github.com/spk/spk-mm-client/internal/state"
)

var errUploadFailed = errors.New("upload failed")

// fakeFiles stands in for the attachment store: Wait blocks until the test
// opens the gate (or fails the uploads); a released id is gone.
type fakeFiles struct {
	mu       sync.Mutex
	fileIDs  map[string]string // attachment id → server file id
	open     bool
	fail     error
	released []string
	waits    int
	retried  [][]string
	changed  chan struct{}
}

func newFakeFiles() *fakeFiles {
	return &fakeFiles{fileIDs: map[string]string{}, changed: make(chan struct{})}
}

func (f *fakeFiles) broadcastLocked() {
	close(f.changed)
	f.changed = make(chan struct{})
}

func (f *fakeFiles) Wait(ctx context.Context, ids []string) ([]string, error) {
	f.mu.Lock()
	f.waits++
	f.mu.Unlock()
	for {
		f.mu.Lock()
		var out []string
		var err error
		for _, id := range ids {
			if slices.Contains(f.released, id) {
				err = errors.New("gone")
			}
			out = append(out, f.fileIDs[id])
		}
		if err == nil {
			err = f.fail
		}
		open, changed := f.open, f.changed
		f.mu.Unlock()
		switch {
		case err != nil:
			return nil, err
		case open:
			return out, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (f *fakeFiles) Retry(ids []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retried = append(f.retried, slices.Clone(ids))
	f.broadcastLocked()
}

func (f *fakeFiles) Release(ids []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, ids...)
	f.broadcastLocked()
}

func (f *fakeFiles) set(fn func(*fakeFiles)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
	f.broadcastLocked()
}

func (f *fakeFiles) get() (released []string, retried [][]string, waits int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.released), slices.Clone(f.retried), f.waits
}

func filesHarness(t *testing.T) (*harness, *fakeFiles) {
	h := newHarness(t, mmfake.Options{})
	files := newFakeFiles()
	h.tune = func(c *Config) { c.Files = files }
	h.start()
	h.live()
	h.eventually(h.allLoaded, "prefetch")
	return h, files
}

// upload puts a file on the fake as alice and returns its id.
func (h *harness) upload(name string) string {
	h.t.Helper()
	info, err := h.w.REST().UploadFile(context.Background(), "c-offtopic", name, "", strings.NewReader("abc"), 3, nil)
	require.NoError(h.t, err)
	return info.ID
}

func staged(ids ...string) []state.FileView {
	var out []state.FileView
	for _, id := range ids {
		out = append(out, state.FileView{ID: id, Name: id + ".txt", Ext: "txt", Size: 3, Mime: "text/plain", Staged: true})
	}
	return out
}

func (h *harness) pending(ch string) (state.PostView, bool) {
	for _, p := range h.view(ch).Posts {
		if p.PendingPostID == p.ID {
			return p, true
		}
	}
	return state.PostView{}, false
}

func TestSendWithFilesWaitsForTheUploadsThenCreates(t *testing.T) {
	h, files := filesHarness(t)
	f1, f2 := h.upload("a.txt"), h.upload("b.txt")
	files.set(func(f *fakeFiles) { f.fileIDs["a1"], f.fileIDs["a2"] = f1, f2 })

	assert.ErrorIs(t, h.w.Send("c-offtopic", "  "), ErrEmptyMessage, "nothing to send")
	require.NoError(t, h.w.Send("c-offtopic", "", staged("a1", "a2")...), "files without text")
	p, ok := h.pending("c-offtopic")
	require.True(t, ok, "shown at once")
	assert.True(t, p.Pending)
	assert.Equal(t, staged("a1", "a2"), p.Files, "with the local files")
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, h.fake.Hits("POST", "/api/v4/posts"), "not created while the uploads run")
	_, stillPending := h.pending("c-offtopic")
	assert.True(t, stillPending, "and not failed by the create timeout's clock either")

	files.set(func(f *fakeFiles) { f.open = true })
	var got state.PostView
	h.eventually(func() bool {
		for _, x := range h.view("c-offtopic").Posts {
			if x.PendingPostID == p.ID && x.ID != p.ID {
				got = x
				return true
			}
		}
		return false
	}, "the post is never confirmed")
	require.Len(t, got.Files, 2)
	assert.Equal(t, []string{f1, f2}, []string{got.Files[0].ID, got.Files[1].ID}, "the server's files")
	assert.False(t, got.Files[0].Staged)
	h.eventually(func() bool {
		released, _, _ := files.get()
		return slices.Equal(released, []string{"a1", "a2"})
	}, "the attachments are let go of once the server has the post")
	visible := h.fake.VisiblePosts("c-offtopic")
	assert.Equal(t, []string{f1, f2}, []string(visible[len(visible)-1].FileIDs))
	assert.Equal(t, 1, h.fake.Hits("POST", "/api/v4/posts"))
}

func TestFailedUploadFailsThePostAndRetryUploadsAgain(t *testing.T) {
	h, files := filesHarness(t)
	f1 := h.upload("a.txt")
	files.set(func(f *fakeFiles) { f.fileIDs["a1"], f.fail = f1, errUploadFailed })
	require.NoError(t, h.w.Send("c-offtopic", "with a file", staged("a1")...))
	var p state.PostView
	h.eventually(func() bool {
		var ok bool
		p, ok = h.pending("c-offtopic")
		return ok && p.Failed
	}, "a failed upload fails the post")
	assert.Zero(t, h.fake.Hits("POST", "/api/v4/posts"))
	_, retried, _ := files.get()
	assert.Equal(t, [][]string{{"a1"}}, retried, "sending is the user asking again for a failed upload")

	files.set(func(f *fakeFiles) { f.fail, f.open = nil, true }) // the next upload works
	h.w.Retry("c-offtopic", p.ID)
	h.eventually(func() bool { return h.hasMessage("c-offtopic", "with a file") }, "retry sends")
	_, retried, _ = files.get()
	assert.Equal(t, [][]string{{"a1"}, {"a1"}}, retried, "retry asks the store to upload the failed ones again")
	assert.Equal(t, 1, h.fake.Hits("POST", "/api/v4/posts"))
}

func TestDiscardLetsTheFilesGo(t *testing.T) {
	h, files := filesHarness(t)
	require.NoError(t, h.w.Send("c-offtopic", "never mind", staged("a1", "a2")...))
	p, ok := h.pending("c-offtopic")
	require.True(t, ok)
	h.eventually(func() bool { _, _, w := files.get(); return w == 1 }, "waiting for the uploads")
	h.w.Discard("c-offtopic", p.ID)
	released, _, _ := files.get()
	assert.Equal(t, []string{"a1", "a2"}, released, "uploads cancelled, spools deleted")
	_, ok = h.pending("c-offtopic")
	assert.False(t, ok)
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, h.fake.Hits("POST", "/api/v4/posts"))
}

func TestStoppingWorkerLetsTheFilesOfItsPendingPostsGo(t *testing.T) {
	h, files := filesHarness(t)
	require.NoError(t, h.w.Send("c-offtopic", "waiting", staged("a1")...))
	files.set(func(f *fakeFiles) { f.fail = errUploadFailed })
	h.eventually(func() bool { p, ok := h.pending("c-offtopic"); return ok && p.Failed }, "failed")
	files.set(func(f *fakeFiles) { f.fail = nil })
	require.NoError(t, h.w.Send("c-offtopic", "still waiting", staged("a2")...))
	h.stop()
	released, _, _ := files.get()
	assert.ElementsMatch(t, []string{"a1", "a2"}, released, "pending posts die with the worker, so do their files")

	require.NoError(t, h.w.Send("c-offtopic", "after stop", staged("a3")...))
	released, _, _ = files.get()
	assert.Contains(t, released, "a3", "refused work lets its files go too")
}
