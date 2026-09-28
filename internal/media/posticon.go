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
	"regexp"
	"strings"
	"syscall"
	"time"
)

// A webhook post may carry its own author picture (override_icon_url). The
// UI asks for it by POST id — /media/<srv>/posticon/<post id> — never by a
// URL: Go looks the post up (PostIcons), and the URL it fetches is only ever
// one a post on that server carries. Where it is fetched from:
//   - the server itself (its exact origin — scheme, host and port — under
//     its base path), and only its image paths (iconPath: /static/…,
//     /api/v4/emoji/<id>/image, /api/v4/image): with the session, through
//     PostIcons.GetIcon — a redirect is followed only to another such path of
//     the same origin (iconRedirect), a 401/403 is the path's answer (no new
//     sign-in) and is remembered;
//   - the server's host on another scheme or port: refused (a redirect or a
//     request there would take the token in cleartext or to another service);
//   - elsewhere, with the server's image proxy on (HasImageProxy): through
//     /api/v4/image?url=, with the session, as the webapp does; when that
//     endpoint redirects off the origin (an atmos/camo proxy), the target
//     is fetched like the next case — without the session;
//   - elsewhere without it: directly, WITHOUT any credentials, http/https
//     only, public addresses only — private ranges too when the server itself
//     is on a private address (an intranet, where the webapp's browser
//     reaches them as well) — checked on every dial, redirects included, a
//     few redirects at most, strict timeouts, two fetches at a time (not the
//     slots other pictures use) and failures remembered for negTTL.
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
	// GetIcon GETs an image path of a live server with its session, on a
	// client that follows only the redirects redirect allows. Unlike
	// Origin.Get a 401 does not ask for a new sign-in: the path is chosen by
	// a post's author, not by us.
	GetIcon(ctx context.Context, serverID int64, path string, redirect func(*http.Request, []*http.Request) error) (*http.Response, error)
}

// iconRoute is where a post's picture comes from: path (an image path of the
// server, query included — GetIcon) or ext (an absolute URL fetched without
// credentials). base is the server's base URL (redirect checks, the
// intranet rule).
type iconRoute struct {
	path string
	ext  string
	base string
	live bool
}

// id names the picture in the cache: where it comes from.
func (r iconRoute) id() string {
	sum := sha256.Sum256([]byte(r.path + "\x00" + r.ext))
	return hex.EncodeToString(sum[:])
}

const (
	maxIconURL   = 2048 // bytes of an override_icon_url
	maxRedirects = 3    // an icon's redirects
)

// routeIcon decides where an override_icon_url is fetched from. The URL is
// resolved against the server's base URL as a browser showing the webapp
// would; only http(s) without credentials in it is accepted.
func routeIcon(ic PostIcon) (iconRoute, bool) {
	base, err := url.Parse(ic.Base)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || len(ic.URL) > maxIconURL {
		return iconRoute{}, false
	}
	ref, err := url.Parse(strings.TrimSpace(ic.URL))
	if err != nil || ic.URL == "" {
		return iconRoute{}, false
	}
	dir := *base
	dir.Path, dir.RawPath = strings.TrimSuffix(base.Path, "/")+"/", ""
	u := dir.ResolveReference(ref)
	u.Fragment, u.RawFragment = "", ""
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" {
		return iconRoute{}, false
	}
	if sameHostname(u, base) {
		// The server's own host: its exact origin and an image path, or
		// nothing — another scheme or port of it is not "external" (the
		// image proxy would redirect there with the token).
		if !sameOrigin(u, base) || dirtyPath(ref.EscapedPath()) {
			return iconRoute{}, false
		}
		rest, ok := iconPath(u, base)
		if !ok {
			return iconRoute{}, false
		}
		return iconRoute{path: rest, base: ic.Base}, true
	}
	if ic.ImageProxy {
		return iconRoute{path: "/api/v4/image?url=" + url.QueryEscape(u.String()), base: ic.Base}, true
	}
	return iconRoute{ext: u.String(), base: ic.Base}, true
}

var emojiImageRe = regexp.MustCompile(`^/api/v4/emoji/[A-Za-z0-9_-]{1,64}/image$`)

