package media

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testIcons is PostIcons over a fixed map: post id → its override.
type testIcons map[string]PostIcon

func (m testIcons) PostIcon(serverID int64, postID string) (PostIcon, bool) {
	if serverID != 1 {
		return PostIcon{}, false
	}
	ic, ok := m[postID]
	return ic, ok
}

// withIcons reopens the cache with post icons; allow (nil: the real
// guard) decides which addresses an external fetch may dial.
func (e *env) withIcons(icons PostIcons, allow func(netip.AddrPort) bool) {
	c, err := New(Options{Dir: e.dir, Origin: e.origin, Timeout: time.Minute, Now: e.clock.now, PostIcons: icons})
	require.NoError(e.t, err)
	if allow != nil {
		c.ext = newExternalClient(allow)
	}
	e.srv.Close()
	e.cache = c
	e.srv = httptest.NewServer(c)
	e.t.Cleanup(e.srv.Close)
}

// extServer is a host outside any Mattermost server (a webhook's icon
// host); it records what every request carried.
type extServer struct {
	*httptest.Server
	mu    sync.Mutex
	hits  map[string]int
	creds []string // Authorization / Cookie / X-Requested-With values seen
}

func newExt(t *testing.T, h http.HandlerFunc) *extServer {
	x := &extServer{hits: map[string]int{}}
	x.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		x.mu.Lock()
		x.hits[r.URL.Path]++
		for _, k := range []string{"Authorization", "Cookie", "X-Requested-With"} {
			if v := r.Header.Get(k); v != "" {
				x.creds = append(x.creds, k+": "+v)
			}
		}
		x.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(x.Close)
	return x
}

func (x *extServer) count(path string) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.hits[path]
}

func (x *extServer) total() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	n := 0
	for _, v := range x.hits {
		n += v
	}
	return n
}

func allowAll(netip.AddrPort) bool { return true }

func TestPostIconIsKeyedByPostID(t *testing.T) {
	img := pngOf(8, 8)
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(img) })
	e.withIcons(testIcons{
		"p1": {URL: "/static/images/icon.png", Base: e.origin.url, Live: true},
		"p2": {URL: "/static/images/icon.png", Base: e.origin.url, Live: true},
	}, nil)

	resp, body := e.get("/media/1/posticon/p1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, img, body)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.Equal(t, "private, max-age=3600", resp.Header.Get("Cache-Control"), "a post's icon may change with an edit")
	resp, _ = e.get("/media/1/posticon/p2")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, e.origin.count("/static/images/icon.png"), "one picture for every post with the same icon URL")

	for _, p := range []string{"/media/1/posticon/nope", "/media/2/posticon/p1"} {
		resp, _ = e.get(p)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "%s: unknown post, or no override", p)
	}
	for _, p := range []string{"/media/1/posticon/a.b", "/media/1/posticon/" + url.PathEscape("https://x/y.png")} {
		resp, _ = e.get(p)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%s: the key is a post id, never a URL", p)
	}
}

