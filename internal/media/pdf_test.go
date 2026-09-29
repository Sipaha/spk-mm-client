package media

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pdfOf is a "PDF" of n bytes: the header, then padding (the cache checks
// the magic, not the document — pdf.js in the UI parses it).
func pdfOf(n int) []byte {
	b := []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	return append(b, bytes.Repeat([]byte{'x'}, n-len(b))...)
}

// writePDFBody streams a PDF header and then padding up to n bytes without
// holding n bytes anywhere.
func writePDFBody(w io.Writer, n int64) {
	head := []byte("%PDF-1.7\n")
	_, _ = w.Write(head)
	_, _ = io.CopyN(w, zeros{}, n-int64(len(head)))
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestPDFServedAsSandboxedAttachment(t *testing.T) {
	doc := pdfOf(64 << 10)
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/files/f1", r.URL.Path)
		w.Header().Set("Content-Type", "text/html") // never trusted
		_, _ = w.Write(doc)
	})
	for i := 0; i < 2; i++ {
		resp, body := e.get("/media/1/pdf/f1")
		require.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, doc, body)
		assert.Equal(t, "application/pdf", resp.Header.Get("Content-Type"))
		assertPDFGuards(t, resp.Header, "GET")
	}
	assert.Equal(t, 1, e.origin.count("/api/v4/files/f1"), "fetched once, then served from the cache")
	assert.Equal(t, int64(len(doc)), e.cache.Size())

	resp, err := http.Head(e.srv.URL + "/media/1/pdf/f1")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "application/pdf", resp.Header.Get("Content-Type"))
	assertPDFGuards(t, resp.Header, "HEAD")

	req, err := http.NewRequest(http.MethodGet, e.srv.URL+"/media/1/pdf/f1", nil)
	require.NoError(t, err)
	req.Header.Set("Range", "bytes=0-4")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	part, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Equal(t, "%PDF-", string(part))
	assert.Equal(t, "application/pdf", resp.Header.Get("Content-Type"))
	assertPDFGuards(t, resp.Header, "Range")
}

// assertPDFGuards checks the headers every answer on a pdf URL carries —
// success, HEAD, Range and errors alike.
func assertPDFGuards(t *testing.T, h http.Header, what string) {
	t.Helper()
	assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"), what)
	assert.Equal(t, "default-src 'none'; sandbox", h.Get("Content-Security-Policy"),
		"%s: the sandbox also keeps WebKit's built-in PDF viewer from running on this URL", what)
	assert.Equal(t, "attachment", h.Get("Content-Disposition"), what)
	assert.Equal(t, "no-store", h.Get("Cache-Control"), "%s: Go keeps the file on disk; the webview keeps no copy", what)
}

// Every error on a pdf URL carries the same guards as a success: the
// webview neither sniffs, runs, shows nor keeps it.
func TestPDFErrorsCarryTheGuardHeaders(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/files/html":
			_, _ = w.Write([]byte("<html></html>"))
		case "/api/v4/files/big":
			w.Header().Set("Content-Length", strconv.Itoa(PDFMax+1))
			w.WriteHeader(http.StatusOK)
		case "/api/v4/files/gone":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_, _ = w.Write(pdfOf(2048))
		}
	})
	for path, want := range map[string]int{
		"/media/2/pdf/f1": http.StatusNotFound, "/media/1/pdf/html": http.StatusUnsupportedMediaType,
		"/media/1/pdf/big": http.StatusRequestEntityTooLarge, "/media/1/pdf/gone": http.StatusBadGateway,
	} {
		resp, _ := e.get(path)
		assert.Equal(t, want, resp.StatusCode, path)
		assertPDFGuards(t, resp.Header, path)
	}
	req, err := http.NewRequest(http.MethodGet, e.srv.URL+"/media/1/pdf/f1", nil)
	require.NoError(t, err)
	req.Header.Set("Range", "bytes=999999-")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, resp.StatusCode)
	assertPDFGuards(t, resp.Header, "416")
}

// Only the whole file is a document: a partial or empty answer from the
// server is refused, never cached as the PDF.
func TestPDFNeedsTheWholeFile(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/files/partial":
			w.Header().Set("Content-Range", "bytes 0-2047/99999")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(pdfOf(2048))
		case "/api/v4/files/nocontent":
			w.WriteHeader(http.StatusNoContent)
		}
	})
	for _, id := range []string{"partial", "nocontent"} {
		resp, _ := e.get("/media/1/pdf/" + id)
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode, id)
	}
	assert.Zero(t, e.cache.Size())
}

