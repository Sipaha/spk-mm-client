package media

import (
	"context"
	"errors"
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

	"github.com/spk/spk-mm-client/internal/mm/rest"
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

// iconLookup is the PostIcon half of PostIcons; authIcons adds GetIcon
// the way api.Service does it: the server's REST client with a token, on
// a client that follows only the redirects the cache allows.
type iconLookup interface {
	PostIcon(serverID int64, postID string) (PostIcon, bool)
}

type authIcons struct {
	iconLookup
	url string
	tr  http.RoundTripper // nil: http.DefaultTransport
}

func (a authIcons) GetIcon(ctx context.Context, serverID int64, path string, redirect func(*http.Request, []*http.Request) error) (*http.Response, error) {
	if serverID != 1 {
		return nil, ErrNoServer
	}
	return rest.New(a.url, "tok", &http.Client{Transport: a.tr, CheckRedirect: redirect}).Stream(ctx, path, nil)
}

// noDNS: tests never resolve a server's host for the intranet rule
// unless they say so.
func noDNS(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no DNS in tests") }

// withIcons reopens the cache with post icons; allow (nil: the real
// guards) decides which addresses an external fetch may dial.
func (e *env) withIcons(icons iconLookup, allow func(netip.AddrPort) bool) {
	e.openIcons(authIcons{iconLookup: icons, url: e.origin.url}, allow)
}

func (e *env) openIcons(icons PostIcons, allow func(netip.AddrPort) bool) {
	c, err := New(Options{Dir: e.dir, Origin: e.origin, Timeout: time.Minute, Now: e.clock.now, PostIcons: icons})
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
		{name: "relative keeps its query", base: "https://mm.example", url: "/static/x/icon.png?v=2", path: "/static/x/icon.png?v=2", ok: true},
		{name: "a custom emoji", base: "https://mm.example", url: "/api/v4/emoji/e1/image", path: "/api/v4/emoji/e1/image", ok: true},
		{name: "the image endpoint", base: "https://mm.example", url: "/api/v4/image?url=https%3A%2F%2Fx%2Fy.png", path: "/api/v4/image?url=https%3A%2F%2Fx%2Fy.png", ok: true},
		{name: "not an image path of the server", base: "https://mm.example", url: "/api/v4/users/me"},
		{name: "a plugin route", base: "https://mm.example", url: "/plugins/x/icon.png"},
		{name: "more below an emoji", base: "https://mm.example", url: "/api/v4/emoji/e1/image/x"},
		{name: "a bad emoji id", base: "https://mm.example", url: "/api/v4/emoji/e.1/image"},
		{name: "the static root itself", base: "https://mm.example", url: "/static/"},
		{name: "encoded dots over the subpath", base: "https://mm.example/chat", url: "/chat/%2e%2e/admin/x.png"},
		{name: "encoded dots, upper case", base: "https://mm.example", url: "/static/%2E%2E/api/v4/users/me"},
		{name: "encoded slash", base: "https://mm.example", url: "/static/..%2fapi/x.png"},
		{name: "encoded slash, upper case", base: "https://mm.example", url: "/static/a%2Fb.png"},
		{name: "encoded backslash", base: "https://mm.example", url: "/static/a%5cb.png"},
		{name: "backslash", base: "https://mm.example", url: "/static/a\\b.png"},
		{name: "plain dot segments", base: "https://mm.example", url: "/static/../api/v4/users/me"},
		{name: "only the path is checked", base: "https://mm.example", url: "/static/x.png?a=..%2f", path: "/static/x.png?a=..%2f", ok: true},
		{name: "default port spelled out", base: "https://mm.example", url: "https://mm.example:443/static/x.png", path: "/static/x.png", ok: true},
		{name: "trailing dot", base: "https://mm.example", url: "https://mm.example./static/x.png", path: "/static/x.png", ok: true},
		{name: "subpath", base: "https://mm.example/chat", url: "/chat/api/v4/emoji/e1/image", path: "/api/v4/emoji/e1/image", ok: true},
		{name: "outside the subpath", base: "https://mm.example/chat", url: "/static/x.png"},
		{name: "same origin absolute", base: "https://mm.example", url: "https://MM.example/static/x.png", path: "/static/x.png", ok: true},
		{name: "external", base: "https://mm.example", url: ext, ext: ext, ok: true},
		{name: "external through the image proxy", base: "https://mm.example", url: ext, proxy: true,
			path: "/api/v4/image?url=" + url.QueryEscape(ext), ok: true},
		{name: "same host, other port", base: "https://mm.example", url: "https://mm.example:8443/x.png"},
		{name: "same host, http", base: "https://mm.example", url: "http://mm.example/static/x.png"},
		{name: "same host, http, image proxy", base: "https://mm.example", url: "http://mm.example/x.png", proxy: true},
		{name: "same host, other port, image proxy", base: "https://mm.example", url: "https://mm.example:8443/x.png", proxy: true},
		{name: "a subdomain is another host", base: "https://mm.example", url: "https://cdn.mm.example/x.png", ext: "https://cdn.mm.example/x.png", ok: true},
		{name: "IDN host", base: "https://mm.example", url: "https://xn--e1afmkfd.example/x.png", ext: "https://xn--e1afmkfd.example/x.png", ok: true},
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
	e.withIcons(testIcons{"p1": {URL: x.URL + "/fox.png", Base: "https://mm.example", Live: true}}, allowAll)
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

func TestAllowedAddr(t *testing.T) {
	never := []string{"127.0.0.1", "169.254.169.254", "0.0.0.0", "0.1.2.3", "255.255.255.255", "224.0.0.1",
		"192.0.0.8", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1",
		"::", "::1", "fe80::1", "fe80::1%eth0", "ff02::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::7f00:1", "64:ff9b::7f00:1%x", "2002:a00:1::1", "2002:c0a8:101::1%x", "2001::1", "2001:db8::1",
		// M1: IPv4-compatible, local-use NAT64, site-local, discard, IPv4-translated, benchmarking, ORCHID.
		"::a00:1", "::808:808", "64:ff9b:1::a00:1", "fec0::1", "100::1", "::ffff:0:a00:1", "2001:2::1", "2001:10::1", "2001:20::1"}
	intranetOnly := []string{"10.1.2.3", "172.16.0.1", "192.168.1.1", "100.64.0.1", "fc00::1", "fd12::1",
		"::ffff:10.0.0.1", "64:ff9b::a00:1", "64:ff9b::a00:1%x", "fd12::1%eth0"}
	public := []string{"8.8.8.8", "140.82.112.3", "2606:4700::1111", "::ffff:8.8.8.8", "64:ff9b::808:808", "2606:4700::1111%x"}
	for _, a := range never {
		assert.False(t, allowedAddr(netip.MustParseAddr(a), false), a)
		assert.False(t, allowedAddr(netip.MustParseAddr(a), true), "%s: never, not even for an intranet server", a)
	}
	for _, a := range intranetOnly {
		assert.False(t, allowedAddr(netip.MustParseAddr(a), false), a)
		assert.True(t, allowedAddr(netip.MustParseAddr(a), true), "%s: an intranet server's webhooks may use it", a)
	}
	for _, a := range public {
		assert.True(t, allowedAddr(netip.MustParseAddr(a), false), a)
		assert.True(t, allowedAddr(netip.MustParseAddr(a), true), a)
	}
}

func TestPrivateHost(t *testing.T) {
	for _, a := range []string{"10.0.0.5", "172.20.1.1", "192.168.0.10", "100.64.1.1", "fd00::5", "::ffff:10.0.0.5"} {
		assert.True(t, privateAddr(netip.MustParseAddr(a)), a)
	}
	for _, a := range []string{"127.0.0.1", "::1", "8.8.8.8", "169.254.1.1", "fe80::1", "2606:4700::1111"} {
		assert.False(t, privateAddr(netip.MustParseAddr(a)), "%s: not an intranet deployment", a)
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
	e.withIcons(icons, allowAll)

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

func TestIconRedirectPolicy(t *testing.T) {
	check := iconRedirect("https://mm.example/chat")
	req := func(u string) *http.Request { r, _ := http.NewRequest("GET", u, nil); return r }
	via := []*http.Request{req("https://mm.example/chat/static/a.png")}
	assert.NoError(t, check(req("https://mm.example/chat/static/b.png"), via), "same origin, an image path")
	assert.NoError(t, check(req("https://MM.example:443/chat/api/v4/emoji/e1/image"), via))
	for _, u := range []string{
		"http://mm.example/chat/static/b.png",       // plain http: the token in cleartext
		"https://mm.example:8443/chat/static/b.png", // another port
		"https://evil.example/chat/static/b.png",    // another host
		"https://cdn.mm.example/chat/static/b.png",  // a subdomain (Go would forward the token there)
		"https://mm.example/static/b.png",           // outside the base path
		"https://mm.example/chat/api/v4/users/me",   // not an image path
		"https://mm.example/chat/static/%2e%2e/x",   // encoded dots
		"https://u:p@mm.example/chat/static/b.png",  // userinfo
		"file:///etc/passwd",
	} {
		assert.ErrorIs(t, check(req(u), via), errBlocked, u)
	}
	long := []*http.Request{via[0], via[0], via[0], via[0]}
	assert.ErrorIs(t, check(req("https://mm.example/chat/static/b.png"), long), errBlocked, "at most maxRedirects")
}

// C1: a redirect answered by the server (a same-origin path or its image
// proxy) never takes the session anywhere but the server itself.
func TestPostIconServerRedirectsNeverCarryTheToken(t *testing.T) {
	img := pngOf(4, 4)
	x := newExt(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(img) }) // same host, another port
	xPort := strings.TrimPrefix(x.URL, "http://127.0.0.1:")
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/static/to-port":
			http.Redirect(w, r, x.URL+"/port.png", http.StatusFound)
		case "/static/to-host":
			http.Redirect(w, r, "http://localhost:"+xPort+"/host.png", http.StatusFound)
		case "/static/to-api":
			http.Redirect(w, r, "/api/v4/users/me", http.StatusFound)
		case "/api/v4/image": // the image proxy redirecting (MM does for a URL of its own host)
			http.Redirect(w, r, x.URL+"/proxy.png", http.StatusFound)
		case "/static/to-self":
			http.Redirect(w, r, "/static/img.png", http.StatusFound)
		case "/static/img.png":
			_, _ = w.Write(img)
		}
	})
	base := e.origin.url
	e.withIcons(testIcons{
		"port":  {URL: "/static/to-port", Base: base, Live: true},
		"host":  {URL: "/static/to-host", Base: base, Live: true},
		"api":   {URL: "/static/to-api", Base: base, Live: true},
		"proxy": {URL: "https://gitlab.example/fox.png", Base: base, ImageProxy: true, Live: true},
		"self":  {URL: "/static/to-self", Base: base, Live: true},
	}, allowAll)
	for _, p := range []string{"port", "host", "api", "proxy"} {
		resp, _ := e.get("/media/1/posticon/" + p)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, p)
	}
	assert.Zero(t, x.total(), "the redirect is refused before anything is sent")
	assert.Zero(t, e.origin.count("/api/v4/users/me"))
	resp, body := e.get("/media/1/posticon/self")
	require.Equal(t, http.StatusOK, resp.StatusCode, "a redirect within the server's image paths is followed")
	assert.Equal(t, img, body)

	// Not vacuous: without the policy Go itself forwards the token to the
	// same host on another port.
	r, err := rest.New(base, "tok", &http.Client{}).Stream(context.Background(), "/static/to-port", nil)
	require.NoError(t, err)
	_ = r.Body.Close()
	x.mu.Lock()
	assert.Contains(t, x.creds, "Authorization: Bearer tok")
	x.mu.Unlock()
}