func TestPostIconWithoutPostIconsIsNotFound(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngOf(1, 1)) })
	resp, _ := e.get("/media/1/posticon/p1")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRouteIcon(t *testing.T) {
	const ext = "https://gitlab.example/fox.png"
	cases := []struct {
		name, base, url string
		proxy           bool
		path, ext       string
		ok              bool
	}{
		{name: "relative", base: "https://mm.example", url: "/static/emoji/1f389.png", path: "/static/emoji/1f389.png", ok: true},
		{name: "relative keeps its query", base: "https://mm.example", url: "/plugins/x/icon.png?v=2", path: "/plugins/x/icon.png?v=2", ok: true},
		{name: "subpath", base: "https://mm.example/chat", url: "/chat/api/v4/emoji/e1/image", path: "/api/v4/emoji/e1/image", ok: true},
		{name: "outside the subpath", base: "https://mm.example/chat", url: "/static/x.png"},
		{name: "same origin absolute", base: "https://mm.example", url: "https://MM.example/static/x.png", path: "/static/x.png", ok: true},
		{name: "external", base: "https://mm.example", url: ext, ext: ext, ok: true},
		{name: "external through the image proxy", base: "https://mm.example", url: ext, proxy: true,
			path: "/api/v4/image?url=" + url.QueryEscape(ext), ok: true},
		{name: "same host, other port", base: "https://mm.example", url: "https://mm.example:8443/x.png", ext: "https://mm.example:8443/x.png", ok: true},
		{name: "scheme-relative", base: "https://mm.example", url: "//gitlab.example/fox.png", ext: ext, ok: true},
		{name: "http", base: "https://mm.example", url: "http://gitlab.example/fox.png", ext: "http://gitlab.example/fox.png", ok: true},
		{name: "fragment dropped", base: "https://mm.example", url: ext + "#x", ext: ext, ok: true},
		{name: "empty", base: "https://mm.example", url: ""},
		{name: "javascript", base: "https://mm.example", url: "javascript:alert(1)"},
		{name: "data", base: "https://mm.example", url: "data:image/png;base64,iVBORw0KGgo="},
		{name: "file", base: "https://mm.example", url: "file:///etc/passwd"},
		{name: "ftp", base: "https://mm.example", url: "ftp://gitlab.example/fox.png"},
		{name: "credentials in the URL", base: "https://mm.example", url: "https://u:p@gitlab.example/fox.png"},
		{name: "credentials, same host", base: "https://mm.example", url: "https://u:p@mm.example/static/x.png"},
		{name: "no host", base: "https://mm.example", url: "https:///x.png"},
		{name: "bad base", base: "", url: "/static/x.png"},
		{name: "too long", base: "https://mm.example", url: "https://gitlab.example/" + strings.Repeat("a", 4096)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := routeIcon(PostIcon{URL: c.url, Base: c.base, ImageProxy: c.proxy})
			require.Equal(t, c.ok, ok)
			assert.Equal(t, c.path, r.path)
			assert.Equal(t, c.ext, r.ext)
		})
	}
}

func TestPostIconExternalSendsNoCredentials(t *testing.T) {
	img := pngOf(8, 8)
	x := newExt(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(img) })
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	e.withIcons(testIcons{"p1": {URL: x.URL + "/fox.png", Base: e.origin.url, Live: true}}, allowAll)
	resp, body := e.get("/media/1/posticon/p1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, img, body)
	assert.Equal(t, 1, x.count("/fox.png"))
	assert.Empty(t, x.creds, "no token, cookie or API marker goes to a webhook's host")
	assert.Zero(t, e.origin.count("/fox.png"), "not through the Mattermost server")
}

func TestPostIconExternalRefusesPrivateAddresses(t *testing.T) {
	x := newExt(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngOf(1, 1)) })
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	e.withIcons(testIcons{"p1": {URL: x.URL + "/fox.png", Base: "https://mm.example", Live: true}}, nil) // the real guard
	resp, _ := e.get("/media/1/posticon/p1")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "127.0.0.1 is refused at dial time")
	resp, _ = e.get("/media/1/posticon/p1")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Zero(t, x.total(), "nothing reached the loopback host")
}

func TestPublicAddr(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1",
		"0.0.0.0", "0.1.2.3", "255.255.255.255", "224.0.0.1", "192.0.2.1", "198.18.0.1", "240.0.0.1",
		"::", "::1", "fe80::1", "fc00::1", "fd12::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
		"64:ff9b::a00:1", "64:ff9b::7f00:1", "2002:a00:1::1", "2001::1", "2001:db8::1"} {
		assert.False(t, publicAddr(netip.MustParseAddr(a)), a)
	}
	for _, a := range []string{"8.8.8.8", "140.82.112.3", "2606:4700::1111", "::ffff:8.8.8.8", "64:ff9b::808:808"} {
		assert.True(t, publicAddr(netip.MustParseAddr(a)), a)
	}
}

