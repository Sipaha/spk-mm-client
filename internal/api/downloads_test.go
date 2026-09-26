package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/store"
)

// downloadEvents collects downloads_changed payloads from their own
// subscription (the fixture's channel also carries sync events).
func downloadEvents(t *testing.T, s *Service) func() []map[string]any {
	ch, unsub := s.em.Subscribe()
	t.Cleanup(unsub)
	var mu sync.Mutex
	var got []map[string]any
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case ev := <-ch:
				if ev.Type == EventDownloadsChanged {
					mu.Lock()
					got = append(got, ev.Payload)
					mu.Unlock()
				}
			case <-done:
				return
			}
		}
	}()
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any{}, got...)
	}
}

func downloadsList(t *testing.T, s *Service) []DownloadView {
	t.Helper()
	list, err := s.Downloads(context.Background())
	require.NoError(t, err)
	return list
}

func TestDownloadsAreListedNewestFirst(t *testing.T) {
	f := newChatFixture(t)
	dl := t.TempDir()
	f.svc.getenv = downloadsIn(dl)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	evs := downloadEvents(t, f.svc)
	ctx := context.Background()

	assert.Empty(t, downloadsList(t, f.svc))
	spec, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	log, err := f.svc.OpenFile(ctx, id, "f-log")
	require.NoError(t, err)

	list := downloadsList(t, f.svc)
	require.Len(t, list, 2)
	assert.Equal(t, "server.log", list[0].Name)
	assert.Equal(t, log.Path, list[0].Path)
	d := list[1]
	assert.Equal(t, id, d.ServerID)
	assert.Equal(t, "f-spec", d.FileID)
	assert.Equal(t, "spec.pdf", d.Name)
	assert.Equal(t, spec.Path, d.Path)
	assert.Equal(t, "application/pdf", d.Mime)
	assert.Equal(t, "done", d.State)
	assert.Empty(t, d.Error)
	assert.True(t, d.Exists)
	assert.True(t, d.Openable)
	assert.Equal(t, d.Size, d.Received)
	assert.NotZero(t, d.StartedAt)
	assert.NotZero(t, d.FinishedAt)

	_, err = f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	list = downloadsList(t, f.svc)
	require.Len(t, list, 2, "a file saved this session is not listed twice")
	assert.Equal(t, "spec.pdf", list[0].Name, "it moves to the top")
	assert.Equal(t, d.ID, list[0].ID)

	require.Eventually(t, func() bool {
		var received, done bool
		for _, p := range evs() {
			if p["id"] == d.ID && p["received"] != nil {
				received = true
			}
			if p["id"] == d.ID && p["state"] == "done" {
				done = true
			}
		}
		return received && done
	}, 2*time.Second, 10*time.Millisecond, "progress and the result are pushed to the UI")
}

func TestFailedDownloadIsListedWithItsCode(t *testing.T) {
	f := newChatFixture(t)
	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o644))
	f.svc.getenv = downloadsIn(notADir)
	fake := startFake(t)
	id := f.signIn(fake, "alice")

	_, err := f.svc.DownloadFile(context.Background(), id, "f-spec")
	require.Error(t, err)
	list := downloadsList(t, f.svc)
	require.Len(t, list, 1)
	assert.Equal(t, "failed", list[0].State)
	assert.Equal(t, codeOf(err), list[0].Error)
	assert.False(t, list[0].Exists)
}

func TestSharedDownloadIsOneEntry(t *testing.T) {
	f := newChatFixture(t)
	f.svc.getenv = downloadsIn(t.TempDir())
	f.svc.SetFileOpener((&RecordingOpener{}).Open)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	fake.SetLatency("/api/v4/files/f-log", 300*time.Millisecond)

	var wg sync.WaitGroup
	for _, save := range []func(context.Context, int64, string) (SavedFile, error){f.svc.DownloadFile, f.svc.OpenFile, f.svc.DownloadFile} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := save(context.Background(), id, "f-log")
			assert.NoError(t, err)
		}()
		time.Sleep(20 * time.Millisecond)
	}
	wg.Wait()
	assert.Len(t, downloadsList(t, f.svc), 1)
}

