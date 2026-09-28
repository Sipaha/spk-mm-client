// Package media serves pictures and file snippets of Mattermost servers to
// the UI from a bounded on-disk cache. The UI never talks to a Mattermost
// server itself: it requests /media/<server id>/<kind>/<key> on its own
// origin, and the cache fetches the object through the server's REST client
// (Origin — the token and the per-server rate limiter stay in Go), checks
// what came back, keeps it on disk and serves it with long-lived headers.
//
// UI contract for images in the feed: feed/{fileId} uses src=preview
// whenever the file has a preview (the server's is ≤1920 px, cheap to
// scale); src=file is only for originals without one, and those above
// scaleMaxPixels are refused (413) rather than decoded or passed through.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/rest"
)

// Kind is the second segment of a /media/ URL: what is served.
type Kind string

// Kinds of media objects.
const (
	KindAvatar Kind = "avatar" // user picture; key user id, ?v=last_picture_update
	KindThumb  Kind = "thumb"  // file thumbnail (server: JPEG ≤120×100)
	KindFeed   Kind = "feed"   // image in the feed: preview or original, scaled to ≤ FeedMax
	KindFull   Kind = "full"   // image in the viewer: preview or original as is
	KindText   Kind = "text"   // first TextLimit (or TextFullLimit with ?full=1) bytes of a text file
	KindEmoji  Kind = "emoji"  // custom emoji picture; key emoji name
	KindStream Kind = "stream" // audio/video passed through with Range, never cached (Streamer)
	// KindStaged is a picture attached to a message not sent yet; key the
	// attachment id. Read from the local file (Staged), scaled like feed.
	KindStaged Kind = "staged"
	// KindPostIcon is a webhook post's own author picture
	// (override_icon_url); key the POST id, never a URL — posticon.go.
	KindPostIcon Kind = "posticon"
)

// Limits of the cache and of what it accepts.
const (
	DefaultMaxBytes = 256 << 20
	FeedMax         = 960              // px: feed boxes are ≤480×360 CSS px, ×2 for HiDPI
	TextLimit       = 64 << 10         // bytes of a text file shown in the feed snippet
	TextFullLimit   = 1 << 20          // bytes of a text file shown in the viewer (?full=1)
	maxPixels       = 50_000_000       // decompression-bomb guard for every image
	scaleMaxPixels  = 24_000_000       // largest feed image decoded to scale (~96 MB bitmap)
	negTTL          = 5 * time.Minute  // 403/404/413/415: will not change soon
	negTTLTransient = 30 * time.Second // network trouble
	maxNeg          = 1000
)

var (
	// ErrNoServer means the server id is unknown, not signed in or not live
	// (404, not remembered).
	ErrNoServer = errors.New("media: server not signed in or not live")
	errTooLarge = errors.New("media: object too large")
	errType     = errors.New("media: content type not allowed")
	errUpstream = errors.New("media: unexpected upstream status")
	errStore    = errors.New("media: cache write failed")
	errNoObject = errors.New("media: no such object")
)

// Origin fetches objects from signed-in servers (implemented by api.Service).
type Origin interface {
	// Get GETs an API path through the server's REST client; the caller
	// closes the body.
	Get(ctx context.Context, serverID int64, path string, hdr http.Header) (*http.Response, error)
	// EmojiID resolves a custom emoji name; "" and no error: no such emoji.
	EmojiID(ctx context.Context, serverID int64, name string) (string, error)
}

// Staged gives the files of attachments not sent yet (implemented by
// api.Service over attach.Store).
type Staged interface {
	// StagedType is the media type of a server's attachment; false when
	// that server has no such attachment.
	StagedType(serverID int64, id string) (string, bool)
	// OpenStaged opens the file of a server's attachment.
	OpenStaged(serverID int64, id string) (io.ReadCloser, error)
}

type Options struct {
	Dir      string
	MaxBytes int64 // total size cap; 0 → DefaultMaxBytes
	Origin   Origin
	Staged   Staged // nil: staged pictures are not found
	// PostIcons: nil — posticon pictures are not found.
	PostIcons PostIcons
	Fetches   int           // concurrent upstream fetches; 0 → 6
	Timeout   time.Duration // per upstream fetch; 0 → 60 s
	Now       func() time.Time
}

type entry struct {
	size int64
	used time.Time
}

type negEntry struct {
	status int
	until  time.Time
}

type call struct {
	done    chan struct{}
	waiters int
	status  int
}