// PDFs have fetch slots of their own (pdfFetches), apart from the shared
// ones: a few PDFs stalled on a slow link never hold up the pictures.
func TestStalledPDFsDoNotBlockOtherPictures(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/thumbnail") {
			_, _ = w.Write(pngOf(2, 2))
			return
		}
		_, _ = w.Write([]byte("%PDF-1.7\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write(pdfOf(2048))
		case <-r.Context().Done():
		}
	})
	started := func() (n int) {
		for i := range 6 {
			n += e.origin.count("/api/v4/files/p" + strconv.Itoa(i))
		}
		return n
	}
	done := make(chan int, 6)
	for i := range 6 {
		go func() {
			resp, _ := e.get("/media/1/pdf/p" + strconv.Itoa(i))
			done <- resp.StatusCode
		}()
	}
	require.Eventually(t, func() bool { return started() == pdfFetches }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, pdfFetches, started(), "PDFs wait for their own slots")
	thumb := make(chan int, 1)
	go func() {
		resp, _ := e.get("/media/1/thumb/t1")
		thumb <- resp.StatusCode
	}()
	select {
	case code := <-thumb:
		assert.Equal(t, 200, code)
	case <-time.After(5 * time.Second):
		t.Fatal("stalled PDFs blocked a thumbnail")
	}
	close(release)
	for range 6 {
		assert.Equal(t, 200, <-done)
	}
}

// A PDF has one reader, the viewer: once nobody waits for it (the viewer
// closed), its download is cancelled rather than left holding a slot for
// minutes, and it is not remembered as a failure.
func TestAbandonedPDFFetchIsCancelled(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	var first atomic.Bool
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if !first.Swap(true) {
			_, _ = w.Write([]byte("%PDF-1.7\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			cancelled <- struct{}{}
			return
		}
		_, _ = w.Write(pdfOf(2048))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.srv.URL+"/media/1/pdf/f1", nil)
	require.NoError(t, err)
	_, err = http.DefaultClient.Do(req)
	require.Error(t, err, "the viewer gave up")
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoned PDF download went on")
	}
	resp, body := e.get("/media/1/pdf/f1")
	require.Equal(t, 200, resp.StatusCode, "a cancelled download is not remembered as a failure")
	assert.Equal(t, pdfOf(2048), body)
	assert.Equal(t, 2, e.origin.count("/api/v4/files/f1"))
}

// Readers accept the header anywhere in the first 1024 bytes (some
// producers put junk before it); so does the cache.
func TestPDFHeaderWithinTheFirstKilobyte(t *testing.T) {
	doc := append(bytes.Repeat([]byte{' '}, pdfMagicWithin-len("%PDF-")), pdfOf(4096)...)
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(doc) })
	resp, body := e.get("/media/1/pdf/f1")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, doc, body)
}

func TestNotAPDFIs415(t *testing.T) {
	late := append(bytes.Repeat([]byte{' '}, pdfMagicWithin-len("%PDF-")+1), pdfOf(4096)...)
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/files/html":
			_, _ = w.Write([]byte("<html><script>alert(1)</script></html>"))
		case "/api/v4/files/late":
			_, _ = w.Write(late)
		case "/api/v4/files/empty":
		default:
			_, _ = w.Write(pdfOf(1024))
		}
	})
	for _, id := range []string{"html", "late", "empty"} {
		for i := 0; i < 2; i++ {
			resp, body := e.get("/media/1/pdf/" + id)
			assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode, id)
			assert.NotContains(t, string(body), "script", id)
			assertPDFGuards(t, resp.Header, id) // no-store: nothing long-lived on an error
		}
		assert.Equal(t, 1, e.origin.count("/api/v4/files/"+id), "the refusal is remembered: %s", id)
	}
	assert.Zero(t, e.cache.Size())

	// Checked on serve as well: a cache file that is not a PDF (corrupt,
	// or written by something else) is dropped, never served.
	bad := filepath.Join(e.dir, fileName("1/pdf/f1/"))
	require.NoError(t, os.WriteFile(bad, []byte("<html>not a pdf</html>"), 0o600))
	e.open(0, time.Minute)
	resp, body := e.get("/media/1/pdf/f1")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
	assert.NotContains(t, string(body), "not a pdf")
	_, err := os.Stat(bad)
	assert.ErrorIs(t, err, os.ErrNotExist, "the corrupt entry is dropped")
	resp, _ = e.get("/media/1/pdf/f1")
	assert.Equal(t, 200, resp.StatusCode, "the next request fetches it anew")
	assert.Equal(t, 1, e.origin.count("/api/v4/files/f1"))
}

func TestPDFOverCapIs413(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/files/fits":
			writePDFBody(w, PDFMax)
		case "/api/v4/files/over": // chunked: the size shows only while copying
			writePDFBody(w, PDFMax+1)
		case "/api/v4/files/declared": // refused by its Content-Length, not read
			w.Header().Set("Content-Length", strconv.Itoa(PDFMax+1))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("%PDF-1.7\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	})
	e.open(0, 30*time.Second)
	resp, err := http.Get(e.srv.URL + "/media/1/pdf/fits")
	require.NoError(t, err)
	n, _ := io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(PDFMax), n)
	assert.Equal(t, int64(PDFMax), e.cache.Size())

	for _, id := range []string{"over", "declared"} {
		start := time.Now()
		resp, _ := e.get("/media/1/pdf/" + id)
		assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, id)
		assert.Less(t, time.Since(start), 20*time.Second, id)
	}
	assert.Equal(t, int64(PDFMax), e.cache.Size(), "nothing refused is kept")
	des, err := os.ReadDir(e.dir)
	require.NoError(t, err)
	for _, de := range des {
		assert.False(t, strings.HasSuffix(de.Name(), ".tmp"), "no spool left behind: %s", de.Name())
	}
}

