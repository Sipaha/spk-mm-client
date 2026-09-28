package mmfake

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // image.Decode of uploaded GIFs
	"image/jpeg"
	"image/png"
	"io"
	stdmime "mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// ffile is an uploaded file with the images the real server derives from it.
type ffile struct {
	info      model.FileInfo
	channelID string
	data      []byte // in memory; nil when stored on disk
	path      string // on disk (Options.FilesDir); "" when in memory
	// previewPath: the preview on disk next to path (it can be megabytes);
	// preview is nil then.
	previewPath string
	thumb       []byte // JPEG fitted into 120×100; nil for non-images
	preview     []byte // JPEG ≤1920 wide; nil unless HasPreviewImage
}

type femoji struct {
	e   model.Emoji
	png []byte
}

type picture struct {
	at  int64 // last_picture_update (negative: generated default)
	png []byte
}

var palette = []color.RGBA{
	{79, 140, 255, 255}, {46, 184, 134, 255}, {218, 160, 56, 255}, {163, 108, 230, 255}, {230, 90, 110, 255},
}

// patternPNG draws w×h diagonal stripes of c and a darker shade, so a picture
// in a screenshot reads as an image rather than a flat box.
func patternPNG(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	dark := color.RGBA{c.R / 2, c.G / 2, c.B / 2, 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px := c
			if ((x+y)/24)%2 == 1 {
				px = dark
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = px.R, px.G, px.B, px.A
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// scaledJPEG mirrors the server's thumbnail/preview: the image fitted into
// maxW×maxH (0 = unbounded), JPEG-encoded.
func scaledJPEG(src image.Image, maxW, maxH int) []byte {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := 1.0
	if maxW > 0 && w > maxW {
		scale = float64(maxW) / float64(w)
	}
	if maxH > 0 && float64(h)*scale > float64(maxH) {
		scale = float64(maxH) / float64(h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	var out bytes.Buffer
	_ = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 80})
	return out.Bytes()
}

// seedImages are generated once per process: stripes under -race are slow.
var seedImages = sync.OnceValue(func() map[string][]byte {
	return map[string][]byte{
		"build.png": patternPNG(960, 540, palette[0]),
		"flow.png":  patternPNG(600, 400, palette[1]),
		"arch.png":  patternPNG(400, 600, palette[2]),
		"avatar0":   patternPNG(128, 128, palette[0]),
		"avatar1":   patternPNG(128, 128, palette[1]),
		"avatar2":   patternPNG(128, 128, palette[2]),
		"avatar3":   patternPNG(128, 128, palette[3]),
		"avatar4":   patternPNG(128, 128, palette[4]),
		"emoji":     patternPNG(64, 64, palette[4]),
	}
})

// seedClips are short generated clips (gst-launch-1.0, spike S6): clip.webm
// 2 s VP9/Opus 320×180, clip.mp4 2 s H.264/AAC (faststart), tone.ogg 3 s Opus.
//
//go:embed seedmedia
var seedClips embed.FS

func seedMedia(name string) []byte {
	b, err := seedClips.ReadFile("seedmedia/" + name)
	if err != nil {
		panic("mmfake: seed clip " + name + ": " + err.Error())
	}
	return b
}

func logText(lines int) []byte {
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "2026-09-24 12:%02d:%02d INFO request #%d handled in %dms\n", i/60, i%60, i, 3+i%17)
	}
	return []byte(b.String())
}

// bigLogText is a larger seeded text file for the viewer's ?full=1 (over
// media.TextFullLimit, 1 MiB): a distinct message shape from logText's
// server.log so e2e text matches on one do not also hit the other.
func bigLogText(lines int) []byte {
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "2026-09-24 12:%02d:%02d DEBUG worker #%d tick %d queued jobs\n", i/60, i%60, i, 7+i%23)
	}
	return []byte(b.String())
}

// newFileLocked registers an uploaded file of channelID ("" id = generated);
// images get the derived images the server makes: a thumbnail always, a
// preview except for GIF (kept animated) and SVG.
func (s *Server) newFileLocked(id, channelID, name, mime string, data []byte) *ffile {
	f := newFFile(id, channelID, name, mime, int64(len(data)))
	f.data = data
	if isPicture(mime) {
		if d, ok := derive(data, mime); ok {
			f.setDerived(d)
		}
	}
	s.chat.files[f.info.ID] = f
	return f
}

