package media

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// streamFile is a file of the stream test upstream.
type streamFile struct {
	info model.FileInfo
	data []byte
}

// clipBytes is a recognisable body: byte i is i%251.
func clipBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// streamUpstream serves /files/{id}/info and /files/{id} (Range through
// http.ServeContent, like the real server) and records what it saw.
type streamUpstream struct {
	files      map[string]streamFile
	lastRange  atomic.Value // string
	lastAccEnc atomic.Value // string
	// endless: /files/endless writes until the client goes away; gone is
	// closed then and written counts what it managed to send.
	gone    chan struct{}
	written atomic.Int64
}

func newStreamUpstream() *streamUpstream {
	return &streamUpstream{gone: make(chan struct{}), files: map[string]streamFile{
		"clip":    {info: model.FileInfo{ID: "clip", Name: "clip.webm", Extension: "webm", MimeType: "video/webm"}, data: clipBytes(1000)},
		"mkv":     {info: model.FileInfo{ID: "mkv", Name: "a.mkv", Extension: "mkv", MimeType: "application/octet-stream"}, data: clipBytes(10)},
		"voice":   {info: model.FileInfo{ID: "voice", Name: "voice", MimeType: "audio/mpeg"}, data: clipBytes(10)},
		"pdf":     {info: model.FileInfo{ID: "pdf", Name: "spec.pdf", Extension: "pdf", MimeType: "application/pdf"}, data: []byte("%PDF-1.4")},
		"page":    {info: model.FileInfo{ID: "page", Name: "x.html", Extension: "html", MimeType: "video/mp4"}, data: []byte("<script>")},
		"endless": {info: model.FileInfo{ID: "endless", Name: "big.mp4", Extension: "mp4", MimeType: "video/mp4"}},
	}}
}

func (u *streamUpstream) handle(w http.ResponseWriter, r *http.Request) {
	rest, _ := strings.CutPrefix(r.URL.Path, "/api/v4/files/")
	id, info := strings.CutSuffix(rest, "/info")
	f, ok := u.files[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if info {
		_ = json.NewEncoder(w).Encode(f.info)
		return
	}
	u.lastRange.Store(r.Header.Get("Range"))
	u.lastAccEnc.Store(r.Header.Get("Accept-Encoding"))
	if id == "endless" {
		defer close(u.gone)
		w.Header().Set("Content-Type", "video/mp4")
		chunk := make([]byte, 32<<10)
		for {
			n, err := w.Write(chunk)
			u.written.Add(int64(n))
			if err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			default:
			}
		}
	}
	w.Header().Set("Content-Type", f.info.MimeType)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.data))
}

func newStreamEnv(t *testing.T) (*env, *streamUpstream) {
	t.Helper()
	u := newStreamUpstream()
	return newEnv(t, 0, u.handle), u
}

func (e *env) do(method, path string, hdr http.Header) (*http.Response, []byte) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, nil)
	require.NoError(e.t, err)
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestStreamPassesRangeThroughAs206(t *testing.T) {
	e, u := newStreamEnv(t)
	resp, body := e.do(http.MethodGet, "/media/1/stream/clip", http.Header{"Range": {"bytes=10-19"}})
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Equal(t, "bytes=10-19", u.lastRange.Load())
	assert.Equal(t, "identity", u.lastAccEnc.Load(), "no transparent gzip: byte ranges must stay byte ranges")
	assert.Equal(t, "bytes 10-19/1000", resp.Header.Get("Content-Range"))
	assert.Equal(t, "10", resp.Header.Get("Content-Length"))
	assert.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
	assert.Equal(t, "video/webm", resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "private, max-age=300", resp.Header.Get("Cache-Control"))
	assert.Equal(t, clipBytes(1000)[10:20], body)

	resp, body = e.do(http.MethodGet, "/media/1/stream/clip", http.Header{"Range": {"bytes=990-"}})
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Equal(t, "bytes 990-999/1000", resp.Header.Get("Content-Range"))
	assert.Len(t, body, 10)
}

