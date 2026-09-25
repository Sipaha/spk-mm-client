package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/rest"
)

// testOrigin is server 1 backed by an httptest upstream; other ids are
// "not signed in".
type testOrigin struct {
	url   string
	emoji map[string]string
	mu    sync.Mutex
	hits  map[string]int
}

func (o *testOrigin) Get(ctx context.Context, serverID int64, path string, hdr http.Header) (*http.Response, error) {
	if serverID != 1 {
		return nil, ErrNoServer
	}
	return rest.New(o.url, "tok", nil).Stream(ctx, path, hdr)
}

func (o *testOrigin) EmojiID(_ context.Context, _ int64, name string) (string, error) {
	return o.emoji[name], nil
}

func (o *testOrigin) count(path string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hits[path]
}

type clock struct{ ns atomic.Int64 }

// now ticks a second per call: every access is strictly newer (LRU order).
func (c *clock) now() time.Time { return time.Unix(0, c.ns.Add(int64(time.Second))) }

func (c *clock) jump(d time.Duration) { c.ns.Add(int64(d)) }

type env struct {
	t      *testing.T
	cache  *Cache
	origin *testOrigin
	srv    *httptest.Server
	clock  *clock
	dir    string
}

func newEnv(t *testing.T, maxBytes int64, h http.HandlerFunc) *env {
	t.Helper()
	o := &testOrigin{hits: map[string]int{}, emoji: map[string]string{"partyparrot": "e1"}}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.hits[r.URL.Path]++
		o.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(up.Close)
	o.url = up.URL
	e := &env{t: t, origin: o, clock: &clock{}, dir: t.TempDir()}
	e.open(maxBytes, time.Minute)
	return e
}

func (e *env) open(maxBytes int64, timeout time.Duration) {
	c, err := New(Options{Dir: e.dir, MaxBytes: maxBytes, Origin: e.origin, Timeout: timeout, Now: e.clock.now})
	require.NoError(e.t, err)
	if e.srv != nil {
		e.srv.Close()
	}
	e.cache = c
	e.srv = httptest.NewServer(c)
	e.t.Cleanup(e.srv.Close)
}

func (e *env) get(path string) (*http.Response, []byte) {
	e.t.Helper()
	resp, err := http.Get(e.srv.URL + path)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func pngOf(w, h int) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

// fakePNG has a PNG signature and n bytes in total (not decodable).
func fakePNG(n int) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, n-8)...)
}

// hugePNG is a valid PNG header claiming w×h pixels.
func hugePNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // 8-bit grayscale
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	b.Write(make([]byte, 64))
	return b.Bytes()
}

func TestAvatarIsFetchedOnceAndServedWithCacheHeaders(t *testing.T) {
	img := pngOf(4, 4)
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/users/u1/image", r.URL.Path)
		assert.Equal(t, "5", r.URL.Query().Get("_"))
		w.Header().Set("Content-Type", "text/html") // never trusted: we sniff
		_, _ = w.Write(img)
	})
	for i := 0; i < 2; i++ {
		resp, body := e.get("/media/1/avatar/u1?v=5")
		require.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, img, body)
		assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		assert.Equal(t, "default-src 'none'; sandbox", resp.Header.Get("Content-Security-Policy"))
		assert.Equal(t, "private, max-age=31536000, immutable", resp.Header.Get("Cache-Control"))
	}
	assert.Equal(t, 1, e.origin.count("/api/v4/users/u1/image"))
	resp, err := http.Head(e.srv.URL + "/media/1/avatar/u1?v=5")
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(len(img)), e.cache.Size())
}

func TestAvatarVersionIsPartOfTheKey(t *testing.T) {
	var queries []string
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		_, _ = w.Write(pngOf(2, 2))
	})
	e.get("/media/1/avatar/u1?v=5")
	e.get("/media/1/avatar/u1?v=-7")
	e.get("/media/1/avatar/u1?v=0")
	e.get("/media/1/avatar/u1")
	assert.Equal(t, []string{"_=5", "_=-7", ""}, queries, "a new version refetches; 0 and absent are the same key")
}

func TestConcurrentRequestsShareOneFetch(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write(pngOf(2, 2))
	})
	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := e.get("/media/1/thumb/f1")
			codes[i] = resp.StatusCode
		}()
	}
	require.Eventually(t, func() bool { return e.origin.count("/api/v4/files/f1/thumbnail") == 1 }, 5*time.Second, 5*time.Millisecond)
	close(release)
	wg.Wait()
	assert.Equal(t, 1, e.origin.count("/api/v4/files/f1/thumbnail"))
	for _, c := range codes {
		assert.Equal(t, 200, c)
	}
}