func newFFile(id, channelID, name, mime string, size int64) *ffile {
	if id == "" {
		id = "f-" + newID()[:12]
	}
	ext := ""
	if i := strings.LastIndex(name, "."); i >= 0 {
		ext = strings.ToLower(name[i+1:])
	}
	return &ffile{channelID: channelID,
		info: model.FileInfo{ID: id, Name: name, Extension: ext, Size: size, MimeType: mime}}
}

func isPicture(mime string) bool { return strings.HasPrefix(mime, "image/") && mime != "image/svg+xml" }

func (f *ffile) setDerived(d derivedImage) {
	f.info.Width, f.info.Height, f.thumb, f.preview = d.w, d.h, d.thumb, d.preview
	f.info.HasPreviewImage = d.preview != nil
}

// open reads the file's content, from memory or from disk.
func (f *ffile) open() (io.ReadSeekCloser, error) {
	if f.path == "" {
		return nopCloser{bytes.NewReader(f.data)}, nil
	}
	return os.Open(f.path)
}

type nopCloser struct{ io.ReadSeeker }

func (nopCloser) Close() error { return nil }

type derivedImage struct {
	w, h           int
	thumb, preview []byte
}

// derived caches derivedImage by content: every fake Start seeds the same
// pictures, and decoding + re-encoding them under -race costs seconds.
var derived sync.Map // [32]byte → derivedImage

// derive makes what the server derives from an uploaded image: dimensions,
// a thumbnail and — except for GIF, kept animated — a preview.
func derive(data []byte, mime string) (derivedImage, bool) {
	key := sha256.Sum256(append([]byte(mime+"\x00"), data...))
	if d, ok := derived.Load(key); ok {
		return d.(derivedImage), true
	}
	d, ok := deriveFrom(bytes.NewReader(data), mime)
	if ok {
		derived.Store(key, d)
	}
	return d, ok
}

// deriveFrom reads an image from r (an upload on disk is not cached by
// content: it is not seeded again). A readable header is enough to be a
// picture; the thumbnail and preview need the whole image to decode.
func deriveFrom(r io.ReadSeeker, mime string) (derivedImage, bool) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return derivedImage{}, false
	}
	d := derivedImage{w: cfg.Width, h: cfg.Height}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return d, true
	}
	src, _, err := image.Decode(r)
	if err != nil {
		return d, true
	}
	d.thumb = scaledJPEG(src, 120, 100)
	if mime != "image/gif" {
		d.preview = scaledJPEG(src, 1920, 0)
	}
	return d, true
}

func (s *Server) fileInfosLocked(ids []string) []model.FileInfo {
	var out []model.FileInfo
	for _, id := range ids {
		if f := s.chat.files[id]; f != nil {
			out = append(out, f.info)
		}
	}
	return out
}

func (s *Server) allUserIDs() []string {
	out := make([]string, len(s.opts.Users))
	for i, u := range s.opts.Users {
		out[i] = u.ID
	}
	return out
}

func (s *Server) mediaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v4/users/{uid}/image", s.handleAuthed(s.userImage))
	mux.HandleFunc("POST /api/v4/users/status/ids", s.handleAuthed(s.statusesByIDs))
	mux.HandleFunc("POST /api/v4/files", s.handleAuthed(s.uploadFile))
	mux.HandleFunc("GET /api/v4/files/{fid}", s.handleAuthed(s.fileHandler("")))
	mux.HandleFunc("GET /api/v4/files/{fid}/thumbnail", s.handleAuthed(s.fileHandler("thumbnail")))
	mux.HandleFunc("GET /api/v4/files/{fid}/preview", s.handleAuthed(s.fileHandler("preview")))
	mux.HandleFunc("GET /api/v4/files/{fid}/info", s.handleAuthed(s.fileInfo))
	mux.HandleFunc("GET /api/v4/emoji", s.handleAuthed(s.emojiList))
	// One pattern for /emoji/name/{name} and /emoji/{id}/image: as two
	// patterns they overlap on /emoji/name/image and ServeMux panics.
	mux.HandleFunc("GET /api/v4/emoji/{a}/{b}", s.handleAuthed(s.emojiPath))
}

func (s *Server) userImage(w http.ResponseWriter, r *http.Request, _ User) {
	s.mu.Lock()
	pic := s.chat.pictures[r.PathValue("uid")]
	s.mu.Unlock()
	if pic == nil {
		appError(w, 404, "app.user.missing_account.const", "no such user")
		return
	}
	etag := strconv.FormatInt(pic.at, 10)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "max-age=86400, private")
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(pic.png)
}