// A PDF is up to PDFMax (twice a picture): its fetch gets a longer timeout
// than the pictures' so a slow link can still load one.
func TestPDFFetchHasALongerTimeout(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		if strings.HasSuffix(r.URL.Path, "/thumbnail") {
			_, _ = w.Write(pngOf(2, 2))
			return
		}
		_, _ = w.Write(pdfOf(2048))
	})
	e.open(0, 200*time.Millisecond)
	resp, _ := e.get("/media/1/thumb/f1")
	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode, "a picture: the plain timeout")
	resp, _ = e.get("/media/1/pdf/f1")
	assert.Equal(t, 200, resp.StatusCode, "a PDF: pdfTimeoutFactor × the timeout")
}

// queuedPDFEnv fills both PDF slots with downloads stalled until release,
// then abandons p2 while it waits for a slot. It returns the p2 call that
// was abandoned, and release (also run on cleanup, so a failing test does
// not wait for the stalled downloads).
func queuedPDFEnv(t *testing.T) (e *env, release func(), abandoned *call) {
	t.Helper()
	stalled := make(chan struct{})
	release = sync.OnceFunc(func() { close(stalled) })
	e = newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("%PDF-1.7\n"))
		w.(http.Flusher).Flush()
		select {
		case <-stalled:
			_, _ = w.Write(pdfOf(2048))
		case <-r.Context().Done():
		}
	})
	t.Cleanup(release)
	for i := range pdfFetches {
		go func() {
			if resp, err := http.Get(e.srv.URL + "/media/1/pdf/p" + strconv.Itoa(i)); err == nil {
				_ = resp.Body.Close()
			}
		}()
	}
	require.Eventually(t, func() bool {
		return e.origin.count("/api/v4/files/p0")+e.origin.count("/api/v4/files/p1") == pdfFetches
	}, 5*time.Second, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.srv.URL+"/media/1/pdf/p2", nil)
	require.NoError(t, err)
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	require.Eventually(t, func() bool {
		e.cache.mu.Lock()
		defer e.cache.mu.Unlock()
		abandoned = e.cache.inflight[pdfKey]
		return abandoned != nil && abandoned.waiters == 1
	}, 5*time.Second, 10*time.Millisecond)
	cancel() // the viewer closed while p2 was queued
	<-gone
	return e, release, abandoned
}

const pdfKey = "1/pdf/p2/"

// R2: an abandoned PDF waiting for a slot leaves the queue at once, not
// when a slot frees up (minutes, with stalled downloads ahead of it).
func TestAbandonedQueuedPDFLeavesAtOnce(t *testing.T) {
	e, release, abandoned := queuedPDFEnv(t)
	select {
	case <-abandoned.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the abandoned PDF still waits for a slot")
	}
	e.cache.mu.Lock()
	assert.Nil(t, e.cache.inflight[pdfKey], "off the in-flight list")
	e.cache.mu.Unlock()
	release()
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, e.origin.count("/api/v4/files/p2"), "never fetched")
	resp, _ := e.get("/media/1/pdf/p2")
	assert.Equal(t, 200, resp.StatusCode, "the abandoned call's 503 is not negative-cached")
}

// R1: a request that comes after the cancel does not join the abandoned
// call (it would get its 503): it starts a fetch of its own, and the
// abandoned call, finishing later, leaves the new one registered.
func TestRequestAfterACancelStartsAFreshPDFFetch(t *testing.T) {
	entered, hold := make(chan struct{}), make(chan struct{})
	unhold := sync.OnceFunc(func() { close(hold) })
	abandonHook = func() {
		close(entered)
		<-hold
	}
	t.Cleanup(func() { abandonHook = func() {} })
	e, release, abandoned := queuedPDFEnv(t)
	t.Cleanup(unhold)
	select {
	case <-entered: // the abandoned call is on its way out, still registered
	case <-time.After(3 * time.Second):
		t.Fatal("the abandoned PDF still waits for a slot")
	}
	again := make(chan int, 1)
	go func() {
		resp, _ := e.get("/media/1/pdf/p2")
		again <- resp.StatusCode
	}()
	require.Eventually(t, func() bool {
		e.cache.mu.Lock()
		defer e.cache.mu.Unlock()
		cl := e.cache.inflight[pdfKey]
		return cl != nil && cl != abandoned && cl.waiters == 1
	}, 3*time.Second, 10*time.Millisecond, "the new request joined the cancelled call")
	unhold()
	<-abandoned.done
	e.cache.mu.Lock()
	assert.NotNil(t, e.cache.inflight[pdfKey], "the abandoned call left the new one registered")
	e.cache.mu.Unlock()
	release()
	assert.Equal(t, 200, <-again)
	assert.Equal(t, 1, e.origin.count("/api/v4/files/p2"))
}
