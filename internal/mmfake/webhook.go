package mmfake

import (
	"image/color"
	"net/http"
	"net/url"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// WebhookIconPath is a picture the fake serves without a session, like the
// real server's /static/ files — a relative override_icon_url for webhook
// posts (the server's own emoji icons look like this too).
const WebhookIconPath = "/static/images/webhook-icon.png"

// webhookIcon: GitLab-ish orange stripes, recognisable in a screenshot.
var webhookIcon = patternPNG(64, 64, color.RGBA{226, 67, 41, 255})

func (s *Server) webhookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+WebhookIconPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(webhookIcon)
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