func TestSVGIsRefused(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	})
	resp, _ := e.get("/media/1/full/f1?src=file")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
	resp, _ = e.get("/media/1/full/f1?src=file")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
	assert.Equal(t, 1, e.origin.count("/api/v4/files/f1"), "the refusal is remembered")
}

func TestTooLargeIsRefused(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fakePNG(3 << 20)) })
	resp, _ := e.get("/media/1/thumb/f1")
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.Zero(t, e.cache.Size())
}

func TestHugeDimensionsAreRefused(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(hugePNG(20000, 20000)) })
	for _, p := range []string{"/media/1/feed/f1", "/media/1/full/f1"} {
		resp, _ := e.get(p)
		assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode, p)
	}
}

func TestFeedScalesLargeImagesDownAndKeepsSmallOnes(t *testing.T) {
	big, small := pngOf(2000, 500), pngOf(300, 200)
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/files/big/preview":
			_, _ = w.Write(big)
		case "/api/v4/files/small":
			_, _ = w.Write(small)
		default:
			http.NotFound(w, r)
		}
	})
	resp, body := e.get("/media/1/feed/big")
	require.Equal(t, 200, resp.StatusCode)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, [2]int{FeedMax, 240}, [2]int{cfg.Width, cfg.Height})

	resp, body = e.get("/media/1/feed/small?src=file")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, small, body, "an image that fits is passed through")

	resp, body = e.get("/media/1/full/big")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, big, body, "the viewer gets the preview as is")
}

func TestTextPreview(t *testing.T) {
	long := strings.Repeat("a", TextLimit-1) + "й" + "tail"
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, fmt.Sprintf("bytes=0-%d", TextLimit-1), r.Header.Get("Range"))
		body := map[string]string{"/api/v4/files/long": long, "/api/v4/files/short": "hello\n", "/api/v4/files/bin": "ab\x00cd"}[r.URL.Path]
		http.ServeContent(w, r, "x.txt", time.Time{}, strings.NewReader(body))
	})
	resp, body := e.get("/media/1/text/long")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "1", resp.Header.Get("X-Truncated"))
	assert.Equal(t, strings.Repeat("a", TextLimit-1), string(body), "a character cut in half is dropped")

	resp, body = e.get("/media/1/text/short")
	require.Equal(t, 200, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("X-Truncated"))
	assert.Equal(t, "hello\n", string(body))

	resp, _ = e.get("/media/1/text/bin")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
}

func TestEmojiResolvesNameToID(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/emoji/e1/image", r.URL.Path)
		_, _ = w.Write(pngOf(8, 8))
	})
	resp, _ := e.get("/media/1/emoji/partyparrot")
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "private, max-age=3600", resp.Header.Get("Cache-Control"))
	resp, _ = e.get("/media/1/emoji/nope")
	assert.Equal(t, 404, resp.StatusCode)
	assert.Equal(t, 1, e.origin.count("/api/v4/emoji/e1/image"))
}

func TestEvictionKeepsTheCacheUnderItsCapLeastRecentlyUsedFirst(t *testing.T) {
	e := newEnv(t, 3000, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fakePNG(1000)) })
	for _, u := range []string{"a1", "a2", "a3"} {
		e.get("/media/1/avatar/" + u)
	}
	e.get("/media/1/avatar/a1") // a1 is used again: a2 is now the oldest
	e.get("/media/1/avatar/a4")
	assert.Equal(t, int64(3000), e.cache.Size())
	e.get("/media/1/avatar/a1")
	assert.Equal(t, 1, e.origin.count("/api/v4/users/a1/image"), "a1 stayed")
	e.get("/media/1/avatar/a2")
	assert.Equal(t, 2, e.origin.count("/api/v4/users/a2/image"), "a2 was evicted")
	files, _ := filepath.Glob(filepath.Join(e.dir, "*.bin"))
	assert.Len(t, files, 3)
}

