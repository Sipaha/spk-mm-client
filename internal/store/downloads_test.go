package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func addDownload(t *testing.T, st *Store, name string, at int64) int64 {
	t.Helper()
	id, err := st.AddDownload(context.Background(), Download{ServerID: 1, FileID: "f-" + name, Name: name, Size: 10, Mime: "text/plain", StartedAt: at})
	require.NoError(t, err)
	return id
}

func TestDownloadLifecycle(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	a := addDownload(t, st, "a.txt", 100)
	b := addDownload(t, st, "b.txt", 200)

	list, err := st.ListDownloads(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, []int64{b, a}, []int64{list[0].ID, list[1].ID}, "newest first")
	assert.Equal(t, DownloadRunning, list[0].State)
	assert.Equal(t, Download{ID: a, ServerID: 1, FileID: "f-a.txt", Name: "a.txt", Size: 10, Mime: "text/plain", StartedAt: 100, State: DownloadRunning}, list[1])

	require.NoError(t, st.FinishDownload(ctx, a, "/dl/a.txt", "", 150))
	require.NoError(t, st.FinishDownload(ctx, b, "", "no_file", 250))
	d, err := st.GetDownload(ctx, a)
	require.NoError(t, err)
	assert.Equal(t, DownloadDone, d.State)
	assert.Equal(t, "/dl/a.txt", d.Path)
	assert.Equal(t, int64(150), d.FinishedAt)
	d, err = st.GetDownload(ctx, b)
	require.NoError(t, err)
	assert.Equal(t, DownloadFailed, d.State)
	assert.Equal(t, "no_file", d.Error)

	ok, err := st.RaiseDownload(ctx, Download{ID: a, ServerID: 1, FileID: "f-b.txt", Path: "/dl/a.txt"}, 300)
	require.NoError(t, err)
	assert.False(t, ok, "only the entry of that very file is raised")
	ok, err = st.RaiseDownload(ctx, Download{ID: a, ServerID: 1, FileID: "f-a.txt", Path: "/dl/a.txt"}, 300)
	require.NoError(t, err)
	assert.True(t, ok)
	list, err = st.ListDownloads(ctx)
	require.NoError(t, err)
	assert.Equal(t, a, list[0].ID, "a download asked for again moves to the top")

	require.NoError(t, st.RemoveDownload(ctx, a))
	_, err = st.GetDownload(ctx, a)
	require.ErrorIs(t, err, ErrNotFound)
	ok, err = st.RaiseDownload(ctx, Download{ID: a, ServerID: 1, FileID: "f-a.txt", Path: "/dl/a.txt"}, 400)
	require.NoError(t, err)
	assert.False(t, ok, "a removed entry is not raised")
}

func TestClearDownloadsKeepsRunningOnes(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	done := addDownload(t, st, "done.txt", 1)
	failed := addDownload(t, st, "failed.txt", 2)
	running := addDownload(t, st, "running.txt", 3)
	require.NoError(t, st.FinishDownload(ctx, done, "/dl/done.txt", "", 5))
	require.NoError(t, st.FinishDownload(ctx, failed, "", "unreachable", 5))

	require.NoError(t, st.RemoveDownload(ctx, running))
	require.NoError(t, st.ClearDownloads(ctx))
	list, err := st.ListDownloads(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, running, list[0].ID, "a download in progress stays")
}

func TestDownloadsKeepTheLatestHundred(t *testing.T) {
	st := openTest(t)
	for i := 1; i <= MaxDownloads+5; i++ {
		addDownload(t, st, fmt.Sprintf("%03d.txt", i), int64(i))
	}
	list, err := st.ListDownloads(context.Background())
	require.NoError(t, err)
	require.Len(t, list, MaxDownloads)
	assert.Equal(t, fmt.Sprintf("%03d.txt", MaxDownloads+5), list[0].Name)
	assert.Equal(t, "006.txt", list[MaxDownloads-1].Name, "the oldest go")
}

func TestInterruptedDownloadsFailOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	ctx := context.Background()
	st, err := Open(ctx, path)
	require.NoError(t, err)
	done, err := st.AddDownload(ctx, Download{ServerID: 1, FileID: "f1", Name: "a.txt", StartedAt: 1})
	require.NoError(t, err)
	require.NoError(t, st.FinishDownload(ctx, done, "/dl/a.txt", "", 2))
	cut, err := st.AddDownload(ctx, Download{ServerID: 1, FileID: "f2", Name: "b.txt", StartedAt: 3})
	require.NoError(t, err)
	require.NoError(t, st.Close())

	st, err = Open(ctx, path)
	require.NoError(t, err)
	defer st.Close()
	d, err := st.GetDownload(ctx, cut)
	require.NoError(t, err)
	assert.Equal(t, DownloadFailed, d.State)
	assert.Equal(t, DownloadInterrupted, d.Error)
	assert.NotZero(t, d.FinishedAt)
	d, err = st.GetDownload(ctx, done)
	require.NoError(t, err)
	assert.Equal(t, DownloadDone, d.State, "finished downloads are left alone")
}

func TestDownloadIDsAreNeverReused(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	addDownload(t, st, "a.txt", 1)
	b := addDownload(t, st, "b.txt", 2)
	require.NoError(t, st.FinishDownload(ctx, b, "/dl/b.txt", "", 3))
	require.NoError(t, st.RemoveDownload(ctx, b))
	c := addDownload(t, st, "c.txt", 4)
	assert.Greater(t, c, b, "a remembered id never points at another download")
}
