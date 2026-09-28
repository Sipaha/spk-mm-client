//go:build unix

package attach

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A FIFO put where an attached file was must not hang the upload (nor
// Close waiting for it) or the preview.
func TestAFIFOInPlaceOfTheFileIsNotOpened(t *testing.T) {
	e := newEnv(t)
	e.b.setLive(1, false)
	p := e.file("pic.png", string(pngHeader()))
	a, err := e.s.AddPath(1, "c1", "", p)
	require.NoError(t, err)
	require.NoError(t, os.Remove(p))
	require.NoError(t, syscall.Mkfifo(p, 0o600))

	done := make(chan error, 1)
	go func() {
		f, _, err := e.s.Open(1, a.ID)
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		assert.Equal(t, CodeNotFound, codeOf(err))
	case <-time.After(2 * time.Second):
		t.Fatal("Open hung on a FIFO")
	}

	e.b.setLive(1, true)
	e.s.Wake(1)
	assert.Equal(t, CodeChanged, e.waitState(a.ID, StateFailed).Error)

	fifo := filepath.Join(t.TempDir(), "pipe")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	_, err = e.s.AddPath(1, "c1", "", fifo)
	assert.Equal(t, CodeNotAFile, codeOf(err))
	closed := make(chan struct{})
	go func() { e.s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung")
	}
}
