package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
)

// Media types a stream may carry: by file extension first, then by the
// file's MIME type; everything else is refused (415). The UI plays these
// in <video>/<audio>; what the webview cannot decode fails there.
var (
	streamTypeByExt = map[string]string{
		"mp4": "video/mp4", "m4v": "video/mp4", "webm": "video/webm", "mov": "video/quicktime",
		"ogv": "video/ogg", "mkv": "video/x-matroska",
		"mp3": "audio/mpeg", "ogg": "audio/ogg", "oga": "audio/ogg", "opus": "audio/ogg",
		"wav": "audio/wav", "flac": "audio/flac", "m4a": "audio/mp4", "aac": "audio/aac",
	}
	streamTypeByMime = map[string]string{
		"video/mp4": "video/mp4", "video/x-m4v": "video/mp4", "video/webm": "video/webm",
		"video/quicktime": "video/quicktime", "video/ogg": "video/ogg", "video/x-matroska": "video/x-matroska",
		"audio/mpeg": "audio/mpeg", "audio/mp3": "audio/mpeg", "audio/ogg": "audio/ogg", "audio/opus": "audio/ogg",
		"audio/wav": "audio/wav", "audio/x-wav": "audio/wav", "audio/wave": "audio/wav",
		"audio/flac": "audio/flac", "audio/x-flac": "audio/flac", "audio/mp4": "audio/mp4",
		"audio/x-m4a": "audio/mp4", "audio/aac": "audio/aac", "audio/webm": "audio/webm",
	}
	// rangeRe is one byte range: "bytes=a-b", "bytes=a-" or "bytes=-n".
	// Multipart ranges are not passed on (players never ask for them).
	rangeRe = regexp.MustCompile(`^bytes=(?:(\d{1,19})-(\d{0,19})|-(\d{1,19}))$`)
)

const (
	streamCacheControl = "private, max-age=300"
	maxStreamTypes     = 1024 // remembered file types; the map is reset when full
	infoLimit          = 64 << 10
)

var errNotMedia = errors.New("media: not an audio or video file")

// streamType picks the Content-Type a stream is served with, "" = refused.
func streamType(info model.FileInfo) string {
	if t := streamTypeByExt[strings.ToLower(info.Extension)]; t != "" {
		return t
	}
	mt, _, err := mime.ParseMediaType(info.MimeType)
	if err != nil {
		return ""
	}
	return streamTypeByMime[mt]
}

func validRange(v string) bool {
	m := rangeRe.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	if m[1] != "" && m[2] != "" {
		a, err1 := strconv.ParseUint(m[1], 10, 63)
		b, err2 := strconv.ParseUint(m[2], 10, 63)
		return err1 == nil && err2 == nil && a <= b
	}
	return true
}

// Streamer passes audio and video files of signed-in servers through to
// the webview: Range goes upstream as is, the body is copied as it comes
// (nothing on disk, nothing buffered beyond the copy buffer), and the
// upstream request follows the client's — a player that seeks or goes
// away closes it. It fetches through Origin like the cache: only while the
// server's worker is live (ErrNoServer → 404), and a 401 there is the
// session dying (the worker asks for a new sign-in).
type Streamer struct {
	origin  Origin
	timeout time.Duration // file info lookup

	mu    sync.Mutex
	types map[string]string // "server/file id" → Content-Type
}

func NewStreamer(o Origin) *Streamer {
	return &Streamer{origin: o, timeout: 30 * time.Second, types: map[string]string{}}
}

// Serve answers GET/HEAD for one file (the caller checked method and ids).
// HEAD costs as much as GET upstream: a full GET (the Origin has no HEAD)
// and a rate-limiter token; only the body is not copied.
func (s *Streamer) Serve(w http.ResponseWriter, r *http.Request, server int64, fileID string) {
	s.serve(w, r, server, fileID, streamCacheControl)
}

// serve is Serve with the Cache-Control of a successful answer.
func (s *Streamer) serve(w http.ResponseWriter, r *http.Request, server int64, fileID, cacheControl string) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	rng := r.Header.Get("Range")
	if rng != "" && !validRange(rng) {
		s.fail(w, server, fileID, http.StatusBadRequest, nil)
		return
	}
	ctype, err := s.contentType(r.Context(), server, fileID)
	if err != nil {
		s.fail(w, server, fileID, streamStatus(err), err)
		return
	}
	// identity: Go's transport would otherwise ask for gzip and inflate it,
	// and a range of the compressed body is not a range of the file.
	hdr := http.Header{"Accept-Encoding": {"identity"}}
	if rng != "" {
		hdr.Set("Range", rng)
	}
	resp, err := s.origin.Get(r.Context(), server, "/api/v4/files/"+url.PathEscape(fileID), hdr)
	if err != nil {
		s.fail(w, server, fileID, streamStatus(err), err)
		return
	}
	defer resp.Body.Close()
	cr := resp.Header.Get("Content-Range")
	if resp.StatusCode != http.StatusOK && (resp.StatusCode != http.StatusPartialContent || cr == "") {
		s.fail(w, server, fileID, http.StatusBadGateway, errUpstream)
		return
	}
	if cr != "" && resp.StatusCode == http.StatusPartialContent {
		h.Set("Content-Range", cr)
	}
	if resp.ContentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if resp.StatusCode == http.StatusPartialContent || resp.Header.Get("Accept-Ranges") == "bytes" {
		h.Set("Accept-Ranges", "bytes")
	}
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", cacheControl)
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	// An error here is the player going away (a seek, a closed viewer) or
	// the server breaking off: either way the response is already started.
	_, _ = io.Copy(w, resp.Body)
}

func (s *Streamer) fail(w http.ResponseWriter, server int64, fileID string, status int, err error) {
	slog.Debug("media stream refused", "server", server, "file", fileID, "status", status, "err", err)
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, http.StatusText(status), status)
}

// contentType looks the file up once (its info) and remembers the verdict;
// failures to look it up are not remembered.
func (s *Streamer) contentType(ctx context.Context, server int64, fileID string) (string, error) {
	key := strconv.FormatInt(server, 10) + "/" + fileID
	s.mu.Lock()
	t, ok := s.types[key]
	s.mu.Unlock()
	if !ok {
		info, err := s.fileInfo(ctx, server, fileID)
		if err != nil {
			return "", err
		}
		t = streamType(info)
		s.mu.Lock()
		if len(s.types) >= maxStreamTypes {
			s.types = map[string]string{}
		}
		s.types[key] = t
		s.mu.Unlock()
	}
	if t == "" {
		return "", errNotMedia
	}
	return t, nil
}

func (s *Streamer) fileInfo(ctx context.Context, server int64, fileID string) (model.FileInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var info model.FileInfo
	resp, err := s.origin.Get(ctx, server, "/api/v4/files/"+url.PathEscape(fileID)+"/info", nil)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, errUpstream
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, infoLimit)).Decode(&info); err != nil {
		return info, errUpstream
	}
	return info, nil
}

func streamStatus(err error) int {
	var re *rest.Error
	switch {
	case errors.Is(err, errNotMedia):
		return http.StatusUnsupportedMediaType
	case errors.As(err, &re) && re.Status == http.StatusRequestedRangeNotSatisfiable:
		return http.StatusRequestedRangeNotSatisfiable
	}
	return statusFor(err)
}
