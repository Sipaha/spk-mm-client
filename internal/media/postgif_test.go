package media

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/rest"
)

func gifOf(w, h int) []byte {
	pal := color.Palette{color.RGBA{20, 40, 80, 255}, color.RGBA{240, 120, 40, 255}}
	f1 := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	f2 := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	f2.SetColorIndex(0, 0, 1)
	var b bytes.Buffer
	_ = gif.EncodeAll(&b, &gif.GIF{Image: []*image.Paletted{f1, f2}, Delay: []int{10, 10}})
	return b.Bytes()
}

type gifLookup map[string]PostIcon // key: postID + "/" + key

func (m gifLookup) PostGif(serverID int64, postID, key string) (PostIcon, bool) {
	if serverID != 1 {
		return PostIcon{}, false
	}
	ic, ok := m[postID+"/"+key]
	return ic, ok
}

type authGifs struct {
	gifLookup
	url string
	tr  http.RoundTripper
}

func (a authGifs) GetIcon(ctx context.Context, serverID int64, path string, redirect func(*http.Request, []*http.Request) error) (*http.Response, error) {
	if serverID != 1 {
		return nil, ErrNoServer
	}
	return rest.New(a.url, "tok", &http.Client{Transport: a.tr, CheckRedirect: redirect}).Stream(ctx, path, nil)
}

func (e *env) withGifs(gifs gifLookup, allow func(netip.AddrPort) bool) {
	c, err := New(Options{Dir: e.dir, Origin: e.origin, Timeout: time.Minute, Now: e.clock.now, PostGifs: authGifs{gifLookup: gifs, url: e.origin.url}})
	require.NoError(e.t, err)
	c.lookup = noDNS
	if allow != nil {
		c.ext, c.extIntranet = newExternalClient(allow), newExternalClient(allow)
	}
	e.srv.Close()
	e.cache = c
	e.srv = httptest.NewServer(c)
	e.t.Cleanup(e.srv.Close)
}

