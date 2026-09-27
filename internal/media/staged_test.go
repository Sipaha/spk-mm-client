package media

import (
	"bytes"
	"errors"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stagedFile struct {
	mime string
	data []byte
}

// testStaged holds attachments of server 1.
type testStaged struct {
	mu     sync.Mutex
	files  map[string]stagedFile
	opened map[string]int
}

func (s *testStaged) StagedType(serverID int64, id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[id]
	if serverID != 1 || !ok {
		return "", false
	}
	return f.mime, true
}

func (s *testStaged) OpenStaged(serverID int64, id string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[id]
	if serverID != 1 || !ok {
		return nil, errors.New("no such attachment")
	}
	s.opened[id]++
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

func (s *testStaged) opens(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened[id]
}

func newStagedEnv(t *testing.T, st Staged) *httptest.Server {
	t.Helper()
	c, err := New(Options{Dir: t.TempDir(), Origin: &testOrigin{hits: map[string]int{}}, Staged: st})
	require.NoError(t, err)
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	return srv
}

func getBody(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestStagedServesRasterPicturesOfThatServer(t *testing.T) {
	st := &testStaged{opened: map[string]int{}, files: map[string]stagedFile{
		"big":   {"image/png", pngOf(2000, 1000)},
		"small": {"image/png", pngOf(4, 4)},
		"huge":  {"image/png", hugePNG(20000, 20000)},
		"notes": {"text/plain", []byte("hello")},
		"fake":  {"image/png", []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>")},
		"svg":   {"image/svg+xml", []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>")},
	}}
	srv := newStagedEnv(t, st)

	resp, body := getBody(t, srv.URL+"/media/1/staged/big")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.Equal(t, "default-src 'none'; sandbox", resp.Header.Get("Content-Security-Policy"))
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, FeedMax, cfg.Width, "scaled like a feed picture")
	resp, _ = getBody(t, srv.URL+"/media/1/staged/big")
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 1, st.opens("big"), "kept in the cache")

	resp, body = getBody(t, srv.URL+"/media/1/staged/small")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, pngOf(4, 4), body)

	resp, _ = getBody(t, srv.URL+"/media/1/staged/huge")
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, "the media limits apply")

	for _, id := range []string{"notes", "svg"} {
		resp, _ = getBody(t, srv.URL+"/media/1/staged/"+id)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "%s: not a raster picture — the UI shows a type icon", id)
		assert.Zero(t, st.opens(id), "%s: never read", id)
	}
	resp, _ = getBody(t, srv.URL+"/media/1/staged/fake")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a picture by name only is sniffed and refused")

	resp, _ = getBody(t, srv.URL+"/media/2/staged/small")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "another server's id")
	resp, _ = getBody(t, srv.URL+"/media/1/staged/missing")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = getBody(t, srv.URL+"/media/1/staged/..%2Fetc")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "keys are checked")

	st.mu.Lock()
	delete(st.files, "small")
	st.mu.Unlock()
	resp, _ = getBody(t, srv.URL+"/media/1/staged/small")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "an attachment gone is gone, cached or not")
}

func TestStagedWithoutAttachmentsIsNotFound(t *testing.T) {
	srv := newStagedEnv(t, nil)
	resp, _ := getBody(t, srv.URL+"/media/1/staged/abc")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