// C1 over TLS: an https server redirecting to plain http on its own host.
func TestPostIconServerRedirectToPlainHTTPIsRefused(t *testing.T) {
	x := newExt(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngOf(1, 1)) })
	xPort := strings.TrimPrefix(x.URL, "http://127.0.0.1:")
	o := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:"+xPort+"/clear.png", http.StatusFound)
	}))
	t.Cleanup(o.Close)
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	e.openIcons(authIcons{iconLookup: testIcons{
		"rel":   {URL: "/static/x.png", Base: o.URL, Live: true},
		"proxy": {URL: "https://gitlab.example/fox.png", Base: o.URL, ImageProxy: true, Live: true},
	}, url: o.URL, tr: o.Client().Transport}, allowAll)
	for _, p := range []string{"rel", "proxy"} {
		resp, _ := e.get("/media/1/posticon/" + p)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, p)
	}
	assert.Zero(t, x.total())
	assert.Empty(t, x.creds)
}

// I2: a 401/403 on an icon path is the path's answer, not a dead session:
// no new sign-in (GetIcon never reports it), and it is remembered.
func TestPostIconUnauthorizedIsRememberedNotSignIn(t *testing.T) {
	e := newEnv(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/static/private.png" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	e.withIcons(testIcons{
		"p401": {URL: "/static/private.png", Base: e.origin.url, Live: true},
		"p403": {URL: "/static/forbidden.png", Base: e.origin.url, Live: true},
	}, nil)
	for _, p := range []string{"p401", "p403"} {
		for range 2 {
			resp, _ := e.get("/media/1/posticon/" + p)
			assert.Equal(t, http.StatusForbidden, resp.StatusCode, p)
		}
	}
	assert.Equal(t, 1, e.origin.count("/static/private.png"), "remembered")
	assert.Equal(t, 1, e.origin.count("/static/forbidden.png"))
}