func TestStreamServesTheWholeFileAs200(t *testing.T) {
	e, u := newStreamEnv(t)
	resp, body := e.do(http.MethodGet, "/media/1/stream/clip", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "", u.lastRange.Load())
	assert.Equal(t, "1000", resp.Header.Get("Content-Length"))
	assert.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
	assert.Empty(t, resp.Header.Get("Content-Range"))
	assert.Equal(t, clipBytes(1000), body)

	resp, body = e.do(http.MethodHead, "/media/1/stream/clip", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "1000", resp.Header.Get("Content-Length"))
	assert.Empty(t, body)
}

func TestStreamTypeComesFromFileInfoAndOnlyMediaIsServed(t *testing.T) {
	e, _ := newStreamEnv(t)
	for id, want := range map[string]string{"mkv": "video/x-matroska", "voice": "audio/mpeg", "page": "video/mp4"} {
		resp, _ := e.do(http.MethodGet, "/media/1/stream/"+id, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode, id)
		assert.Equal(t, want, resp.Header.Get("Content-Type"), id)
	}
	resp, _ := e.do(http.MethodGet, "/media/1/stream/pdf", nil)
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
	assert.Zero(t, e.origin.count("/api/v4/files/pdf"), "a non-media file is never fetched")

	e.do(http.MethodGet, "/media/1/stream/clip", http.Header{"Range": {"bytes=0-1"}})
	e.do(http.MethodGet, "/media/1/stream/clip", http.Header{"Range": {"bytes=2-3"}})
	assert.Equal(t, 1, e.origin.count("/api/v4/files/clip/info"), "file info is looked up once per file")
}

func TestStreamRangeErrors(t *testing.T) {
	e, _ := newStreamEnv(t)
	for _, rng := range []string{"bytes=abc", "bytes=0-1,5-6", "items=0-1", "bytes=5-2", "bytes=-", "bytes=1-2-3"} {
		resp, _ := e.do(http.MethodGet, "/media/1/stream/clip", http.Header{"Range": {rng}})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, rng)
	}
	assert.Zero(t, e.origin.count("/api/v4/files/clip"), "a malformed Range is not sent upstream")
	resp, _ := e.do(http.MethodGet, "/media/1/stream/clip", http.Header{"Range": {"bytes=5000-"}})
	assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, resp.StatusCode, "the server's 416 is passed through")
}

func TestStreamIsOnlyForLiveServersAndKnownFiles(t *testing.T) {
	e, _ := newStreamEnv(t)
	resp, _ := e.do(http.MethodGet, "/media/2/stream/clip", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "not signed in / not live")
	resp, _ = e.do(http.MethodGet, "/media/1/stream/gone", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	for _, p := range []string{"/media/1/stream/..%2Fx", "/media/1/stream", "/media/1/stream/clip/extra"} {
		resp, _ = e.do(http.MethodGet, p, nil)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, p)
	}
}

func TestStreamCancelClosesTheUpstream(t *testing.T) {
	e, u := newStreamEnv(t)
	resp, err := http.Get(e.srv.URL + "/media/1/stream/endless")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, err = io.ReadFull(resp.Body, make([]byte, 64<<10))
	require.NoError(t, err)
	_ = resp.Body.Close()
	select {
	case <-u.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream request was not closed when the client went away")
	}
	assert.Less(t, u.written.Load(), int64(64<<20), "the body was not read to its end")
}

func TestStreamWritesNothingToTheCache(t *testing.T) {
	e, _ := newStreamEnv(t)
	resp, _ := e.do(http.MethodGet, "/media/1/stream/clip", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = e.do(http.MethodGet, "/media/1/stream/clip", http.Header{"Range": {"bytes=0-99"}})
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	des, err := os.ReadDir(e.dir)
	require.NoError(t, err)
	assert.Empty(t, des)
	assert.Zero(t, e.cache.Size())
}

func TestStreamUnauthorizedIsForbiddenAndNotRemembered(t *testing.T) {
	var code atomic.Int32
	code.Store(http.StatusUnauthorized)
	u := newStreamUpstream()
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if c := int(code.Load()); c != 0 {
			w.WriteHeader(c)
			return
		}
		u.handle(w, r)
	})
	resp, _ := e.do(http.MethodGet, "/media/1/stream/clip", nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	code.Store(0)
	resp, _ = e.do(http.MethodGet, "/media/1/stream/clip", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