func TestDeletedFileIsNotOpenedOrRevealed(t *testing.T) {
	f := newChatFixture(t)
	f.svc.getenv = downloadsIn(t.TempDir())
	opened := &RecordingOpener{}
	f.svc.SetFileOpener(opened.Open)
	revealed := &RecordingOpener{}
	f.svc.SetRevealer(func(_ context.Context, p string) error { return revealed.Open(p) })
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()

	r, err := f.svc.DownloadFile(ctx, id, "f-log")
	require.NoError(t, err)
	d := downloadsList(t, f.svc)[0]
	require.NoError(t, os.WriteFile(r.Path, []byte("changed"), 0o644))
	assert.False(t, downloadsList(t, f.svc)[0].Exists, "a file of another size is not ours any more")
	require.NoError(t, os.Remove(r.Path))
	assert.False(t, downloadsList(t, f.svc)[0].Exists)

	_, err = f.svc.OpenDownload(ctx, d.ID)
	assert.Equal(t, CodeNoFile, codeOf(err))
	assert.Equal(t, CodeNoFile, codeOf(f.svc.RevealDownload(ctx, d.ID)))
	assert.Empty(t, opened.List())
	assert.Empty(t, revealed.List())

	_, err = f.svc.OpenDownload(ctx, 999)
	assert.Equal(t, CodeNotFound, codeOf(err))
	assert.Equal(t, CodeNotFound, codeOf(f.svc.RevealDownload(ctx, 999)))
}

func TestOpenDownloadOpensSafeTypesOnly(t *testing.T) {
	f := newChatFixture(t)
	f.svc.getenv = downloadsIn(t.TempDir())
	opened := &RecordingOpener{}
	f.svc.SetFileOpener(opened.Open)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()

	p := fake.PostFile("c-offtopic", "bob", "run me", "setup.desktop", "application/x-desktop", []byte("[Desktop Entry]\n"))
	_, err := f.svc.DownloadFile(ctx, id, p.Metadata.Files[0].ID)
	require.NoError(t, err)
	spec, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	list := downloadsList(t, f.svc)
	require.Len(t, list, 2)
	assert.True(t, list[0].Openable)
	assert.False(t, list[1].Openable)

	ok, err := f.svc.OpenDownload(ctx, list[1].ID)
	require.NoError(t, err)
	assert.False(t, ok, "launchers are never opened")
	ok, err = f.svc.OpenDownload(ctx, list[0].ID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []string{spec.Path}, opened.List())
}

func TestRevealFallsBackToOpeningTheFolder(t *testing.T) {
	f := newChatFixture(t)
	dl := t.TempDir()
	f.svc.getenv = downloadsIn(dl)
	opened := &RecordingOpener{}
	f.svc.SetFileOpener(opened.Open)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	r, err := f.svc.DownloadFile(ctx, id, "f-log")
	require.NoError(t, err)
	did := downloadsList(t, f.svc)[0].ID

	var asked []string
	f.svc.SetRevealer(func(_ context.Context, p string) error {
		asked = append(asked, p)
		return nil
	})
	require.NoError(t, f.svc.RevealDownload(ctx, did))
	assert.Equal(t, []string{r.Path}, asked, "the file manager is asked to show the file")
	assert.Empty(t, opened.List())

	f.svc.SetRevealer(func(context.Context, string) error { return errors.New("org.freedesktop.DBus.Error.ServiceUnknown") })
	require.NoError(t, f.svc.RevealDownload(ctx, did))
	assert.Equal(t, []string{dl}, opened.List(), "without a file manager service its folder is opened")

	f.svc.SetRevealer(nil)
	require.NoError(t, f.svc.RevealDownload(ctx, did))
	assert.Equal(t, []string{dl, dl}, opened.List())

	f.svc.SetFileOpener(nil)
	assert.Equal(t, CodeInternal, codeOf(f.svc.RevealDownload(ctx, did)))
}

func TestRemoveAndClearDownloads(t *testing.T) {
	f := newChatFixture(t)
	f.svc.getenv = downloadsIn(t.TempDir())
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	evs := downloadEvents(t, f.svc)
	ctx := context.Background()
	spec, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	_, err = f.svc.DownloadFile(ctx, id, "f-log")
	require.NoError(t, err)
	list := downloadsList(t, f.svc)

	require.NoError(t, f.svc.RemoveDownload(ctx, list[1].ID))
	list = downloadsList(t, f.svc)
	require.Len(t, list, 1)
	assert.Equal(t, "server.log", list[0].Name)
	assert.FileExists(t, spec.Path, "removing an entry keeps the file")

	_, err = f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	list = downloadsList(t, f.svc)
	require.Len(t, list, 2, "asked for again after removal, the saved file is listed anew")
	assert.Equal(t, "spec.pdf", list[0].Name)
	assert.Equal(t, spec.Path, list[0].Path)
	assert.True(t, list[0].Exists)

	n := len(evs())
	require.NoError(t, f.svc.ClearDownloads(ctx))
	assert.Empty(t, downloadsList(t, f.svc))
	require.Eventually(t, func() bool { return len(evs()) > n }, 2*time.Second, 10*time.Millisecond, "the UI hears about it")
}

func TestProgressIsThrottled(t *testing.T) {
	now := time.Unix(100, 0)
	var got []int64
	m := &meter{every: 250 * time.Millisecond, now: func() time.Time { return now }, report: func(n int64) { got = append(got, n) }}
	for i := 0; i < 20; i++ { // 20 chunks of 10 bytes, 50 ms apart: one second
		_, err := m.Write(make([]byte, 10))
		require.NoError(t, err)
		now = now.Add(50 * time.Millisecond)
	}
	assert.Equal(t, []int64{10, 60, 110, 160}, got, "at most ~4 a second, the first at once")
}

