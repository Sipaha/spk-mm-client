// Package mmfake is an in-process fake of the Mattermost server subset
// spk-mattermost talks to, plus a fake GitLab consent page for the mobile
// SSO flow. Used by Go tests and by browser mode (--mm-fake) for e2e.
package mmfake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
)

type User struct {
	ID       string
	Username string
	Password string
}

type Options struct {
	SiteName      string // default "Fake MM"
	DisableGitLab bool   // GitLab SSO is advertised and served unless set
	Users         []User // default: alice/secret
}

type Server struct {
	ts       *httptest.Server
	opts     Options
	mu       sync.Mutex
	sessions map[string]string // token -> user id
	pending  map[string]string // oauth state -> redirect_to
}

func Start(o Options) *Server {
	if o.SiteName == "" {
		o.SiteName = "Fake MM"
	}
	if o.Users == nil {
		o.Users = []User{{ID: "u-alice", Username: "alice", Password: "secret"}}
	}
	s := &Server{opts: o, sessions: map[string]string{}, pending: map[string]string{}}
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
	s.ts = httptest.NewServer(mux)
	return s
}

func (s *Server) URL() string { return s.ts.URL }
func (s *Server) Close()      { s.ts.Close() }

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

func userJSON(u User) map[string]string { return map[string]string{"id": u.ID, "username": u.Username} }

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

func (s *Server) clientConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{
		"SiteName":               s.opts.SiteName,
		"SiteURL":                s.ts.URL,
		"Version":                "10.11.0-fake",
		"EnableSignUpWithGitLab": fmt.Sprint(!s.opts.DisableGitLab),
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
	writeJSON(w, 200, userJSON(u))
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
	fmt.Fprintf(w, `<html><body><h1>Fake GitLab</h1><a id="authorize" href="/mmfake/gitlab/complete?state=%s">Authorize as %s</a></body></html>`,
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
	fmt.Fprintf(w, `<html><head><meta http-equiv="refresh" content="2; url=%s"></head><body><h2>Authentication complete</h2><p><a id="mmauth-link" href="%s">Click here</a></p></body></html>`, link, link)
}
