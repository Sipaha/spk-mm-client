// Package mmfake is an in-process fake of the Mattermost server subset
// spk-mm-client talks to, plus a fake GitLab consent page for the mobile
// SSO flow. Used by Go tests and by browser mode (--mm-fake) for e2e.
package mmfake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

type User struct {
	ID        string
	Username  string
	Password  string
	FirstName string
	LastName  string
}

// ExtraUserNames is the fixed pool Options.ExtraUsers draws from, in
// order — exported so callers building on the fake (e.g.
// cmd/spk-mm-client/browser.go's test-api routes) can react/post as one by
// name without hardcoding the list a second time.
var ExtraUserNames = []string{"dave", "erin", "frank", "grace", "heidi", "ivan", "judy", "mallory", "niaj", "olivia"}

type Options struct {
	SiteName      string // default DefaultSiteName
	DisableGitLab bool   // GitLab SSO is advertised and served unless set
	Users         []User // default: alice/bob/carol, password "secret"
	// ExtraUsers appends this many more real, named, resolvable accounts
	// (from ExtraUserNames, capped at its length) to Users at Start —
	// before the server begins serving, so it never mutates the directory
	// concurrently with the many unlocked reads of Users elsewhere in this
	// package (AGENTS.md: Options.Users is assumed immutable after Start).
	// Also added as members of c-town by seed(). For tests/e2e that need
	// more than bob/carol's two real accounts to react (e.g. a reactor
	// list long enough to prove truncation with names that actually
	// resolve, not synthetic ids — see ReactAsUnknown for the opposite,
	// deliberately-unresolvable case).
	ExtraUsers int

	CRT           bool // CollapsedThreads client config: always_on vs disabled
	SeedPosts     int  // 0 -> 150 posts in Town Square; <0 -> none
	ExtraChannels int  // open "load-NNN" channels with ExtraChannelPosts posts each
	// ExtraChannelPosts: posts seeded in each load channel, 0 -> 20.
	ExtraChannelPosts int
	// KeepPosts > 0 keeps only the newest KeepPosts posts of each channel
	// and records no event log (Events): a soak run's fake then holds a
	// fixed amount however long the churn posts. 0 keeps everything.
	KeepPosts  int
	SinceLimit int // 0 -> 1000

	DisableCustomEmoji bool // custom emoji endpoints answer 501 and the config says false

	// DisablePostOverrides: EnablePostUsernameOverride and
	// EnablePostIconOverride say false (the fake allows both by default).
	DisablePostOverrides bool
	// ImageProxy: HasImageProxy is true and GET /api/v4/image serves the
	// pictures registered with SetProxiedImage (webhook.go).
	ImageProxy bool

	DisableFileAttachments bool  // EnableFileAttachments client config; POST /api/v4/files answers 403 when set
	MaxFileSize            int64 // bytes; 0 -> DefaultMaxFileSize

	// FilesDir: uploaded files (POST /api/v4/files) are stored on disk in a
	// directory of their own under it, removed on Close, instead of in
	// memory. The dev desktop runs the fake in its own process, and a memory
	// check of the client must not measure the fake keeping every upload.
	// "" keeps them in memory (tests).
	FilesDir string
}

// DefaultMaxFileSize mirrors the real server's default (model/config.go,
// FileSettings.MaxFileSize): 100 MiB.
const DefaultMaxFileSize int64 = 100 << 20

type Server struct {
	ts       *httptest.Server
	opts     Options
	mu       sync.Mutex
	sessions map[string]string // token -> user id
	pending  map[string]string // oauth state -> redirect_to
	chat     chatData
	hub      wsHub

	// network conditions (test controls, see net.go)
	down           bool
	latency        map[string]time.Duration
	failures       map[string]failure
	broken         map[string]bool // BreakReplies
	rejectResumes  bool
	hits           map[string]int
	fileThrottle   int // bytes/sec, 0 = full speed (SetFileThrottle)
	uploadThrottle int // bytes/sec, 0 = full speed (SetUploadThrottle)

	filesDir string // uploads on disk (Options.FilesDir); "" = in memory

	proxied map[string][]byte // image proxy: url → picture (SetProxiedImage)
}

// DefaultSiteName is the fake's site (and so server) name unless set.
const DefaultSiteName = "Fake MM"

