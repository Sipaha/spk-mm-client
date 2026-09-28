package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// A webhook post may carry its own author picture (override_icon_url). The
// UI asks for it by POST id — /media/<srv>/posticon/<post id> — never by a
// URL: Go looks the post up (PostIcons), and the URL it fetches is only ever
// one a post on that server carries. Where it is fetched from:
//   - the server itself (a relative URL, or the server's origin): through
//     its REST client, with the session, like any other picture;
//   - elsewhere, with the server's image proxy on (HasImageProxy): through
//     /api/v4/image?url=, with the session, as the webapp does;
//   - elsewhere without it: directly, WITHOUT any credentials, http/https
//     only, public addresses only (checked on every dial, redirects
//     included — a hostile webhook must not reach into the user's LAN), a
//     few redirects at most, strict timeouts.
//
// The picture then goes through the same raster/size/pixel checks and disk
// cache as every other one, keyed by where it comes from (every post of a
// webhook shares one copy), with the same negative cache.

// PostIcon is what the cache needs to fetch a post's picture.
type PostIcon struct {
	URL        string // the post's override_icon_url
	Base       string // the server's base URL
	ImageProxy bool   // HasImageProxy: external pictures via /api/v4/image
	Live       bool   // the server is live: a picture not on disk may be fetched
}

// PostIcons finds the overridden picture of a post (implemented by
// api.Service over the server's state).
type PostIcons interface {
	// PostIcon is a held post's override when its server allows icon
	// overrides and the post qualifies (a webhook post showing its own
	// picture); false for an unknown post or one without it.
	PostIcon(serverID int64, postID string) (PostIcon, bool)
}

// iconRoute is where a post's picture comes from: path (an API path of the
// server, query included — its REST client) or ext (an absolute URL fetched
// without credentials).
type iconRoute struct {
	path string
	ext  string
	live bool
}

// id names the picture in the cache: where it comes from.
func (r iconRoute) id() string {
	sum := sha256.Sum256([]byte(r.path + "\x00" + r.ext))
	return hex.EncodeToString(sum[:])
}

const (
	maxIconURL   = 2048 // bytes of an override_icon_url
	maxRedirects = 3    // an external picture's redirects
)

// routeIcon decides where an override_icon_url is fetched from. The URL is
// resolved against the server's base URL as a browser showing the webapp
// would; only http(s) without credentials in it is accepted, and a URL of
// the server's host outside its base path is refused (its REST client only
// reaches below the base).
func routeIcon(ic PostIcon) (iconRoute, bool) {
	base, err := url.Parse(ic.Base)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || len(ic.URL) > maxIconURL {
		return iconRoute{}, false
	}
	ref, err := url.Parse(strings.TrimSpace(ic.URL))
	if err != nil || ic.URL == "" {
		return iconRoute{}, false
	}
	basePath := strings.TrimSuffix(base.Path, "/")
	dir := *base
	dir.Path, dir.RawPath = basePath+"/", ""
	u := dir.ResolveReference(ref)
	u.Fragment, u.RawFragment = "", ""
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" {
		return iconRoute{}, false
	}
	if u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) {
		rest, ok := strings.CutPrefix(u.EscapedPath(), basePath)
		if !ok || !strings.HasPrefix(rest, "/") {
			return iconRoute{}, false
		}
		if u.RawQuery != "" {
			rest += "?" + u.RawQuery
		}
		return iconRoute{path: rest}, true
	}
	if ic.ImageProxy {
		return iconRoute{path: "/api/v4/image?url=" + url.QueryEscape(u.String())}, true
	}
	return iconRoute{ext: u.String()}, true
}

// errBlocked: an external picture's address or redirect is not allowed.
var errBlocked = errors.New("media: address or redirect not allowed")

// postIcon resolves a posticon request: the cache id of its picture and
// where it comes from, or a status.
func (c *Cache) postIcon(q request) (string, iconRoute, int) {
	if c.o.PostIcons == nil {
		return "", iconRoute{}, http.StatusNotFound
	}
	ic, ok := c.o.PostIcons.PostIcon(q.server, q.key)
	if !ok {
		return "", iconRoute{}, http.StatusNotFound
	}
	r, ok := routeIcon(ic)
	if !ok {
		return "", iconRoute{}, http.StatusNotFound
	}
	r.live = ic.Live
	return r.id(), r, 0
}

// openIcon starts a posticon download. Every route needs the server live,
// the external one too: offline nothing is fetched (ErrNoServer, not
// remembered), and pictures on disk are still served.
func (c *Cache) openIcon(ctx context.Context, q request) (*http.Response, error) {
	if !q.icon.live {
		return nil, ErrNoServer
	}
	if q.icon.ext == "" {
		return c.o.Origin.Get(ctx, q.server, q.icon.path, nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q.icon.ext, nil)
	if err != nil {
		return nil, errNoObject
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp,image/bmp")
	req.Header.Set("User-Agent", "spk-mm-client")
	resp, err := c.ext.Do(req)
	if err != nil {
		if errors.Is(err, errBlocked) {
			return nil, errBlocked
		}
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp, nil
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden, http.StatusUnauthorized:
		err = errNoObject
	default:
		err = errUpstream
	}
	_ = resp.Body.Close()
	return nil, err
}

// newExternalClient fetches pictures from outside any Mattermost server:
// no cookie jar, no environment proxy (the address check must see the real
// target), every dial checked by allow (the address a name resolved to —
// so a name re-pointed at the LAN is caught too), at most maxRedirects
// http(s) redirects, strict timeouts. Callers set no credentials.
func newExternalClient(allow func(netip.AddrPort) bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !allow(netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())) {
			return errBlocked
		}
		return nil
	}}
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            d.DialContext,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		MaxIdleConns:           4,
		IdleConnTimeout:        30 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.User != nil {
				return errBlocked
			}
			return nil
		}}
}

// publicDial is the address check of external pictures.
func publicDial(ap netip.AddrPort) bool { return publicAddr(ap.Addr()) }

// nonPublic are special-purpose ranges IsGlobalUnicast/IsPrivate let
// through: "this network", CGNAT, IETF protocol assignments, documentation,
// benchmarking, reserved; 6to4, Teredo and IPv6 documentation (NAT64 is
// checked by the IPv4 address it carries).
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2001:db8::/32"),
}

var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// publicAddr: a global unicast address outside private, loopback,
// link-local and special-purpose ranges.
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if nat64.Contains(a) {
		b := a.As16()
		return publicAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
	}
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}