type Cache struct {
	o        Options
	sem      chan struct{}
	mu       sync.Mutex
	index    map[string]*entry // file name → entry
	total    int64
	neg      map[string]negEntry // cache key → recent failure
	inflight map[string]*call    // cache key → fetch in progress
	stream   *Streamer
	// External post icons: no credentials, public addresses only (ext) or
	// private ones too for a server on a private address (extIntranet);
	// extSem: their own fetch slots; intranets: servers' answers; lookup
	// resolves a server's host (tests stub it).
	ext, extIntranet *http.Client
	extSem           chan struct{}
	intranets        map[string]intranetEntry
	lookup           func(ctx context.Context, host string) ([]netip.Addr, error)
}

// New opens (creating) the cache directory: leftovers of interrupted writes
// are removed, existing objects are indexed (last use = mtime) and trimmed
// to the cap.
func New(o Options) (*Cache, error) {
	if o.Dir == "" || o.Origin == nil {
		return nil, errors.New("media: Dir and Origin are required")
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.Fetches <= 0 {
		o.Fetches = 6
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return nil, err
	}
	des, err := os.ReadDir(o.Dir)
	if err != nil {
		return nil, err
	}
	c := &Cache{o: o, sem: make(chan struct{}, o.Fetches), index: map[string]*entry{},
		neg: map[string]negEntry{}, inflight: map[string]*call{}, stream: NewStreamer(o.Origin),
		ext: newExternalClient(strictDial), extIntranet: newExternalClient(intranetDial),
		extSem: make(chan struct{}, extFetches), intranets: map[string]intranetEntry{},
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}}
	for _, de := range des {
		name := de.Name()
		switch {
		case strings.HasSuffix(name, ".tmp"):
			_ = os.Remove(filepath.Join(o.Dir, name))
		case strings.HasSuffix(name, ".bin"):
			if fi, err := de.Info(); err == nil {
				c.index[name] = &entry{size: fi.Size(), used: fi.ModTime()}
				c.total += fi.Size()
			}
		}
	}
	c.mu.Lock()
	c.evictLocked("")
	c.mu.Unlock()
	return c, nil
}

// Size is the total size of cached objects.
func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

type request struct {
	server  int64
	kind    Kind
	key     string    // user id, file id or emoji name
	variant string    // avatar: picture version; feed/full: "preview" | "file"
	icon    iconRoute // posticon: where the picture comes from (resolved in get)
}