// iconPath is u's path below base's (query kept) when it is one of the
// server's image paths — /static/…, /api/v4/emoji/<id>/image,
// /api/v4/image — without dot segments or encoded slashes/backslashes.
func iconPath(u, base *url.URL) (string, bool) {
	p := u.EscapedPath()
	if dirtyPath(p) {
		return "", false
	}
	rest, ok := strings.CutPrefix(p, strings.TrimSuffix(base.EscapedPath(), "/"))
	switch {
	case !ok:
		return "", false
	case strings.HasPrefix(rest, "/static/") && len(rest) > len("/static/"), emojiImageRe.MatchString(rest), rest == "/api/v4/image":
	default:
		return "", false
	}
	if u.RawQuery != "" {
		rest += "?" + u.RawQuery
	}
	return rest, true
}

// dirtyPath: a path a proxy in front of the server could read differently
// than we check it — dot segments, encoded dots/slashes/backslashes, a
// backslash.
func dirtyPath(p string) bool {
	l := strings.ToLower(p)
	return strings.Contains(l, "%2e") || strings.Contains(l, "%2f") || strings.Contains(l, "%5c") ||
		strings.Contains(l, "\\") || strings.Contains(l, "..")
}

func sameHostname(a, b *url.URL) bool {
	return strings.EqualFold(strings.TrimSuffix(a.Hostname(), "."), strings.TrimSuffix(b.Hostname(), "."))
}

// sameOrigin: scheme, host and port (the default one spelled out or not).
func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && sameHostname(a, b) && port(a) == port(b)
}

func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// iconRedirect is the redirect policy of the session's icon fetches: only
// to an image path of the server's exact origin, at most maxRedirects —
// anything else is refused, never retried with the session. One exception:
// the image endpoint (/api/v4/image) redirecting off the origin is its
// atmos/camo proxy handing over — that answer is kept
// (http.ErrUseLastResponse: nothing is sent there with the session) and
// openIcon fetches the target like an external icon, without credentials.
func iconRedirect(base string) func(*http.Request, []*http.Request) error {
	b, err := url.Parse(base)
	return func(req *http.Request, via []*http.Request) error {
		if err != nil || len(via) == 0 || len(via) > maxRedirects {
			return errBlocked
		}
		if sameOrigin(req.URL, b) {
			if _, ok := iconPath(req.URL, b); !ok || req.URL.User != nil {
				return errBlocked
			}
			return nil
		}
		if from, ok := iconPath(via[len(via)-1].URL, b); ok && strings.SplitN(from, "?", 2)[0] == "/api/v4/image" {
			return http.ErrUseLastResponse
		}
		return errBlocked
	}
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
func (c *Cache) openIcon(ctx context.Context, q request, sl *slot) (*http.Response, error) {
	if !q.icon.live {
		return nil, ErrNoServer
	}
	if q.icon.ext != "" {
		return c.fetchExternal(ctx, q.icon.ext, q.icon.base)
	}
	resp, err := c.o.PostIcons.GetIcon(ctx, q.server, q.icon.path, iconRedirect(q.icon.base))
	if err != nil {
		if errors.Is(err, errBlocked) {
			return nil, errBlocked
		}
		return nil, err
	}
	if !isRedirect(resp.StatusCode) {
		return resp, nil
	}
	// The image endpoint's proxy handing over (iconRedirect): its target is
	// fetched without the session, by the external client with its guards,
	// its slots, its timeout, its redirect limit and its negative cache.
	loc, err := resp.Request.URL.Parse(resp.Header.Get("Location"))
	_ = resp.Body.Close()
	if err != nil || (loc.Scheme != "http" && loc.Scheme != "https") || loc.Host == "" || loc.User != nil || len(loc.String()) > maxIconURL {
		return nil, errBlocked
	}
	loc.Fragment, loc.RawFragment = "", ""
	sl.toExternal()
	return c.fetchExternal(ctx, loc.String(), q.icon.base)
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// fetchExternal GETs rawURL without any credentials: the strict client, or
// the intranet one for a server on a private address.
func (c *Cache) fetchExternal(ctx context.Context, rawURL, base string) (*http.Response, error) {
	ext := c.ext
	if c.intranet(ctx, base) {
		ext = c.extIntranet
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errNoObject
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp,image/bmp")
	req.Header.Set("User-Agent", "spk-mm-client")
	resp, err := ext.Do(req)
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

// intranetTTL: how long a server's "is it on a private address" answer is
// kept.
const intranetTTL = 10 * time.Minute

type intranetEntry struct {
	private bool
	until   time.Time
}

// intranet reports whether the server of base is itself on a private
// address (RFC 1918, ULA, CGNAT) — an intranet deployment, whose webhooks'
// icons on other private hosts the webapp's browser would load too. Asked
// only when an external icon is fetched (never at startup); a failed lookup
// is "no".
func (c *Cache) intranet(ctx context.Context, base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	if a, err := netip.ParseAddr(host); err == nil {
		return privateAddr(a)
	}
	now := c.o.Now()
	c.mu.Lock()
	e, ok := c.intranets[host]
	c.mu.Unlock()
	if ok && now.Before(e.until) {
		return e.private
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := c.lookup(ctx, host)
	private := err == nil && len(addrs) > 0
	for _, a := range addrs {
		private = private && privateAddr(a)
	}
	c.mu.Lock()
	if len(c.intranets) >= 64 {
		c.intranets = map[string]intranetEntry{}
	}
	c.intranets[host] = intranetEntry{private: private, until: now.Add(intranetTTL)}
	c.mu.Unlock()
	return private
}

// Limits of external icon fetches.
const (
	extFetches = 2                // at once, apart from the slots other pictures use
	extTimeout = 10 * time.Second // a whole external fetch, redirects included
)

// newExternalClient fetches pictures from outside any Mattermost server:
// no cookie jar, no environment proxy (the address check must see the real
// target), every dial checked by allow (the address a name resolved to —
// so a name re-pointed at the LAN is caught too; a zone is dropped first),
// at most maxRedirects http(s) redirects, strict timeouts. Callers set no
// credentials.
func newExternalClient(allow func(netip.AddrPort) bool) *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !allow(netip.AddrPortFrom(ap.Addr().WithZone("").Unmap(), ap.Port())) {
			return errBlocked
		}
		return nil
	}}
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            d.DialContext,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  8 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		MaxIdleConns:           4,
		IdleConnTimeout:        30 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: extTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.User != nil {
				return errBlocked
			}
			return nil
		}}
}