func Start(o Options) *Server {
	if o.SiteName == "" {
		o.SiteName = DefaultSiteName
	}
	if o.Users == nil {
		o.Users = []User{
			{ID: "u-alice", Username: "alice", Password: "secret"},
			{ID: "u-bob", Username: "bob", Password: "secret"},
			{ID: "u-carol", Username: "carol", Password: "secret"},
		}
	}
	for i := 0; i < o.ExtraUsers && i < len(ExtraUserNames); i++ {
		name := ExtraUserNames[i]
		o.Users = append(o.Users, User{ID: "u-" + name, Username: name, Password: "secret"})
	}
	if o.MaxFileSize == 0 {
		o.MaxFileSize = DefaultMaxFileSize
	}
	s := &Server{opts: o, sessions: map[string]string{}, pending: map[string]string{}, hub: wsHub{sessions: map[string]*wsSession{}}}
	if o.FilesDir != "" {
		if err := os.MkdirAll(o.FilesDir, 0o700); err != nil {
			slog.Warn("mmfake: files dir unavailable, uploads kept in memory", "err", err)
		} else if s.filesDir, err = os.MkdirTemp(o.FilesDir, "files-*"); err != nil {
			slog.Warn("mmfake: files dir unavailable, uploads kept in memory", "err", err)
		}
	}
	s.seed()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/system/ping", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "OK"})
	})
	mux.HandleFunc("GET /api/v4/config/client", s.clientConfig)
	mux.HandleFunc("POST /api/v4/users/login", s.login)
	mux.HandleFunc("GET /api/v4/users/me", s.me)
	mux.HandleFunc("POST /api/v4/users/logout", s.logout)
	mux.HandleFunc("GET /oauth/gitlab/mobile_login", s.mobileLogin)
	mux.HandleFunc("GET /mmfake/gitlab/authorize", s.gitlabAuthorize)
	mux.HandleFunc("GET /mmfake/gitlab/complete", s.gitlabComplete)
	mux.HandleFunc("GET /api/v4/websocket", s.websocketHandler)
	s.chatRoutes(mux)
	s.mediaRoutes(mux)
	s.reactionRoutes(mux)
	s.threadRoutes(mux)
	s.webhookRoutes(mux)
	s.ts = httptest.NewServer(s.conditions(mux))
	return s
}

func (s *Server) URL() string { return s.ts.URL }
func (s *Server) Close() {
	s.DropConnections(true)
	s.ts.Close()
	if s.filesDir != "" {
		_ = os.RemoveAll(s.filesDir)
	}
}

func (s *Server) ActiveSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *Server) RevokeAll() {
	s.mu.Lock()
	s.sessions = map[string]string{}
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func appError(w http.ResponseWriter, status int, id, msg string) {
	writeJSON(w, status, map[string]any{"id": id, "message": msg, "status_code": status})
}

func newID() string {
	b := make([]byte, 13)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) userByID(id string) (User, bool) {
	for _, u := range s.opts.Users {
		if u.ID == id {
			return u, true
		}
	}
	return User{}, false
}

func userJSON(u User) model.User {
	return model.User{ID: u.ID, Username: u.Username, FirstName: u.FirstName, LastName: u.LastName}
}

func (s *Server) newSession(userID string) string {
	tok := newID()
	s.mu.Lock()
	s.sessions[tok] = userID
	s.mu.Unlock()
	return tok
}

func (s *Server) authed(r *http.Request) (User, string, bool) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	uid, ok := s.sessions[tok]
	s.mu.Unlock()
	if !ok {
		return User{}, "", false
	}
	u, ok := s.userByID(uid)
	return u, tok, ok
}

// handleAuthed wraps h with session lookup: 401 api.context.session_expired.app_error
// when the bearer token is unknown, otherwise h runs with the resolved user.
func (s *Server) handleAuthed(h func(w http.ResponseWriter, r *http.Request, u User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _, ok := s.authed(r)
		if !ok {
			appError(w, 401, "api.context.session_expired.app_error", "Invalid or expired session, please login again.")
			return
		}
		h(w, r, u)
	}
}