// I4: external hosts have their own two fetch slots — a stalled one does
// not hold the ones avatars, previews and emoji use.
func TestPostIconStalledExternalHostDoesNotBlockOtherPictures(t *testing.T) {
	stall := make(chan struct{})
	var started atomic.Int32
	x := newExt(t, func(_ http.ResponseWriter, r *http.Request) {
		started.Add(1)
		select {
		case <-stall:
		case <-r.Context().Done():
		}
	})
	img := pngOf(4, 4)
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(img) })
	icons := testIcons{}
	for i := range 8 {
		id := "s" + string(rune('a'+i))
		icons[id] = PostIcon{URL: x.URL + "/" + id + ".png", Base: "https://mm.example", Live: true}
	}
	e.withIcons(icons, allowAll)
	t.Cleanup(func() { close(stall) }) // before the servers close: they wait for their requests
	for id := range icons {
		go func() {
			if resp, err := http.Get(e.srv.URL + "/media/1/posticon/" + id); err == nil {
				_ = resp.Body.Close()
			}
		}()
	}
	require.Eventually(t, func() bool { return started.Load() == extFetches }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond) // the other six are queued now
	assert.Equal(t, int32(extFetches), started.Load(), "no more than extFetches external fetches at once")
	done := make(chan int, 1)
	go func() {
		resp, _ := e.get("/media/1/avatar/u1?v=1")
		done <- resp.StatusCode
	}()
	select {
	case status := <-done:
		assert.Equal(t, http.StatusOK, status)
	case <-time.After(5 * time.Second):
		t.Fatal("an avatar waited for stalled external icons")
	}
}

