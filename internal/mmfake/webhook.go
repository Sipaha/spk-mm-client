package mmfake

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/url"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// WebhookIconPath is a picture the fake serves without a session, like the
// real server's /static/ files — a relative override_icon_url for webhook
// posts (the server's own emoji icons look like this too).
const WebhookIconPath = "/static/images/webhook-icon.png"

// SampleGifPath is a valid animated GIF89a served from /static/ for E2E and
// integration tests of GIF previews.
const SampleGifPath = "/static/images/sample.gif"

// UnauthorizedIconPath answers 401 to everyone (a location behind a
// reverse proxy's own auth); RedirectIconPath?to=<url> redirects there (a
// server-side redirect off the server). Test paths for post icons.
const (
	UnauthorizedIconPath = "/static/images/locked.png"
	RedirectIconPath     = "/static/images/redirect.png"
)

// webhookIcon: GitLab-ish orange stripes, recognisable in a screenshot.
var webhookIcon = patternPNG(64, 64, color.RGBA{226, 67, 41, 255})

// SampleGifBytes is a real 2-frame animated GIF89a (160×100).
var SampleGifBytes = animatedSampleGIF(160, 100)

func animatedSampleGIF(w, h int) []byte {
	pal := color.Palette{
		color.RGBA{34, 42, 62, 255},
		color.RGBA{79, 140, 255, 255},
		color.RGBA{46, 184, 134, 255},
		color.RGBA{245, 247, 250, 255},
	}
	makeFrame := func(shift int) *image.Paletted {
		img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var idx uint8
				if ((x + y + shift) / 20 % 2) == 0 {
					idx = 1
				} else if ((x - y + w + shift) / 20 % 2) == 0 {
					idx = 2
				}
				if x >= 12 && x < w-12 && y >= 12 && y < h-12 && ((x-w/2)*(x-w/2)+(y-h/2)*(y-h/2) < 400) {
					idx = 3
				}
				img.SetColorIndex(x, y, idx)
			}
		}
		return img
	}
	var buf bytes.Buffer
	_ = gif.EncodeAll(&buf, &gif.GIF{
		Image:     []*image.Paletted{makeFrame(0), makeFrame(10)},
		Delay:     []int{25, 25},
		LoopCount: 0,
	})
	return buf.Bytes()
}

func (s *Server) webhookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+WebhookIconPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(webhookIcon)
	})
	mux.HandleFunc("GET "+SampleGifPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/gif")
		_, _ = w.Write(SampleGifBytes)
	})
	mux.HandleFunc("GET "+UnauthorizedIconPath, func(w http.ResponseWriter, _ *http.Request) {
		appError(w, http.StatusUnauthorized, "api.context.session_expired.app_error", "not for you")
	})
	mux.HandleFunc("GET "+RedirectIconPath, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Query().Get("to"), http.StatusFound)
	})
	mux.HandleFunc("GET /api/v4/image", s.handleAuthed(s.imageProxy))
}

// imageProxy mirrors api4/image.go getImage: without the image proxy the
// endpoint refuses (MM-54477); with it, a picture of the site itself is a
// redirect and any other is fetched by the server — here: served from the
// pictures registered with SetProxiedImage (the fake never fetches).
func (s *Server) imageProxy(w http.ResponseWriter, r *http.Request, _ User) {
	if !s.opts.ImageProxy {
		appError(w, http.StatusBadRequest, "api.image.get.app_error", "image proxy disabled")
		return
	}
	raw := r.URL.Query().Get("url")
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		appError(w, http.StatusBadRequest, "api.image.get.app_error", "bad url")
		return
	}
	if u.Host == "" || u.Host == r.Host {
		http.Redirect(w, r, u.RequestURI(), http.StatusFound)
		return
	}
	s.mu.Lock()
	pic := s.proxied[raw]
	s.mu.Unlock()
	if pic == nil {
		appError(w, http.StatusNotFound, "api.image.get.app_error", "not found")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(pic)
}

// SetProxiedImage makes the image proxy answer rawURL with png.
func (s *Server) SetProxiedImage(rawURL string, png []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proxied == nil {
		s.proxied = map[string][]byte{}
	}
	s.proxied[rawURL] = png
}

// WebhookPostAs posts as an incoming webhook owned by username would:
// from_webhook plus the given overrides (override_username,
// override_icon_url, override_icon_emoji, use_user_icon).
func (s *Server) WebhookPostAs(channelID, username, message string, props model.PostProps) model.Post {
	props.FromWebhook = true
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.createPostLocked(s.userIDByName(username), model.Post{ChannelID: channelID, Message: message, Props: props})
	if e != nil {
		panic("mmfake: WebhookPostAs: " + e.id)
	}
	return p
}