func TestIndexSurvivesRestartAndLeftoversAreRemoved(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngOf(2, 2)) })
	e.get("/media/1/avatar/u1?v=1")
	require.NoError(t, os.WriteFile(filepath.Join(e.dir, "x.tmp"), []byte("half"), 0o600))
	e.open(0, time.Minute)
	resp, _ := e.get("/media/1/avatar/u1?v=1")
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 1, e.origin.count("/api/v4/users/u1/image"))
	_, err := os.Stat(filepath.Join(e.dir, "x.tmp"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Positive(t, e.cache.Size())
}

func TestFailuresAreCachedBriefly(t *testing.T) {
	fail := atomic.Int32{}
	fail.Store(http.StatusNotFound)
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) {
		if code := int(fail.Load()); code != 0 {
			w.WriteHeader(code)
			return
		}
		_, _ = w.Write(pngOf(2, 2))
	})
	resp, _ := e.get("/media/1/thumb/f1")
	assert.Equal(t, 404, resp.StatusCode)
	e.get("/media/1/thumb/f1")
	assert.Equal(t, 1, e.origin.count("/api/v4/files/f1/thumbnail"), "404 is remembered")
	e.clock.jump(6 * time.Minute)
	fail.Store(http.StatusInternalServerError)
	resp, _ = e.get("/media/1/thumb/f1")
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	e.get("/media/1/thumb/f1")
	assert.Equal(t, 2, e.origin.count("/api/v4/files/f1/thumbnail"), "a 5xx is remembered too")
	e.clock.jump(31 * time.Second)
	fail.Store(0)
	resp, _ = e.get("/media/1/thumb/f1")
	assert.Equal(t, 200, resp.StatusCode, "…but only for 30 s")
}

func TestUpstreamRefusalAndTimeout(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/files/slow/thumbnail" {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	e.open(0, 100*time.Millisecond)
	resp, _ := e.get("/media/1/thumb/f1")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp, _ = e.get("/media/1/thumb/slow")
	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
}

func TestBadRequests(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngOf(2, 2)) })
	for _, p := range []string{
		"/media/x/avatar/u1", "/media/0/avatar/u1", "/media/1/bogus/u1", "/media/1/avatar/..%2Fx",
		"/media/1/avatar/u1?v=abc", "/media/1/feed/f1?src=evil", "/media/1/avatar", "/media/1/avatar/u1/extra",
		"/media/1/emoji/bad%20name",
	} {
		resp, _ := e.get(p)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, p)
	}
	resp, _ := e.get("/media/2/avatar/u1")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a server that is not signed in")
	resp, err := http.Post(e.srv.URL+"/media/1/avatar/u1", "text/plain", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Zero(t, e.origin.count("/api/v4/users/u1/image"))
}

// switchOrigin reports ErrNoServer until the server signs in.
type switchOrigin struct {
	*testOrigin
	in atomic.Bool
}

func (o *switchOrigin) Get(ctx context.Context, serverID int64, path string, hdr http.Header) (*http.Response, error) {
	if !o.in.Load() {
		return nil, ErrNoServer
	}
	return o.testOrigin.Get(ctx, serverID, path, hdr)
}

func TestNotSignedInIsNotRemembered(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngOf(2, 2)) })
	o := &switchOrigin{testOrigin: e.origin}
	c, err := New(Options{Dir: t.TempDir(), Origin: o, Now: e.clock.now})
	require.NoError(t, err)
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	get := func() int {
		resp, err := http.Get(srv.URL + "/media/1/avatar/u1")
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusNotFound, get())
	o.in.Store(true)
	assert.Equal(t, http.StatusOK, get(), "pictures appear as soon as the server signs in")
}

func TestEmptyTextFile(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "x.txt", time.Time{}, strings.NewReader("")) // a range of nothing: 416
	})
	resp, body := e.get("/media/1/text/empty")
	require.Equal(t, 200, resp.StatusCode)
	assert.Empty(t, body)
	assert.Empty(t, resp.Header.Get("X-Truncated"))
}

func TestFileDeletedBehindOurBackIsFetchedAgain(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngOf(2, 2)) })
	e.get("/media/1/avatar/u1")
	files, _ := filepath.Glob(filepath.Join(e.dir, "*.bin"))
	require.Len(t, files, 1)
	require.NoError(t, os.Remove(files[0]))
	resp, _ := e.get("/media/1/avatar/u1")
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 2, e.origin.count("/api/v4/users/u1/image"))
	fi, err := os.Stat(files[0])
	require.NoError(t, err)
	assert.Equal(t, fi.Size(), e.cache.Size(), "the refetched object is counted once")
}