// statusesByIDs mirrors getUserStatusesByIds: unknown users are "offline".
func (s *Server) statusesByIDs(w http.ResponseWriter, r *http.Request, _ User) {
	var ids []string
	if err := json.NewDecoder(r.Body).Decode(&ids); err != nil || len(ids) == 0 {
		appError(w, 400, "api.context.invalid_param.app_error", "user_ids")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Status, 0, len(ids))
	for _, id := range ids {
		st := s.chat.status[id]
		if st == "" {
			st = "offline"
		}
		out = append(out, model.Status{UserID: id, Status: st})
	}
	writeJSON(w, 200, out)
}

func (s *Server) fileFor(w http.ResponseWriter, r *http.Request, u User) *ffile {
	s.mu.Lock()
	f := s.chat.files[r.PathValue("fid")]
	allowed := f != nil && s.isMemberLocked(f.channelID, u.ID)
	s.mu.Unlock()
	switch {
	case f == nil:
		appError(w, 404, "app.file_info.get.app_error", "not found")
		return nil
	case !allowed:
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return nil
	}
	return f
}

// fileHandler serves the file ("") or a derived image; Range works through
// http.ServeContent like on the real server.
func (s *Server) fileHandler(what string) func(http.ResponseWriter, *http.Request, User) {
	return func(w http.ResponseWriter, r *http.Request, u User) {
		f := s.fileFor(w, r, u)
		if f == nil {
			return
		}
		var content io.ReadSeekCloser
		ctype := f.info.MimeType
		switch what {
		case "thumbnail":
			content, ctype = derivedContent(f.thumb), "image/jpeg"
		case "preview":
			content, ctype = derivedContent(f.preview), "image/jpeg"
			if f.previewPath != "" {
				var err error
				if content, err = os.Open(f.previewPath); err != nil {
					appError(w, 500, "api.file.get_file.app_error", "could not read the file")
					return
				}
			}
		default:
			var err error
			if content, err = f.open(); err != nil {
				appError(w, 500, "api.file.get_file.app_error", "could not read the file")
				return
			}
		}
		if content == nil {
			appError(w, 400, "api.file.get_file_"+what+".no_"+what+".app_error", "no "+what)
			return
		}
		defer content.Close()
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		s.mu.Lock()
		bps := s.fileThrottle
		s.mu.Unlock()
		if what == "" && bps > 0 {
			streamThrottled(w, r, content, f.info.Size, bps)
			return
		}
		http.ServeContent(w, r, f.info.Name, time.Time{}, content)
	}
}

// derivedContent is a derived image to serve; nil when there is none.
func derivedContent(b []byte) io.ReadSeekCloser {
	if b == nil {
		return nil
	}
	return nopCloser{bytes.NewReader(b)}
}