// strictDial / intranetDial are the address checks of external icons: of
// a server on the internet, and of one on a private address.
func strictDial(ap netip.AddrPort) bool   { return allowedAddr(ap.Addr(), false) }
func intranetDial(ap netip.AddrPort) bool { return allowedAddr(ap.Addr(), true) }

// special are ranges no icon is fetched from, whoever runs the server:
// "this network", IETF protocol assignments, documentation, benchmarking,
// reserved; IPv4-compatible and IPv4-translated IPv6, the discard prefix,
// local-use NAT64 (RFC 8215), site-local, 6to4, Teredo, benchmarking,
// ORCHID and documentation IPv6. (Well-known NAT64 is judged by the IPv4
// address it carries; an operator-chosen NAT64 prefix cannot be told
// apart from a public address.)
var special = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"), netip.MustParsePrefix("::ffff:0:0:0/96"), netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("fec0::/10"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2001:2::/48"), netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"), netip.MustParsePrefix("2001:db8::/32"),
	// Cloud metadata inside ranges an intranet server may use: AWS IMDS
	// over IPv6 (ULA), Alibaba Cloud (CGNAT). 169.254.169.254 and the rest
	// are link-local — refused anyway.
	netip.MustParsePrefix("fd00:ec2::254/128"), netip.MustParsePrefix("100.100.100.200/32"),
}

var (
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
	cgnat = netip.MustParsePrefix("100.64.0.0/10")
)

// allowedAddr: may an external icon be fetched from a? Never from
// loopback, link-local (the cloud metadata address too), unspecified,
// multicast or a special range; from a private one (RFC 1918, ULA, CGNAT)
// only for a server that is itself on one (intranet); otherwise a global
// unicast address. A zone is dropped first (a zoned address is contained
// in no prefix).
func allowedAddr(a netip.Addr, intranet bool) bool {
	a = a.WithZone("").Unmap()
	if nat64.Contains(a) {
		b := a.As16()
		return allowedAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), intranet)
	}
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
		return false
	}
	for _, p := range special {
		if p.Contains(a) {
			return false
		}
	}
	if privateAddr(a) {
		return intranet
	}
	return true
}

// privateAddr: RFC 1918, ULA (fc00::/7) or CGNAT.
func privateAddr(a netip.Addr) bool {
	a = a.WithZone("").Unmap()
	return a.IsPrivate() || cgnat.Contains(a)
}
