package mmfake

import (
	"bytes"
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// With FilesDir, uploads are stored on disk, not in the fake's memory: the
// dev desktop runs the fake in its own process, and a memory check pasting
// 16 MB pictures measured the fake keeping every upload (Task 6 fix round).
// They are served as before, and removed when the fake closes.
func TestUploadsWithFilesDirAreStoredOnDisk(t *testing.T) {
	dir := t.TempDir()
	s := Start(Options{FilesDir: dir})
	closed := false
	defer func() {
		if !closed {
			s.Close()
		}
	}()
	a := loginAs(t, s, "bob")
	pic := patternPNG(2400, 1200, palette[0])
	resp, body := a.rawPost("/api/v4/files?channel_id=c-offtopic&filename=pic.png&client_id=c1", pic)
	require.Equal(t, 201, resp.StatusCode)
	var out struct {
		FileInfos []model.FileInfo `json:"file_infos"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	fi := out.FileInfos[0]
	assert.Equal(t, int64(len(pic)), fi.Size)
	assert.Equal(t, "image/png", fi.MimeType)
	assert.Equal(t, 2400, fi.Width)
	assert.True(t, fi.HasPreviewImage)

	s.mu.Lock()
	f := s.chat.files[fi.ID]
	s.mu.Unlock()
	require.NotNil(t, f)
	assert.Nil(t, f.data, "not kept in memory")
	assert.Nil(t, f.preview, "nor its preview")
	onDisk, err := os.ReadFile(f.path)
	require.NoError(t, err)
	assert.Equal(t, pic, onDisk)

	resp, got := a.raw("GET", "/api/v4/files/"+fi.ID, nil)
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, pic, got)
	for _, what := range []string{"preview", "thumbnail"} {
		resp, got = a.raw("GET", "/api/v4/files/"+fi.ID+"/"+what, nil)
		require.Equal(t, 200, resp.StatusCode, what)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(got))
		require.NoError(t, err, what)
		assert.LessOrEqual(t, cfg.Width, 1920, what)
	}
	s.SetFileThrottle(10 << 20)
	resp, got = a.raw("GET", "/api/v4/files/"+fi.ID, nil)
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, pic, got, "a throttled download reads the file too")

	s.SetUploadThrottle(1000) // chunk 100 B/100 ms
	start := time.Now()
	resp, _ = a.rawPost("/api/v4/files?channel_id=c-offtopic&filename=slow.bin", bytes.Repeat([]byte("x"), 300))
	require.Equal(t, 201, resp.StatusCode)
	assert.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond, "the upload throttle applies")

	s.Close()
	closed = true
	left, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, left, "the fake's files go with it")
	_, err = os.Stat(filepath.Dir(f.path))
	assert.True(t, os.IsNotExist(err))
}

// An over-limit upload leaves no file behind.
func TestUploadsWithFilesDirOverLimitLeaveNothing(t *testing.T) {
	dir := t.TempDir()
	s := Start(Options{FilesDir: dir, MaxFileSize: 10})
	defer s.Close()
	a := loginAs(t, s, "bob")
	req := "/api/v4/files?channel_id=c-offtopic&filename=x.bin"
	resp, _ := a.rawPost(req, bytes.Repeat([]byte("x"), 11))
	assert.Equal(t, 413, resp.StatusCode)
	sub, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, sub, 1)
	files, err := os.ReadDir(filepath.Join(dir, sub[0].Name()))
	require.NoError(t, err)
	assert.Empty(t, files)
}