func TestPostGifValidationHeadersAndSameOrigin(t *testing.T) {
	anim := gifOf(12, 8)
	const k1 = "0123456789abcdef"
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/static/sample.gif":
			_, _ = w.Write(anim)
		case "/static/not-a-gif.png":
			_, _ = w.Write(pngOf(8, 8))
		}
	})
	e.withGifs(gifLookup{
		"p1/" + k1: {URL: "/static/sample.gif", Base: e.origin.url, Live: true},
		"p2/" + k1: {URL: "/static/sample.gif", Base: e.origin.url, Live: true},
		"p3/" + k1: {URL: "/static/not-a-gif.png", Base: e.origin.url, Live: true},
	}, nil)

	// Bad / missing ?k= is 400.
	for _, bad := range []string{
		"/media/1/postgif/p1",
		"/media/1/postgif/p1?k=short",
		"/media/1/postgif/p1?k=0123456789ABCDEF", // uppercase rejected
		"/media/1/postgif/p1?k=../0123456789abc",
	} {
		resp, _ := e.get(bad)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, bad)
	}

	// Valid GIF served with security headers and Cache-Control.
	resp, body := e.get("/media/1/postgif/p1?k=" + k1)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, anim, body, "animated GIF passed through without downscale")
	assert.Equal(t, "image/gif", resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "default-src 'none'; sandbox", resp.Header.Get("Content-Security-Policy"))
	assert.Equal(t, "private, max-age=3600", resp.Header.Get("Cache-Control"))

	// Second post referencing same GIF hits disk cache (one upstream fetch).
	resp, body = e.get("/media/1/postgif/p2?k=" + k1)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, anim, body)
	assert.Equal(t, 1, e.origin.count("/static/sample.gif"))

	// PNG/non-GIF is refused with 415 even though it is valid raster.
	resp, _ = e.get("/media/1/postgif/p3?k=" + k1)
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)

	// Unknown post or key -> 404.
	resp, _ = e.get("/media/1/postgif/unknown?k=" + k1)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = e.get("/media/1/postgif/p1?k=fedcba9876543210")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPostGifExternalGuardsCamoAndSizeCap(t *testing.T) {
	anim := gifOf(16, 12)
	const k1 = "abcdef0123456789"
	x := newExt(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.gif":
			_, _ = w.Write(anim)
		case "/svg.gif":
			w.Header().Set("Content-Type", "image/gif")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
		case "/huge.gif":
			// > 16 MiB GIF header + zero padding
			_, _ = w.Write(anim)
			chunk := make([]byte, 1<<20)
			for range 17 {
				_, _ = w.Write(chunk)
			}
		}
	})

	// Real dial guard blocks loopback/private external host.
	eBlocked := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	eBlocked.withGifs(gifLookup{
		"p1/" + k1: {URL: x.URL + "/ok.gif", Base: "https://mm.example", Live: true},
	}, nil)
	resp, _ := eBlocked.get("/media/1/postgif/p1?k=" + k1)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Zero(t, x.total())

	// Allowed external fetch sends no credentials, enforces gifOnly and 16 MiB cap.
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/image" {
			http.Redirect(w, r, x.URL+"/ok.gif", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusTeapot)
	})
	e.withGifs(gifLookup{
		"ok/" + k1:   {URL: x.URL + "/ok.gif", Base: "https://mm.example", Live: true},
		"svg/" + k1:  {URL: x.URL + "/svg.gif", Base: "https://mm.example", Live: true},
		"huge/" + k1: {URL: x.URL + "/huge.gif", Base: "https://mm.example", Live: true},
		"camo/" + k1: {URL: "https://giphy.example/cat.gif", Base: e.origin.url, ImageProxy: true, Live: true},
	}, allowAll)

	resp, body := e.get("/media/1/postgif/ok?k=" + k1)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, anim, body)
	assert.Empty(t, x.creds)

	resp, _ = e.get("/media/1/postgif/svg?k=" + k1)
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)

	resp, _ = e.get("/media/1/postgif/huge?k=" + k1)
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)

	resp, body = e.get("/media/1/postgif/camo?k=" + k1)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, anim, body)
	assert.Empty(t, x.creds, "camo handover never sends session credentials")
}

type liveGifLookup struct {
	ic   PostIcon
	live atomic.Bool
}

func (l *liveGifLookup) PostGif(serverID int64, postID, key string) (PostIcon, bool) {
	ic := l.ic
	ic.Live = l.live.Load()
	return ic, serverID == 1 && postID == "p1" && key == "1111222233334444"
}

func (l *liveGifLookup) GetIcon(context.Context, int64, string, func(*http.Request, []*http.Request) error) (*http.Response, error) {
	return nil, ErrNoServer
}

func TestPostGifFetchesOnlyWhileLiveAndDeadlines(t *testing.T) {
	assert.Equal(t, 10*time.Second, extDeadline(KindPostIcon))
	assert.Equal(t, 30*time.Second, extDeadline(KindPostGif))

	anim := gifOf(8, 8)
	x := newExt(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(anim) })
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	lg := &liveGifLookup{ic: PostIcon{URL: x.URL + "/a.gif", Base: "https://mm.example"}}
	c, err := New(Options{Dir: e.dir, Origin: e.origin, Timeout: time.Minute, Now: e.clock.now, PostGifs: lg})
	require.NoError(t, err)
	c.lookup = noDNS
	c.ext, c.extIntranet = newExternalClient(allowAll), newExternalClient(allowAll)
	e.srv.Close()
	e.cache = c
	e.srv = httptest.NewServer(c)
	t.Cleanup(e.srv.Close)

	// Offline: 404, not negative-cached.
	resp, _ := e.get("/media/1/postgif/p1?k=1111222233334444")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Zero(t, x.total())

	// Online: fetches once.
	lg.live.Store(true)
	resp, body := e.get("/media/1/postgif/p1?k=1111222233334444")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, anim, body)

	// Offline again: served from disk cache.
	lg.live.Store(false)
	resp, body = e.get("/media/1/postgif/p1?k=1111222233334444")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, anim, body)
	assert.Equal(t, 1, x.total())
}
