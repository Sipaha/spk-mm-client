package media

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		h := resp.Header
		assert.Equal(t, "application/pdf", h.Get("Content-Type"))
		assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
		assert.Equal(t, "default-src 'none'; sandbox", h.Get("Content-Security-Policy"),
			"the sandbox also keeps WebKit's built-in PDF viewer from running on this URL")
		assert.Equal(t, "attachment", h.Get("Content-Disposition"))
		assert.Equal(t, "no-store", h.Get("Cache-Control"), "Go keeps the file on disk; the webview keeps no copy")
	}
	assert.Equal(t, 1, e.origin.count("/api/v4/files/f1"), "fetched once, then served from the cache")
	assert.Equal(t, int64(len(doc)), e.cache.Size())

	resp, err := http.Head(e.srv.URL + "/media/1/pdf/f1")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "application/pdf", resp.Header.Get("Content-Type"))

	req, err := http.NewRequest(http.MethodGet, e.srv.URL+"/media/1/pdf/f1", nil)
	require.NoError(t, err)
	req.Header.Set("Range", "bytes=0-4")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	part, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Equal(t, "%PDF-", string(part))
	assert.Equal(t, "attachment", resp.Header.Get("Content-Disposition"))
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
			assert.Empty(t, resp.Header.Get("Cache-Control"), id)
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
