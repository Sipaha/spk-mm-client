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
	"net/http"
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
	data      []byte
	thumb     []byte // JPEG fitted into 120×100; nil for non-images
	preview   []byte // JPEG ≤1920 wide; nil unless HasPreviewImage
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
func scaledJPEG(data []byte, maxW, maxH int) []byte {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
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
	if id == "" {
		id = "f-" + newID()[:12]
	}
	ext := ""
	if i := strings.LastIndex(name, "."); i >= 0 {
		ext = strings.ToLower(name[i+1:])
	}
	f := &ffile{channelID: channelID, data: data,
		info: model.FileInfo{ID: id, Name: name, Extension: ext, Size: int64(len(data)), MimeType: mime}}
	if strings.HasPrefix(mime, "image/") && mime != "image/svg+xml" {
		if d, ok := derive(data, mime); ok {
			f.info.Width, f.info.Height, f.thumb, f.preview = d.w, d.h, d.thumb, d.preview
			f.info.HasPreviewImage = d.preview != nil
		}
	}
	s.chat.files[id] = f
	return f
}

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
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return derivedImage{}, false
	}
	d := derivedImage{w: cfg.Width, h: cfg.Height, thumb: scaledJPEG(data, 120, 100)}
	if mime != "image/gif" {
		d.preview = scaledJPEG(data, 1920, 0)
	}
	derived.Store(key, d)
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
		data, ctype := f.data, f.info.MimeType
		switch what {
		case "thumbnail":
			data, ctype = f.thumb, "image/jpeg"
		case "preview":
			data, ctype = f.preview, "image/jpeg"
		}
		if data == nil {
			appError(w, 400, "api.file.get_file_"+what+".no_"+what+".app_error", "no "+what)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		s.mu.Lock()
		bps := s.fileThrottle
		s.mu.Unlock()
		if what == "" && bps > 0 {
			streamThrottled(w, r, data, bps)
			return
		}
		http.ServeContent(w, r, f.info.Name, time.Time{}, bytes.NewReader(data))
	}
}

// streamThrottled writes data in small chunks at roughly bytesPerSec,
// flushing after each one so a client copying the response body observes
// its progress growing over time instead of getting it all at once; it
// does not support Range (SetFileThrottle is a dev/e2e knob, not used
// together with partial requests).
func streamThrottled(w http.ResponseWriter, r *http.Request, data []byte, bytesPerSec int) {
	const tick = 100 * time.Millisecond
	chunk := max(1, bytesPerSec/10)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for len(data) > 0 {
		n := min(chunk, len(data))
		if _, err := w.Write(data[:n]); err != nil {
			return
		}
		data = data[n:]
		if flusher != nil {
			flusher.Flush()
		}
		if len(data) == 0 {
			return
		}
		select {
		case <-time.After(tick):
		case <-r.Context().Done():
			return
		}
	}
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
	s.publishLocked("user_updated", map[string]any{"user": out}, wsBroadcast{}, s.allUserIDs(), nil)
	return at
}

// PostFile posts message with one attached file as username (an upload in
// the real client); the post carries the file's info in metadata.files.
func (s *Server) PostFile(channelID, username, message, name, mime string, data []byte) model.Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.newFileLocked("", channelID, name, mime, data)
	p, e := s.createPostLocked(s.userIDByName(username), model.Post{ChannelID: channelID, Message: message, FileIDs: []string{f.info.ID}})
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
	s.publishLocked("emoji_added", map[string]any{"emoji": string(b)}, wsBroadcast{}, s.allUserIDs(), nil)
	return e
}
