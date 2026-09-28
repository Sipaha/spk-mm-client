package media

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noisePNG is w×h of random opaque pixels: barely compressible, so the file
// is about as large as its bitmap — a large attachment in miniature.
func noisePNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(r.Uint32())
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// allocated is how many bytes f allocated on the Go heap (tests of this
// package do not run in parallel).
func allocated(f func()) uint64 {
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// liveHeap is the Go heap still reachable after a full collection.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// A picture that already fits FeedMax is copied through: the file is never
// read into memory whole (attachments are not held in the Go heap).
func TestWriteImagePassesAFittingPictureThroughWithoutReadingItWhole(t *testing.T) {
	data := noisePNG(900, 700)
	out, err := os.Create(filepath.Join(t.TempDir(), "out"))
	require.NoError(t, err)
	defer out.Close()
	var size int64
	n := allocated(func() { size, err = writeImage(out, bytes.NewReader(data), spec{max: 25 << 20, scale: true}) })
	require.NoError(t, err)
	assert.Equal(t, int64(len(data)), size)
	got, err := os.ReadFile(out.Name())
	require.NoError(t, err)
	assert.Equal(t, data, got, "passed through as is")
	t.Logf("allocated %d bytes for a %d-byte file", n, len(data))
	assert.Less(t, n, uint64(len(data)/4), "allocated %d bytes for a %d-byte file", n, len(data))
}

// Scaling needs the bitmap, but not the file in memory as well: the
// decoder reads the stream, the encoder writes to dst.
func TestWriteImageScalesWithoutReadingTheFileWhole(t *testing.T) {
	const w, h = 1600, 1200
	data := noisePNG(w, h)
	out, err := os.Create(filepath.Join(t.TempDir(), "out"))
	require.NoError(t, err)
	defer out.Close()
	n := allocated(func() { _, err = writeImage(out, bytes.NewReader(data), spec{max: 25 << 20, scale: true}) })
	require.NoError(t, err)
	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	cfg, _, err := image.DecodeConfig(out)
	require.NoError(t, err)
	assert.Equal(t, FeedMax, cfg.Width)
	bitmaps := uint64(w*h*4 + FeedMax*(FeedMax*h/w)*4) // decoded + scaled
	t.Logf("allocated %d bytes: bitmaps %d, file %d", n, bitmaps, len(data))
	assert.Less(t, n, bitmaps+uint64(len(data)/4), "allocated %d bytes: bitmaps %d, file %d", n, bitmaps, len(data))
}

// The byte cap still holds when the picture is streamed: bytes after the
// image data count too.
func TestWriteImageStreamedStillEnforcesTheByteCap(t *testing.T) {
	small := pngOf(100, 100)
	padded := append(append([]byte{}, small...), make([]byte, 4096)...)
	_, err := writeImage(io.Discard, bytes.NewReader(padded), spec{max: int64(len(small) + 100), scale: true})
	assert.ErrorIs(t, err, errTooLarge, "fits FeedMax: copied")
	big := pngOf(2000, 100)
	padded = append(append([]byte{}, big...), make([]byte, 4096)...)
	_, err = writeImage(io.Discard, bytes.NewReader(padded), spec{max: int64(len(big) + 100), scale: true})
	assert.ErrorIs(t, err, errTooLarge, "scaled")
}

// A large staged picture fetched for its preview leaves nothing behind in
// the Go heap once served: no bytes, bitmaps or fetch bookkeeping retained
// (the Task 6 memory check).
func TestStagedPreviewRetainsNothing(t *testing.T) {
	st := &testStaged{opened: map[string]int{}, files: map[string]stagedFile{"big": {"image/png", noisePNG(2000, 1600)}}}
	c, err := New(Options{Dir: t.TempDir(), Origin: &testOrigin{hits: map[string]int{}}, Staged: st})
	require.NoError(t, err)
	srv := httptest.NewServer(c)
	defer srv.Close()
	before := liveHeap()
	resp, body := getBody(t, srv.URL+"/media/1/staged/big")
	require.Equal(t, 200, resp.StatusCode)
	require.NotEmpty(t, body)
	body = nil
	after := liveHeap()
	assert.Less(t, int64(after)-int64(before), int64(1<<20), "live heap before %d, after %d", before, after)
	c.mu.Lock()
	assert.Empty(t, c.inflight, "no fetch left in flight")
	c.mu.Unlock()
	_ = body
}

// jpegWithAPPn is a small JPEG with n 60 KiB APP1 segments before its frame
// header (EXIF can sit there): DecodeConfig reads through all of them.
func jpegWithAPPn(n int) []byte {
	var j bytes.Buffer
	_ = jpeg.Encode(&j, image.NewRGBA(image.Rect(0, 0, 100, 100)), nil)
	src := j.Bytes()
	out := append([]byte{}, src[:2]...) // SOI
	seg := make([]byte, 60<<10)
	for range n {
		l := len(seg) + 2
		out = append(out, 0xFF, 0xE1, byte(l>>8), byte(l))
		out = append(out, seg...)
	}
	return append(out, src[2:]...)
}

// The header the decoder reads is not kept in memory, however long it is.
func TestWriteImageDoesNotKeepALongHeader(t *testing.T) {
	data := jpegWithAPPn(40)
	var err error
	n := allocated(func() { _, err = writeImage(io.Discard, bytes.NewReader(data), spec{max: 25 << 20, scale: true}) })
	require.NoError(t, err)
	t.Logf("allocated %d bytes for a %d-byte file", n, len(data))
	assert.Less(t, n, uint64(len(data)/4))
}

// A feed picture still downloading does not hold up another one: the body
// is spooled to disk outside the decode semaphore, which bounds decodes only.
func TestSlowFeedDownloadDoesNotBlockOtherPictures(t *testing.T) {
	big := pngOf(2000, 300)
	release := make(chan struct{})
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "slow") {
			_, _ = w.Write(big[:len(big)/2])
			w.(http.Flusher).Flush()
			<-release
			_, _ = w.Write(big[len(big)/2:])
			return
		}
		_, _ = w.Write(big)
	})
	defer close(release)
	slowDone := make(chan int, 1)
	go func() {
		resp, _ := e.get("/media/1/feed/slow")
		slowDone <- resp.StatusCode
	}()
	require.Eventually(t, func() bool { return e.origin.count("/api/v4/files/slow/preview") == 1 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond) // the slow body is being read
	fast := make(chan int, 1)
	go func() {
		resp, _ := e.get("/media/1/feed/fast")
		fast <- resp.StatusCode
	}()
	select {
	case code := <-fast:
		assert.Equal(t, 200, code)
	case <-time.After(5 * time.Second):
		t.Fatal("a slow download blocked another picture")
	}
	release <- struct{}{}
	assert.Equal(t, 200, <-slowDone)
}

// A PDF goes to the cache as it comes and to the UI from the file: neither
// the download nor the answer is ever held whole in the Go heap.
func TestPDFIsNotReadWhole(t *testing.T) {
	const size = 20 << 20
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { writePDFBody(w, size) })
	var got int64
	n := allocated(func() {
		resp, err := http.Get(e.srv.URL + "/media/1/pdf/big")
		require.NoError(t, err)
		got, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
	})
	assert.Equal(t, int64(size), got)
	t.Logf("allocated %d bytes for a %d-byte PDF", n, size)
	assert.Less(t, n, uint64(size/4), "allocated %d bytes for a %d-byte PDF", n, size)
}