// I4: an external host that fails is not asked again for 5 minutes.
func TestPostIconExternalFailureIsRememberedLonger(t *testing.T) {
	x := newExt(t, func(w http.ResponseWriter, _ *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close() // a broken connection: 502
	})
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	e.withIcons(testIcons{"p1": {URL: x.URL + "/x.png", Base: "https://mm.example", Live: true}}, allowAll)
	resp, _ := e.get("/media/1/posticon/p1")
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	e.clock.jump(2 * time.Minute)
	resp, _ = e.get("/media/1/posticon/p1")
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Equal(t, 1, x.total(), "still remembered after 2 minutes")
	e.clock.jump(4 * time.Minute)
	_, _ = e.get("/media/1/posticon/p1")
	assert.Equal(t, 2, x.total(), "asked again after 5 minutes")
}

// I5: private destinations only for a server that is itself in the
// intranet (its host resolves to a private address).
func TestPostIconPrivateTargetsOnlyForAnIntranetServer(t *testing.T) {
	img := pngOf(4, 4)
	x := newExt(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(img) })
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	e.withIcons(testIcons{
		"corp":    {URL: x.URL + "/corp.png", Base: "https://mm.corp", Live: true},
		"public":  {URL: x.URL + "/public.png", Base: "https://mm.example", Live: true},
		"literal": {URL: x.URL + "/literal.png", Base: "https://10.1.2.3:8065", Live: true},
		"nodns":   {URL: x.URL + "/nodns.png", Base: "https://mm.nodns", Live: true},
	}, nil)
	var mu sync.Mutex
	lookups := map[string]int{}
	e.cache.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		mu.Lock()
		lookups[host]++
		mu.Unlock()
		switch host {
		case "mm.corp":
			return []netip.Addr{netip.MustParseAddr("10.0.0.5")}, nil
		case "mm.example":
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return nil, errors.New("no such host")
	}
	// The strict client stays the real one; the intranet one is replaced
	// by one that also lets loopback through, standing for a LAN host.
	e.cache.extIntranet = newExternalClient(allowAll)
	for p, want := range map[string]int{"corp": 200, "literal": 200, "public": 403, "nodns": 403} {
		resp, _ := e.get("/media/1/posticon/" + p)
		assert.Equal(t, want, resp.StatusCode, p)
	}
	assert.Equal(t, 1, x.count("/corp.png"))
	assert.Equal(t, 1, x.count("/literal.png"))
	assert.Zero(t, x.count("/public.png"))
	mu.Lock()
	assert.Zero(t, lookups["10.1.2.3"], "an address needs no lookup")
	mu.Unlock()
}

// M2: the icon URL of a post is versioned by what it points at.
func TestPostIconAcceptsAVersion(t *testing.T) {
	img := pngOf(4, 4)
	e := newEnv(t, 0, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(img) })
	e.withIcons(testIcons{"p1": {URL: "/static/i.png", Base: e.origin.url, Live: true}}, nil)
	resp, _ := e.get("/media/1/posticon/p1?v=0a1b2c3d4e5f6a7b")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = e.get("/media/1/posticon/p1?v=0a1b2c3d4e5f6a7c")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, e.origin.count("/static/i.png"), "one picture whatever the version")
	resp, _ = e.get("/media/1/posticon/p1?v=../x")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