func TestPostIconExternalRedirects(t *testing.T) {
	img := pngOf(4, 4)
	inner := newExt(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(img) })
	x := newExt(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/one":
			http.Redirect(w, r, "/img", http.StatusFound)
		case "/img":
			_, _ = w.Write(img)
		case "/r1", "/r2", "/r3", "/r4":
			http.Redirect(w, r, "/r"+string(rune(r.URL.Path[2]+1)), http.StatusFound)
		case "/file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/lan":
			http.Redirect(w, r, inner.URL+"/img", http.StatusFound)
		}
	})
	port := netip.MustParseAddrPort(strings.TrimPrefix(x.URL, "http://")).Port()
	onlyX := func(ap netip.AddrPort) bool { return ap.Port() == port } // inner stands for the LAN
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	icons := testIcons{}
	for _, p := range []string{"one", "r1", "file", "lan"} {
		icons[p] = PostIcon{URL: x.URL + "/" + p, Base: "https://mm.example", Live: true}
	}
	e.withIcons(icons, onlyX)

	resp, body := e.get("/media/1/posticon/one")
	require.Equal(t, http.StatusOK, resp.StatusCode, "one redirect is followed")
	assert.Equal(t, img, body)
	resp, _ = e.get("/media/1/posticon/r1")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "more than maxRedirects is refused")
	assert.Zero(t, x.count("/r5"))
	resp, _ = e.get("/media/1/posticon/file")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a redirect off http(s) is refused")
	resp, _ = e.get("/media/1/posticon/lan")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "the redirect target's address is checked too")
	assert.Zero(t, inner.total())
}

func TestPostIconExternalRasterOnlySizeCapAndNegativeCache(t *testing.T) {
	x := newExt(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/svg":
			w.Header().Set("Content-Type", "image/png") // lies: the bytes decide
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		case "/big":
			_, _ = w.Write(fakePNG(3 << 20))
		case "/bomb":
			_, _ = w.Write(hugePNG(20000, 20000))
		case "/gone":
			http.NotFound(w, r)
		}
	})
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	icons := testIcons{}
	for _, p := range []string{"svg", "big", "bomb", "gone"} {
		icons[p] = PostIcon{URL: x.URL + "/" + p, Base: "https://mm.example", Live: true}
	}
	e.withIcons(icons, allowAll)
	want := map[string]int{"svg": http.StatusUnsupportedMediaType, "big": http.StatusRequestEntityTooLarge,
		"bomb": http.StatusRequestEntityTooLarge, "gone": http.StatusNotFound}
	for p, status := range want {
		for range 2 {
			resp, _ := e.get("/media/1/posticon/" + p)
			assert.Equal(t, status, resp.StatusCode, p)
		}
		assert.Equal(t, 1, x.count("/"+p), "%s: the failure is remembered", p)
	}
	assert.Zero(t, e.cache.Size())
}

func TestPostIconThroughTheImageProxy(t *testing.T) {
	img := pngOf(8, 8)
	const ext = "https://gitlab.example/fox.png"
	var got url.Values
	var mu sync.Mutex
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.URL.Query()
		mu.Unlock()
		_, _ = w.Write(img)
	})
	e.withIcons(testIcons{"p1": {URL: ext, Base: e.origin.url, ImageProxy: true, Live: true}}, nil)
	resp, body := e.get("/media/1/posticon/p1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, img, body)
	assert.Equal(t, 1, e.origin.count("/api/v4/image"), "fetched by the server, with the session, as the webapp does")
	mu.Lock()
	assert.Equal(t, ext, got.Get("url"))
	mu.Unlock()
}

// liveIcons is one post's icon whose server goes live and offline.
type liveIcons struct {
	ic   PostIcon
	live atomic.Bool
}

func (l *liveIcons) PostIcon(serverID int64, postID string) (PostIcon, bool) {
	ic := l.ic
	ic.Live = l.live.Load()
	return ic, serverID == 1 && postID == "p1"
}

func TestPostIconFetchesOnlyWhileLive(t *testing.T) {
	img := pngOf(8, 8)
	x := newExt(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(img) })
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	icons := &liveIcons{ic: PostIcon{URL: x.URL + "/fox.png", Base: "https://mm.example"}}
	c, err := New(Options{Dir: e.dir, Origin: e.origin, Timeout: time.Minute, Now: e.clock.now, PostIcons: icons})
	require.NoError(t, err)
	c.ext = newExternalClient(allowAll)
	e.srv.Close()
	e.cache, e.srv = c, httptest.NewServer(c)
	t.Cleanup(e.srv.Close)

	resp, _ := e.get("/media/1/posticon/p1")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "offline: no fetch")
	assert.Zero(t, x.total())
	icons.live.Store(true)
	resp, _ = e.get("/media/1/posticon/p1")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the offline refusal was not remembered")
	icons.live.Store(false)
	resp, _ = e.get("/media/1/posticon/p1")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "offline again: served from disk")
	assert.Equal(t, 1, x.total())
}