func TestSaveIntoReportsProgress(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer ts.Close()
	var got []int64
	_, err := saveInto(context.Background(), rest.New(ts.URL, "", ts.Client()), t.TempDir(), "f1",
		model.FileInfo{ID: "f1", Name: "a.txt", Size: 10}, &meter{now: time.Now, report: func(n int64) { got = append(got, n) }})
	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, int64(10), got[len(got)-1], "every byte is counted")
}

// A remembered entry id must not raise another download's entry after the
// newest one was removed (SQLite would hand its rowid out again).
func TestRemovedNewestEntryIsNotConfusedWithTheNext(t *testing.T) {
	f := newChatFixture(t)
	f.svc.getenv = downloadsIn(t.TempDir())
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	_, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	require.NoError(t, f.svc.RemoveDownload(ctx, downloadsList(t, f.svc)[0].ID))
	logFile, err := f.svc.DownloadFile(ctx, id, "f-log")
	require.NoError(t, err)
	_, err = f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)

	list := downloadsList(t, f.svc)
	require.Len(t, list, 2)
	assert.Equal(t, "spec.pdf", list[0].Name)
	assert.Equal(t, "server.log", list[1].Name, "the other entry is untouched")
	assert.Equal(t, logFile.Path, list[1].Path)
	assert.NotEqual(t, list[0].ID, list[1].ID)
}

func TestListedPathMustBeARegularAbsoluteFile(t *testing.T) {
	f := newChatFixture(t)
	f.svc.getenv = downloadsIn(t.TempDir())
	opened := &RecordingOpener{}
	f.svc.SetFileOpener(opened.Open)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	r, err := f.svc.DownloadFile(ctx, id, "f-log")
	require.NoError(t, err)
	d := downloadsList(t, f.svc)[0]

	elsewhere := filepath.Join(t.TempDir(), "same-size")
	data, err := os.ReadFile(r.Path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(elsewhere, data, 0o644))
	require.NoError(t, os.Remove(r.Path))
	require.NoError(t, os.Symlink(elsewhere, r.Path))
	assert.False(t, downloadsList(t, f.svc)[0].Exists, "a symlink in the file's place is not the file")
	_, err = f.svc.OpenDownload(ctx, d.ID)
	assert.Equal(t, CodeNoFile, codeOf(err))

	rel, err := f.st.AddDownload(ctx, store.Download{ServerID: id, FileID: "f-x", Name: "x.log", Size: int64(len(data)), StartedAt: 1})
	require.NoError(t, err)
	wd, err := os.Getwd()
	require.NoError(t, err)
	relPath, err := filepath.Rel(wd, elsewhere)
	require.NoError(t, err)
	require.NoError(t, f.st.FinishDownload(ctx, rel, relPath, "", 2))
	for _, v := range downloadsList(t, f.svc) {
		if v.ID == rel {
			assert.False(t, v.Exists, "a relative path from the DB is never trusted")
		}
	}
	_, err = f.svc.OpenDownload(ctx, rel)
	assert.Equal(t, CodeNoFile, codeOf(err))
	assert.Equal(t, CodeNoFile, codeOf(f.svc.RevealDownload(ctx, rel)))
	assert.Empty(t, opened.List())
}

func TestFailureBeforeTheDownloadIsListed(t *testing.T) {
	f := newChatFixture(t)
	f.svc.getenv = downloadsIn(t.TempDir())
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	_, err := f.svc.DownloadFile(context.Background(), id, "f-nope")
	require.Equal(t, CodeNoFile, codeOf(err))
	list := downloadsList(t, f.svc)
	require.Len(t, list, 1)
	assert.Equal(t, "failed", list[0].State)
	assert.Equal(t, CodeNoFile, list[0].Error)
	assert.Equal(t, "f-nope", list[0].Name, "the name is unknown: the file id stands in")
	assert.Equal(t, "f-nope", list[0].FileID)

	f.svc.getenv = func(string) string { return "" }
	t.Setenv("HOME", "")
	xdgPrev := xdg.UserDirs.Download
	t.Cleanup(func() { xdg.UserDirs.Download = xdgPrev })
	xdg.UserDirs.Download = ""
	_, err = f.svc.DownloadFile(context.Background(), id, "f-spec")
	require.Equal(t, CodeInternal, codeOf(err))
	list = downloadsList(t, f.svc)
	require.Len(t, list, 2)
	assert.Equal(t, "spec.pdf", list[0].Name, "no downloads folder: listed with its name")
	assert.Equal(t, "failed", list[0].State)
	assert.Equal(t, CodeInternal, list[0].Error)
}