// streamThrottled writes size bytes of content in small chunks at roughly
// bytesPerSec, flushing after each one so a client copying the response
// body observes its progress growing over time instead of getting it all
// at once; it does not support Range (SetFileThrottle is a dev/e2e knob,
// not used together with partial requests).
func streamThrottled(w http.ResponseWriter, r *http.Request, content io.Reader, size int64, bytesPerSec int) {
	const tick = 100 * time.Millisecond
	chunk := make([]byte, max(1, bytesPerSec/10))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for {
		n, err := io.ReadFull(content, chunk)
		if n > 0 {
			if _, werr := w.Write(chunk[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil { // io.EOF / io.ErrUnexpectedEOF: all sent
			return
		}
		select {
		case <-time.After(tick):
		case <-r.Context().Done():
			return
		}
	}
}

// copyBodyThrottled copies r's body to dst (capped at limit+1 bytes, so an
// over-limit upload is detected without reading it in full), in small
// chunks at roughly bytesPerSec when bytesPerSec > 0, so a client's upload
// progress can be observed growing over time instead of the body landing
// all at once — the upload-side counterpart of streamThrottled
// (SetUploadThrottle is a dev/e2e knob, not meant to model real network
// behaviour).
func copyBodyThrottled(dst io.Writer, r *http.Request, limit int64, bytesPerSec int) (int64, error) {
	body := io.LimitReader(r.Body, limit+1)
	if bytesPerSec <= 0 {
		return io.Copy(dst, body)
	}
	const tick = 100 * time.Millisecond
	small := make([]byte, max(1, bytesPerSec/10))
	var total int64
	for {
		n, err := body.Read(small)
		if n > 0 {
			if _, werr := dst.Write(small[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
		select {
		case <-time.After(tick):
		case <-r.Context().Done():
			return total, r.Context().Err()
		}
	}
}

// sniffMime guesses a MIME type for an uploaded file the way the real
// server does when the client sends none: by extension first, falling back
// to sniffing the content.
func sniffMime(name string, data []byte) string {
	if ext := filepath.Ext(name); ext != "" {
		if t := stdmime.TypeByExtension(ext); t != "" {
			if i := strings.IndexByte(t, ';'); i >= 0 {
				t = t[:i]
			}
			return t
		}
	}
	return http.DetectContentType(data)
}

// uploadFile implements the server's "simple" upload mode (api4/file.go
// uploadFileStream): channel_id, filename and an optional client_id in the
// query, a raw body with a required Content-Length. Multipart mode is not
// implemented — the client only ever sends one file per request.
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	channelID, filename, clientID := q.Get("channel_id"), q.Get("filename"), q.Get("client_id")
	s.mu.Lock()
	failing := s.chat.failUploads > 0
	if failing {
		s.chat.failUploads--
	}
	member := s.isMemberLocked(channelID, u.ID)
	disabled := s.opts.DisableFileAttachments
	maxSize := s.opts.MaxFileSize
	bps := s.uploadThrottle
	s.mu.Unlock()
	switch {
	// failing is checked first, like createPost's failPosts: "the next n
	// uploads fail" is unconditional, not gated on whether the request
	// would otherwise have succeeded.
	case failing:
		appError(w, 500, "app.upload.upload_data.app_error", "injected failure")
		return
	case disabled:
		// Checked before channel membership, like the real server
		// (docs/research/2026-09-24-mattermost-api-facts.md §7: "проверяется
		// раньше прав на канал") — both give 403 either way, so this only
		// matters for which detail a caller would see, not the status code.
		appError(w, 403, "api.file.attachments.disabled.app_error", "file attachments are disabled")
		return
	case !member:
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	case r.ContentLength <= 0:
		appError(w, 400, "api.file.upload_file.read_request.app_error", "Content-Length is required")
		return
	case r.ContentLength > maxSize:
		appError(w, 413, "api.file.upload_file.too_large_detailed.app_error", "file too large")
		return
	}
	var f *ffile
	if s.filesDir != "" {
		f = s.storeUpload(w, r, channelID, filename, maxSize, bps)
	} else {
		var buf bytes.Buffer
		buf.Grow(int(r.ContentLength)) // checked ≤ maxSize: no spare capacity kept
		if _, err := copyBodyThrottled(&buf, r, maxSize, bps); err != nil {
			appError(w, 400, "api.file.upload_file.read_request.app_error", "could not read the request body")
			return
		}
		if int64(buf.Len()) > maxSize {
			appError(w, 413, "api.file.upload_file.too_large_detailed.app_error", "file too large")
			return
		}
		s.mu.Lock()
		f = s.newFileLocked("", channelID, filename, sniffMime(filename, buf.Bytes()), buf.Bytes())
		s.mu.Unlock()
	}
	if f == nil {
		return
	}
	s.mu.Lock()
	f.info.UserID = u.ID
	info := f.info
	s.mu.Unlock()
	writeJSON(w, 201, map[string]any{
		"file_infos": []model.FileInfo{info},
		"client_ids": []string{clientID},
	})
}

// storeUpload writes an upload to the fake's files dir, streamed, and
// registers it; nil after an error answer. Pictures are decoded from the
// file for their thumbnail and preview.
func (s *Server) storeUpload(w http.ResponseWriter, r *http.Request, channelID, filename string, maxSize int64, bps int) *ffile {
	tmp, err := os.CreateTemp(s.filesDir, "upload-*")
	if err != nil {
		appError(w, 500, "api.file.upload_file.storage.app_error", "could not store the file")
		return nil
	}
	size, err := copyBodyThrottled(tmp, r, maxSize, bps)
	var head [512]byte
	n, _ := tmp.ReadAt(head[:], 0)
	mime := sniffMime(filename, head[:n])
	var d derivedImage
	derivedOK := false
	if err == nil && size <= maxSize && isPicture(mime) {
		if _, err = tmp.Seek(0, io.SeekStart); err == nil {
			d, derivedOK = deriveFrom(tmp, mime)
		}
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		_ = os.Remove(tmp.Name())
		appError(w, 400, "api.file.upload_file.read_request.app_error", "could not read the request body")
		return nil
	case size > maxSize:
		_ = os.Remove(tmp.Name())
		appError(w, 413, "api.file.upload_file.too_large_detailed.app_error", "file too large")
		return nil
	}
	f := newFFile("", channelID, filename, mime, size)
	f.path = tmp.Name()
	if derivedOK {
		f.setDerived(d)
		if d.preview != nil && os.WriteFile(f.path+".preview", d.preview, 0o600) == nil {
			f.previewPath, f.preview = f.path+".preview", nil
		}
	}
	s.mu.Lock()
	s.chat.files[f.info.ID] = f
	s.mu.Unlock()
	return f
}

// FailUploads makes the next n POST /api/v4/files fail with 500, like
// FailPosts does for posts (send-failure/retry tests).
func (s *Server) FailUploads(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chat.failUploads = n
}

// SetMaxFileSize changes the server's advertised/enforced MaxFileSize at
// runtime (e2e: hitting the limit without actually uploading megabytes) — 0
// restores DefaultMaxFileSize, like SetUploadThrottle's 0 restores full
// speed. The client only sees the new value after its next metadata
// refresh (e.g. the e2e helper forces one with fake/drop {lose:true}).
func (s *Server) SetMaxFileSize(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		n = DefaultMaxFileSize
	}
	s.opts.MaxFileSize = n
}

// SetFileAttachmentsEnabled flips EnableFileAttachments at runtime, the
// counterpart to SetMaxFileSize.
func (s *Server) SetFileAttachmentsEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.DisableFileAttachments = !enabled
}

func (s *Server) fileInfo(w http.ResponseWriter, r *http.Request, u User) {
	if f := s.fileFor(w, r, u); f != nil {
		writeJSON(w, 200, f.info)
	}
}

func (s *Server) emojiList(w http.ResponseWriter, r *http.Request, _ User) {
	if s.opts.DisableCustomEmoji {
		appError(w, 501, "api.emoji.disabled.app_error", "custom emoji disabled")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	per, _ := strconv.Atoi(q.Get("per_page"))
	if per <= 0 {
		per = 60
	}
	per = min(per, 200)
	s.mu.Lock()
	all := make([]model.Emoji, 0, len(s.chat.emoji))
	for _, e := range s.chat.emoji {
		all = append(all, e.e)
	}
	s.mu.Unlock()
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	start := min(page*per, len(all))
	writeJSON(w, 200, all[start:min(start+per, len(all))])
}

func (s *Server) emojiPath(w http.ResponseWriter, r *http.Request, _ User) {
	if s.opts.DisableCustomEmoji {
		appError(w, 501, "api.emoji.disabled.app_error", "custom emoji disabled")
		return
	}
	a, b := r.PathValue("a"), r.PathValue("b")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case a == "name":
		for _, e := range s.chat.emoji {
			if e.e.Name == b {
				writeJSON(w, 200, e.e)
				return
			}
		}
		appError(w, 404, "app.emoji.get_by_name.no_result", "no such emoji")
	case b == "image" && s.chat.emoji[a] != nil:
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=2592000, private")
		_, _ = w.Write(s.chat.emoji[a].png)
	default:
		appError(w, 404, "app.emoji.get.no_result", "no such emoji")
	}
}

// ---- test controls ----

// SetPicture gives username a new profile picture and tells everyone
// (user_updated), like an avatar upload. Returns the new version.
func (s *Server) SetPicture(username string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.userIDByName(username)
	at := s.nowLocked()
	s.chat.pictureSeq++
	s.chat.pictures[id] = &picture{at: at, png: seedImages()[fmt.Sprintf("avatar%d", (s.chat.pictureSeq+2)%len(palette))]}
	u, _ := s.userByID(id)
	out := s.userWithPictureLocked(u)
	s.publishLocked("user_updated", map[string]any{"user": out}, wsBroadcast{}, s.allUserIDs(), nil, nil)
	return at
}

// PostFile posts message with one attached file as username (an upload in
// the real client); the post carries the file's info in metadata.files.
func (s *Server) PostFile(channelID, username, message, name, mime string, data []byte) model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := s.userIDByName(username)
	f := s.newFileLocked("", channelID, name, mime, data)
	f.info.UserID = uid // as if uid had just uploaded it: createPostLocked only attaches the uploader's own files
	p, e := s.createPostLocked(uid, model.Post{ChannelID: channelID, Message: message, FileIDs: []string{f.info.ID}})
	if e != nil {
		panic("mmfake: PostFile: " + e.id)
	}
	return p
}

// AddEmoji creates a custom emoji and announces it (emoji_added to all).
func (s *Server) AddEmoji(name string) model.Emoji {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := model.Emoji{ID: "e-" + newID()[:12], Name: name, CreatorID: "u-bob"}
	s.chat.emoji[e.ID] = &femoji{e: e, png: seedImages()["emoji"]}
	b, _ := json.Marshal(e)
	s.publishLocked("emoji_added", map[string]any{"emoji": string(b)}, wsBroadcast{}, s.allUserIDs(), nil, nil)
	return e
}