var (
	idRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	emojiRe = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,64}$`)
	// iconVersionRe: a posticon's ?v= (state.PostView.IconVersion).
	iconVersionRe = regexp.MustCompile(`^[0-9a-f]{1,16}$`)
)

func parse(u *url.URL) (request, bool) {
	tail, ok := strings.CutPrefix(u.Path, "/media/")
	parts := strings.Split(tail, "/")
	if !ok || len(parts) != 3 {
		return request{}, false
	}
	srv, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || srv <= 0 {
		return request{}, false
	}
	q := request{server: srv, kind: Kind(parts[1]), key: parts[2]}
	switch q.kind {
	case KindAvatar:
		q.variant = "0"
		if v := u.Query().Get("v"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return request{}, false
			}
			q.variant = strconv.FormatInt(n, 10) // "+5", "05" and "5" are one object
		}
	case KindFeed, KindFull:
		q.variant = u.Query().Get("src")
		if q.variant == "" {
			q.variant = "preview"
		}
		if q.variant != "preview" && q.variant != "file" {
			return request{}, false
		}
	case KindPostIcon:
		// v: the version of the post's icon (the UI's cache buster); the
		// picture itself is keyed by where it comes from.
		if v := u.Query().Get("v"); v != "" && !iconVersionRe.MatchString(v) {
			return request{}, false
		}
	case KindThumb, KindStream, KindStaged:
	case KindText:
		switch u.Query().Get("full") {
		case "":
		case "1":
			q.variant = "full"
		default:
			return request{}, false
		}
	case KindEmoji:
		return q, emojiRe.MatchString(q.key)
	default:
		return request{}, false
	}
	return q, idRe.MatchString(q.key)
}

// spec is what to fetch for a request and how much of it to accept.
type spec struct {
	path  string
	hdr   http.Header
	max   int64
	text  bool
	scale bool
}

func (q request) spec(id string) spec {
	esc := url.PathEscape(id)
	switch q.kind {
	case KindAvatar:
		p := "/api/v4/users/" + esc + "/image"
		if q.variant != "0" {
			p += "?_=" + url.QueryEscape(q.variant)
		}
		return spec{path: p, max: 2 << 20}
	case KindThumb:
		return spec{path: "/api/v4/files/" + esc + "/thumbnail", max: 2 << 20}
	case KindFeed, KindFull:
		p := "/api/v4/files/" + esc
		if q.variant == "preview" {
			p += "/preview"
		}
		return spec{path: p, max: 25 << 20, scale: q.kind == KindFeed}
	case KindStaged: // a local file: no path
		return spec{max: 25 << 20, scale: true}
	case KindPostIcon: // shown as a 36 px avatar: large ones are scaled down
		return spec{path: q.icon.path, max: 2 << 20, scale: true}
	case KindText:
		limit := int64(TextLimit)
		if q.variant == "full" {
			limit = TextFullLimit
		}
		return spec{path: "/api/v4/files/" + esc, max: limit, text: true,
			hdr: http.Header{"Range": {fmt.Sprintf("bytes=0-%d", limit-1)}}}
	default: // KindEmoji: id is the resolved emoji id
		return spec{path: "/api/v4/emoji/" + esc + "/image", max: 1 << 20}
	}
}

func fileName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + ".bin"
}

func (c *Cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q, ok := parse(r.URL)
	if !ok {
		http.Error(w, "bad media path", http.StatusBadRequest)
		return
	}
	if q.kind == KindStream {
		c.stream.Serve(w, r, q.server, q.key)
		return
	}
	// Staged: only a raster picture of an attachment that exists now — a
	// copy cached earlier is not served once the attachment is gone.
	if q.kind == KindStaged && !c.stagedPicture(q) {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		name, status := c.get(r.Context(), q)
		if status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
		f, err := os.Open(filepath.Join(c.o.Dir, name))
		if errors.Is(err, fs.ErrNotExist) { // evicted between get and open
			c.forget(name)
			continue
		}
		if err != nil {
			http.Error(w, "cache read failed", http.StatusInternalServerError)
			return
		}
		c.serve(w, r, q, name, f)
		_ = f.Close()
		return
	}
	http.Error(w, "cache busy", http.StatusServiceUnavailable)
}

func (c *Cache) stagedPicture(q request) bool {
	if c.o.Staged == nil {
		return false
	}
	mt, ok := c.o.Staged.StagedType(q.server, q.key)
	return ok && rasterTypes[mt]
}

// serve answers with a cached object. A corrupt one (a text entry without
// its flag, an image entry that does not sniff as raster) is dropped and
// the request fails; the next one fetches it anew. Cache headers are set
// only on success.
func (c *Cache) serve(w http.ResponseWriter, r *http.Request, q request, name string, f *os.File) {
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "cache read failed", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	var body io.ReadSeeker = f
	if q.kind == KindText {
		var flag [1]byte
		if _, err := io.ReadFull(f, flag[:]); err != nil || (flag[0] != 'T' && flag[0] != 'F') {
			c.drop(name)
			http.Error(w, "cache read failed", http.StatusInternalServerError)
			return
		}
		if flag[0] == 'T' {
			h.Set("X-Truncated", "1")
		}
		h.Set("Content-Type", "text/plain; charset=utf-8")
		body = io.NewSectionReader(f, 1, fi.Size()-1)
	} else {
		head := make([]byte, sniffLen)
		n, _ := io.ReadFull(f, head)
		ctype := http.DetectContentType(head[:n])
		if !rasterTypes[ctype] {
			c.drop(name)
			http.Error(w, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
			return
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "cache read failed", http.StatusInternalServerError)
			return
		}
		h.Set("Content-Type", ctype)
	}
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if q.kind == KindEmoji || q.kind == KindPostIcon {
		h.Set("Cache-Control", "private, max-age=3600") // by name / by post: may change
	} else {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, "", time.Time{}, body)
}

// get makes sure the object is on disk and returns its file name, or an
// HTTP status for why it is not.
func (c *Cache) get(ctx context.Context, q request) (string, int) {
	id := q.key
	if q.kind == KindEmoji {
		var status int
		if id, status = c.emojiID(ctx, q); status != 0 {
			return "", status
		}
	}
	if q.kind == KindPostIcon {
		var status int
		if id, q.icon, status = c.postIcon(q); status != 0 {
			return "", status
		}
	}
	key := fmt.Sprintf("%d/%s/%s/%s", q.server, q.kind, id, q.variant)
	name := fileName(key)
	now := c.o.Now()
	c.mu.Lock()
	if e := c.index[name]; e != nil {
		e.used = now
		c.mu.Unlock()
		return name, 0
	}
	if n, ok := c.neg[key]; ok && now.Before(n.until) {
		c.mu.Unlock()
		return "", n.status
	}
	cl := c.inflight[key]
	if cl == nil {
		cl = &call{done: make(chan struct{})}
		c.inflight[key] = cl
		go c.fill(key, name, q, id, cl)
	}
	cl.waiters++
	c.mu.Unlock()
	select {
	case <-cl.done:
		if cl.status != 0 {
			return "", cl.status
		}
		return name, 0
	case <-ctx.Done():
		c.mu.Lock()
		cl.waiters--
		c.mu.Unlock()
		return "", http.StatusServiceUnavailable
	}
}

func (c *Cache) emojiID(ctx context.Context, q request) (string, int) {
	ctx, cancel := context.WithTimeout(ctx, c.o.Timeout)
	defer cancel()
	id, err := c.o.Origin.EmojiID(ctx, q.server, q.key)
	switch {
	case err != nil:
		return "", statusFor(err)
	case id == "" || !idRe.MatchString(id):
		return "", http.StatusNotFound
	}
	return id, 0
}

// fill runs one upstream fetch for everyone waiting on cl. It does not use
// a requester's context: the first viewer scrolling away must not waste a
// fetch the next one needs. But a fetch nobody waits for any more when its
// turn comes is skipped (not remembered as a failure): the server's request
// budget is shared with synchronisation.
func (c *Cache) fill(key, name string, q request, id string, cl *call) {
	// An external icon waits for its own slots: a slow host out there must
	// not hold the ones avatars, previews and emoji use.
	external := q.kind == KindPostIcon && q.icon.ext != ""
	sem := c.sem
	if external {
		sem = c.extSem
	}
	sem <- struct{}{}
	c.mu.Lock()
	if cl.waiters == 0 {
		delete(c.inflight, key)
		cl.status = http.StatusServiceUnavailable
		c.mu.Unlock()
		<-sem
		close(cl.done)
		return
	}
	c.mu.Unlock()
	sl := &slot{c: c, sem: sem}
	err := c.fetch(q, id, name, sl)
	<-sl.sem
	external = sl.external() // a camo handover moved it out
	status := 0
	if err != nil {
		status = statusFor(err)
	}
	c.mu.Lock()
	delete(c.inflight, key)
	// A server that is not signed in (or not live) yet is not remembered:
	// its pictures must appear as soon as it is (asking again costs no
	// network). Nor is a 401: the session died, the worker asks for a new
	// sign-in, and the picture must load once it is signed in again.
	// A post icon's 401 is its path's answer, not a dead session (GetIcon
	// never asks for a sign-in): remembered like a 403.
	if status != 0 && !errors.Is(err, ErrNoServer) && (!unauthorized(err) || q.kind == KindPostIcon) {
		ttl := negTTLTransient
		switch status {
		case http.StatusForbidden, http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
			ttl = negTTL
		}
		if external { // a host out there that failed is not asked again soon
			ttl = negTTL
		}
		if len(c.neg) >= maxNeg {
			c.neg = map[string]negEntry{}
		}
		c.neg[key] = negEntry{status: status, until: c.o.Now().Add(ttl)}
	}
	cl.status = status
	c.mu.Unlock()
	close(cl.done)
}

// slot is the fetch slot a fill holds: one of the shared ones, or an
// external icon's own.
type slot struct {
	c   *Cache
	sem chan struct{}
}

func (s *slot) external() bool { return s.sem == s.c.extSem }

// toExternal trades a shared slot for an external one (not holding the
// shared one while it waits): a fetch that turned out to go to a host out
// there (a camo handover) must not hold what other pictures need.
func (s *slot) toExternal() {
	if s.external() {
		return
	}
	<-s.sem
	s.c.extSem <- struct{}{}
	s.sem = s.c.extSem
}

func (c *Cache) fetch(q request, id, name string, sl *slot) error {
	sp := q.spec(id)
	var body io.Reader
	var contentRange string
	if q.kind == KindStaged {
		f, err := c.o.Staged.OpenStaged(q.server, id)
		if err != nil {
			return errNoObject
		}
		defer f.Close()
		body = f
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), c.o.Timeout)
		defer cancel()
		var resp *http.Response
		var err error
		if q.kind == KindPostIcon {
			resp, err = c.openIcon(ctx, q, sl)
		} else {
			resp, err = c.o.Origin.Get(ctx, q.server, sp.path, sp.hdr)
		}
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			return errUpstream
		}
		body, contentRange = resp.Body, resp.Header.Get("Content-Range")
	}
	tmp, err := os.CreateTemp(c.o.Dir, "*.tmp")
	if err != nil {
		slog.Warn("media cache write failed", "err", err)
		return errStore
	}
	var size int64
	if sp.text {
		size, err = writeText(tmp, body, contentRange, sp.max)
	} else {
		size, err = c.writeImageFrom(tmp, body, sp)
	}
	if q.kind == KindStaged && errors.Is(err, errType) {
		err = errNoObject // not a picture after all: the UI shows a type icon
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(c.o.Dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	c.mu.Lock()
	if old := c.index[name]; old != nil {
		c.total -= old.size
	}
	c.index[name] = &entry{size: size, used: c.o.Now()}
	c.total += size
	c.evictLocked(name)
	c.mu.Unlock()
	return nil
}

// writeImageFrom writes a picture from body: a file as it is, a download
// spooled to a temp file of the cache first. The download is read outside
// decodeSem (only decodes are serialised) and never whole into memory.
func (c *Cache) writeImageFrom(dst io.Writer, body io.Reader, sp spec) (int64, error) {
	if rs, ok := body.(io.ReadSeeker); ok {
		return writeImage(dst, rs, sp)
	}
	spool, err := os.CreateTemp(c.o.Dir, "*.tmp")
	if err != nil {
		slog.Warn("media cache write failed", "err", err)
		return 0, errStore
	}
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spool.Name())
	}()
	n, err := io.Copy(spool, io.LimitReader(body, sp.max+1))
	switch {
	case err != nil:
		return 0, err
	case n > sp.max:
		return 0, errTooLarge
	}
	return writeImage(dst, spool, sp)
}

// evictLocked drops least recently used objects until the cache fits its
// cap; keep (the object just written) is never dropped.
func (c *Cache) evictLocked(keep string) {
	for c.total > c.o.MaxBytes {
		victim := ""
		var oldest time.Time
		for name, e := range c.index {
			if name != keep && (victim == "" || e.used.Before(oldest)) {
				victim, oldest = name, e.used
			}
		}
		if victim == "" {
			return
		}
		if err := os.Remove(filepath.Join(c.o.Dir, victim)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("media eviction failed", "err", err) // e.g. open on Windows: retried on the next write
			return
		}
		c.total -= c.index[victim].size
		delete(c.index, victim)
	}
}

// drop removes a corrupt object: its file and its index entry.
func (c *Cache) drop(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.Remove(filepath.Join(c.o.Dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Debug("media drop failed", "err", err)
		return
	}
	if e := c.index[name]; e != nil {
		c.total -= e.size
		delete(c.index, name)
	}
}

// forget drops the index entry of an object whose file is gone (deleted
// behind our back). The file is checked again under the lock: the object may
// have been fetched anew since the failed open, and dropping that entry would
// leave its file uncounted.
func (c *Cache) forget(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := os.Stat(filepath.Join(c.o.Dir, name)); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	if e := c.index[name]; e != nil {
		c.total -= e.size
		delete(c.index, name)
	}
}

func unauthorized(err error) bool {
	var re *rest.Error
	return errors.As(err, &re) && re.Status == http.StatusUnauthorized
}

func statusFor(err error) int {
	var re *rest.Error
	switch {
	case errors.Is(err, ErrNoServer), errors.Is(err, errNoObject):
		return http.StatusNotFound
	case errors.Is(err, errTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, errType):
		return http.StatusUnsupportedMediaType
	case errors.Is(err, errBlocked):
		return http.StatusForbidden
	case errors.Is(err, errStore):
		return http.StatusInternalServerError
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.As(err, &re) && (re.Status == http.StatusUnauthorized || re.Status == http.StatusForbidden):
		return http.StatusForbidden
	case errors.As(err, &re) && (re.Status == http.StatusBadRequest || re.Status == http.StatusNotFound || re.Status == http.StatusNotImplemented):
		return http.StatusNotFound // no thumbnail/preview, deleted file, custom emoji off
	}
	return http.StatusBadGateway
}