func (s *Server) clientConfig(w http.ResponseWriter, _ *http.Request) {
	// MaxFileSize/DisableFileAttachments can change at runtime (e2e:
	// SetMaxFileSize/SetFileAttachmentsEnabled) — read every field under the
	// same lock those setters use.
	s.mu.Lock()
	fileAttachments, maxFileSize, crt := !s.opts.DisableFileAttachments, s.opts.MaxFileSize, s.chat.crtMode
	s.mu.Unlock()
	overrides := fmt.Sprint(!s.opts.DisablePostOverrides)
	writeJSON(w, 200, map[string]string{
		"SiteName":                   s.opts.SiteName,
		"SiteURL":                    s.ts.URL,
		"Version":                    "10.11.0-fake",
		"EnableSignUpWithGitLab":     fmt.Sprint(!s.opts.DisableGitLab),
		"CollapsedThreads":           crt,
		"TeammateNameDisplay":        "username",
		"EnableCustomEmoji":          fmt.Sprint(!s.opts.DisableCustomEmoji),
		"EnableFileAttachments":      fmt.Sprint(fileAttachments),
		"MaxFileSize":                strconv.FormatInt(maxFileSize, 10),
		"EnablePostUsernameOverride": overrides,
		"EnablePostIconOverride":     overrides,
		"HasImageProxy":              fmt.Sprint(s.opts.ImageProxy),
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LoginID  string `json:"login_id"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	for _, u := range s.opts.Users {
		if u.Username == in.LoginID && u.Password == in.Password {
			w.Header().Set("Token", s.newSession(u.ID))
			writeJSON(w, 200, userJSON(u))
			return
		}
	}
	appError(w, 401, "api.user.login.invalid_credentials_email_username", "Enter a valid email or username and/or password.")
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, _, ok := s.authed(r)
	if !ok {
		appError(w, 401, "api.context.session_expired.app_error", "Invalid or expired session, please login again.")
		return
	}
	out := userJSON(u)
	out.NotifyProps = map[string]string{
		"desktop": "mention", "channel": "true", "desktop_threads": "all", "mention_keys": "", "first_name": "false",
	}
	s.mu.Lock()
	if pic := s.chat.pictures[u.ID]; pic != nil {
		out.LastPictureUpdate = pic.at
	}
	s.mu.Unlock()
	writeJSON(w, 200, out)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	_, tok, ok := s.authed(r)
	if !ok {
		appError(w, 401, "api.context.session_expired.app_error", "Invalid or expired session, please login again.")
		return
	}
	s.mu.Lock()
	delete(s.sessions, tok)
	s.mu.Unlock()
	writeJSON(w, 200, map[string]string{"status": "OK"})
}

// mobileLogin mirrors server/channels/web/oauth.go: only redirect_to values
// prefixed by an allowed custom scheme (default mmauth://) are accepted.
func (s *Server) mobileLogin(w http.ResponseWriter, r *http.Request) {
	redirect := r.URL.Query().Get("redirect_to")
	if !strings.HasPrefix(strings.ToLower(redirect), "mmauth://") {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("<html><body>Invalid custom url scheme has been provided</body></html>"))
		return
	}
	if s.opts.DisableGitLab {
		appError(w, 501, "api.user.authorize_oauth_user.unsupported.app_error", "GitLab SSO is disabled")
		return
	}
	state := newID()
	s.mu.Lock()
	s.pending[state] = redirect
	s.mu.Unlock()
	http.Redirect(w, r, "/mmfake/gitlab/authorize?state="+state, http.StatusFound)
}

func (s *Server) gitlabAuthorize(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<html><body><h1>Fake GitLab</h1><a id="authorize" href="/mmfake/gitlab/complete?state=%s">Authorize as %s</a></body></html>`,
		url.QueryEscape(state), html.EscapeString(s.opts.Users[0].Username))
}

// gitlabComplete mirrors utils.RenderMobileAuthComplete: a page that
// redirects to redirect_to with MMAUTHTOKEN, MMCSRF and srv appended.
func (s *Server) gitlabComplete(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	s.mu.Lock()
	redirect, ok := s.pending[state]
	delete(s.pending, state)
	s.mu.Unlock()
	if !ok {
		http.Error(w, "unknown state", http.StatusBadRequest)
		return
	}
	tok := s.newSession(s.opts.Users[0].ID)
	q := url.Values{"MMAUTHTOKEN": {tok}, "MMCSRF": {newID()}, "srv": {s.ts.URL}}
	sep := "?"
	if strings.Contains(redirect, "?") {
		sep = "&"
	}
	link := html.EscapeString(redirect + sep + q.Encode())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<html><head><meta http-equiv="refresh" content="2; url=%s"></head><body><h2>Authentication complete</h2><p><a id="mmauth-link" href="%s">Click here</a></p></body></html>`, link, link)
}
