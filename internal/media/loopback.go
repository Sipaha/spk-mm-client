package media

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errLoopbackClosed = errors.New("media: stream server closed")

// Loopback serves audio and video to the desktop webview over plain HTTP on
// 127.0.0.1: WebKitGTK's media player (GStreamer) cannot read the app's
// wails:// scheme at all (spike S6), so <video>/<audio> get
// http://127.0.0.1:<port>/<token>/<server id>/stream/<file id> instead.
//
// It listens only after the UI first asks for its address (Base), on a
// random port, until Close. Every request must carry the run's token (32
// random bytes, compared in constant time) and Host 127.0.0.1:<port> (no DNS
// rebinding); only GET/HEAD, no CORS, and only what the Streamer serves:
// audio/video files of servers whose worker is live. The token is never
// logged.
type Loopback struct {
	stream *Streamer

	mu     sync.Mutex
	srv    *http.Server // nil until Base
	base   string
	host   string // 127.0.0.1:<port>, set before the server starts
	token  []byte
	closed bool
}

func NewLoopback(s *Streamer) *Loopback { return &Loopback{stream: s} }

// Base is http://127.0.0.1:<port>/<token>; the first call starts the server.
func (l *Loopback) Base() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return "", errLoopbackClosed
	}
	if l.srv != nil {
		return l.base, nil
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	l.token = []byte(token)
	l.host = ln.Addr().String()
	l.base = "http://" + l.host + "/" + token
	l.srv = &http.Server{
		Handler:           http.HandlerFunc(l.serve),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Its messages carry no request paths; at debug level like ours.
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelDebug),
	}
	srv := l.srv
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("media stream server stopped", "err", err)
		}
	}()
	slog.Debug("media stream server started", "addr", l.host)
	return l.base, nil
}

// Close stops the server and drops open streams (their upstream requests
// end with them). Base fails afterwards.
func (l *Loopback) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.srv == nil {
		return nil
	}
	err := l.srv.Close()
	l.srv = nil
	return err
}

func (l *Loopback) serve(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	// no-store: neither the tokenized URL nor the bytes go to WebKit's
	// disk cache.
	h.Set("Cache-Control", "no-store")
	if r.Host != l.host {
		l.reject(w, http.StatusForbidden, "host")
		return
	}
	server, fileID, ok := l.route(r.URL.Path)
	if !ok {
		l.reject(w, http.StatusNotFound, "path")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		l.reject(w, http.StatusMethodNotAllowed, "method")
		return
	}
	l.stream.serve(w, r, server, fileID, "no-store")
}

// route checks /<token>/<server id>/stream/<file id> — the base is
// /<token> where browser mode has /media (same <base>/<srv>/<kind>/<key>).
func (l *Loopback) route(path string) (int64, string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) != 4 || subtle.ConstantTimeCompare([]byte(parts[0]), l.token) != 1 || parts[2] != "stream" {
		return 0, "", false
	}
	server, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || server <= 0 || !idRe.MatchString(parts[3]) {
		return 0, "", false
	}
	return server, parts[3], true
}

// reject answers a request that never reaches the Streamer; the log line
// names only the reason (the path would carry the token).
func (l *Loopback) reject(w http.ResponseWriter, status int, reason string) {
	slog.Debug("media stream request rejected", "status", status, "reason", reason)
	http.Error(w, http.StatusText(status), status)
}
