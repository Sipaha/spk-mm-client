package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/spk/spk-mm-client/internal/attach"
	"github.com/spk/spk-mm-client/internal/auth"
	"github.com/spk/spk-mm-client/internal/events"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mmsync"
	"github.com/spk/spk-mm-client/internal/store"
)

type Opener func(url string) error

type Service struct {
	st       *store.Store
	em       *events.Emitter
	sso      *auth.SSO
	open     Opener
	hc       *http.Client
	transfer *http.Client // media and downloads: no whole-request timeout
	// callTimeout bounds each interactive call to a Mattermost server as a
	// whole (all REST requests and retries): Wails bindings pass
	// context.Background(), so without it a hung server would freeze the
	// action for minutes.
	callTimeout time.Duration

	co     *events.Coalescer
	nq     *notifyQueue
	getenv func(string) string
	tune   func(*mmsync.Config) // tests: shorter intervals
	// onFocus (tests) sees each worker about to get focus with the channel
	// that SetFocused(true) will then mark read; nil in production.
	onFocus func(serverID int64, activeChannel string)
	focusMu sync.Mutex // serializes applyFocus

	// badgeMu orders badge deliveries: the replay in OnBadge and the
	// fan-out in refreshBadges never interleave, so a late subscriber can't
	// get a stale total after a newer one. Never held together with a
	// callback-reachable s.mu section.
	badgeMu sync.Mutex

	filesMu  sync.Mutex
	saved    map[string]savedEntry // "server/file id" → file saved this session
	saving   map[string]*download  // "server/file id" → download in progress
	progress map[int64]int64       // listed download in progress → bytes received

	// att holds attachments of unsent messages; nil until
	// EnableAttachments (set once, before Start).
	att *attach.Store

	mu       sync.Mutex
	mgr      *mmsync.Manager
	notifier Notifier
	fileOpen Opener   // hands saved files to the system; nil: save only
	reveal   Revealer // shows a saved file in the file manager; nil: open its folder
	// clipboard and picker are the desktop's attachment sources; nil:
	// unsupported (browser mode).
	clipboard Clipboard
	picker    FilePicker
	// streamBase gives the desktop's media stream address; nil: "/media".
	streamBase func() (string, error)
	badgeFns   []func(Badge)
	active     int64 // server shown in the UI
	focused    bool  // window focused and visible
	marks      map[int64]serverMark
	total      Badge
}

const defaultCallTimeout = 15 * time.Second

var _ API = (*Service)(nil)

func NewService(st *store.Store, em *events.Emitter, open Opener, hc *http.Client) *Service {
	s := &Service{st: st, em: em, sso: auth.NewSSO(), open: open, hc: hc, transfer: &http.Client{Transport: transportOf(hc)}, callTimeout: defaultCallTimeout,
		co: events.NewCoalescer(coalesceDelay), getenv: os.Getenv,
		saved: map[string]savedEntry{}, saving: map[string]*download{}, progress: map[int64]int64{}}
	s.nq = newNotifyQueue(notifyBurst, s.deliver)
	return s
}

func (s *Service) dto(srv store.Server) ServerDTO {
	d := ServerDTO{ID: srv.ID, Name: srv.Name, URL: srv.URL, SignedIn: srv.SignedIn(), Username: srv.Username,
		GitLab: srv.GitLab, State: string(mmsync.StatusOff)}
	if m := s.manager(); m != nil {
		if w := m.Worker(srv.ID); w != nil {
			b := w.State().Badge()
			d.State, d.Unread, d.Mentions = string(w.Status()), b.Unread, b.Mentions
		}
	}
	return d
}

func (s *Service) emit(typ string, payload map[string]any) {
	s.em.Emit(events.Event{Type: typ, Payload: payload})
}

// bounded limits one interactive exchange with a Mattermost server.
func (s *Service) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.callTimeout)
}

func (s *Service) getServer(ctx context.Context, id int64) (store.Server, error) {
	srv, err := s.st.GetServer(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Server{}, coded(CodeNotFound, nil)
	}
	if err != nil {
		return store.Server{}, coded(CodeInternal, err)
	}
	return srv, nil
}

func (s *Service) ListServers(ctx context.Context) ([]ServerDTO, error) {
	list, err := s.st.ListServers(ctx)
	if err != nil {
		return nil, coded(CodeInternal, err)
	}
	out := make([]ServerDTO, 0, len(list))
	for _, srv := range list {
		out = append(out, s.dto(srv))
	}
	return out, nil
}

func (s *Service) AddServer(ctx context.Context, rawURL string) (ServerDTO, error) {
	norm, err := rest.NormalizeURL(rawURL)
	if err != nil {
		return ServerDTO{}, coded(CodeInvalidURL, err)
	}
	c := rest.New(norm, "", s.hc)
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	if err := c.Ping(rctx); err != nil {
		if rest.IsNetwork(err) {
			return ServerDTO{}, coded(CodeUnreachable, err)
		}
		return ServerDTO{}, coded(CodeNotMattermost, err)
	}
	cfg, err := c.ClientConfig(rctx)
	if err != nil {
		return ServerDTO{}, coded(CodeNotMattermost, err)
	}
	name := cfg.SiteName
	if name == "" {
		if u, perr := url.Parse(norm); perr == nil {
			name = u.Host
		}
	}
	siteURL := ""
	if cfg.SiteURL != "" {
		if n, err := rest.NormalizeURL(cfg.SiteURL); err == nil {
			siteURL = n
		}
	}
	srv, err := s.st.AddServer(ctx, store.Server{URL: norm, SiteURL: siteURL, Name: name, GitLab: cfg.GitLabEnabled()})
	if errors.Is(err, store.ErrServerExists) {
		return ServerDTO{}, coded(CodeServerExists, nil)
	}
	if err != nil {
		return ServerDTO{}, coded(CodeInternal, err)
	}
	slog.Info("server added", "srv", srv)
	s.emit(EventServersChanged, nil)
	return s.dto(srv), nil
}

// revoke logs the session out server-side, best effort: a dead server or an
// already-revoked token must never keep the user from signing out locally.
func (s *Service) revoke(ctx context.Context, srv store.Server) {
	if !srv.SignedIn() {
		return
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	if err := rest.New(srv.URL, srv.Token, s.hc).Logout(rctx); err != nil {
		slog.Warn("server-side logout failed; clearing locally", "srv", srv, "err", err)
	}
}

func (s *Service) RemoveServer(ctx context.Context, id int64) error {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return err
	}
	s.sso.Cancel(id) // first: a GitLab sign-in finishing now must not start a worker
	s.deactivate(id)
	s.dropAttachments(id)
	s.revoke(ctx, srv)
	if err := s.st.DeleteServer(ctx, id); err != nil {
		return coded(CodeInternal, err)
	}
	s.emit(EventServersChanged, nil)
	return nil
}

func (s *Service) StartGitLabLogin(ctx context.Context, id int64) error {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return err
	}
	if !srv.GitLab {
		return coded(CodeGitLabDisabled, nil)
	}
	s.sso.Begin(id, srv.URL, srv.SiteURL)
	if err := s.open(auth.GitLabLoginURL(srv.URL)); err != nil {
		s.sso.Cancel(id)
		return coded(CodeInternal, err)
	}
	return nil
}

func (s *Service) LoginWithPassword(ctx context.Context, id int64, login, password string) (ServerDTO, error) {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return ServerDTO{}, err
	}
	rctx, cancel := s.bounded(ctx)
	tok, u, err := rest.New(srv.URL, "", s.hc).Login(rctx, login, password)
	cancel()
	if err != nil {
		var re *rest.Error
		if errors.As(err, &re) && (re.Kind == rest.KindAuth || re.Status == http.StatusBadRequest) {
			return ServerDTO{}, coded(CodeBadCredentials, err)
		}
		if rest.IsNetwork(err) {
			return ServerDTO{}, coded(CodeUnreachable, err)
		}
		return ServerDTO{}, coded(CodeInternal, err)
	}
	// Signing in over a live session: revoke the old one (best effort) so it
	// doesn't linger server-side. Done only after the new login succeeded, so
	// a failed attempt leaves the existing session intact. The worker using
	// the old token stops first, so its in-flight work never sees a 401.
	s.deactivate(id)
	s.revoke(ctx, srv)
	if err := s.st.SetSession(ctx, id, tok, u.ID, u.Username); err != nil {
		s.resume(ctx, srv)
		return ServerDTO{}, coded(CodeInternal, err)
	}
	s.activate(ctx, srv)
	s.emit(EventServersChanged, nil)
	srv, err = s.getServer(ctx, id)
	if err != nil {
		return ServerDTO{}, err
	}
	return s.dto(srv), nil
}

func (s *Service) Logout(ctx context.Context, id int64) error {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return err
	}
	s.sso.Cancel(id) // first: a GitLab login still in flight must not sign back in
	s.deactivate(id) // final snapshot flush happens before the cache is dropped below
	s.dropAttachments(id)
	s.revoke(ctx, srv)
	if err := s.st.ClearSession(ctx, id); err != nil {
		return coded(CodeInternal, err)
	}
	if err := s.st.ClearCache(ctx, id); err != nil {
		slog.Warn("cache clear after sign-out failed", "srv", id, "err", err)
	}
	s.emit(EventServersChanged, nil)
	return nil
}

// HandleDeepLink finishes a GitLab SSO login from an mmauth:// callback.
// Duplicate delivery of an already-handled callback is ignored silently.
func (s *Service) HandleDeepLink(ctx context.Context, raw string) error {
	res, err := s.sso.Complete(raw)
	switch {
	case errors.Is(err, auth.ErrAlreadyCompleted):
		return nil
	case errors.Is(err, auth.ErrNoPendingLogin):
		return s.loginFailed(0, coded(CodeNoPendingLogin, err))
	case errors.Is(err, auth.ErrServerMismatch), errors.Is(err, auth.ErrMalformedCallback):
		return s.loginFailed(0, coded(CodeLoginMismatch, err))
	case err != nil:
		return s.loginFailed(0, coded(CodeInternal, err))
	}
	srv, err := s.getServer(ctx, res.ServerID)
	if err != nil {
		var ce *CodedError
		if !errors.As(err, &ce) {
			ce = coded(CodeInternal, err)
		}
		return s.loginFailed(res.ServerID, ce)
	}
	rctx, cancel := s.bounded(ctx)
	u, err := rest.New(srv.URL, res.Token, s.hc).Me(rctx)
	cancel()
	if err != nil {
		return s.loginFailed(res.ServerID, coded(CodeAuthFailed, err))
	}
	s.deactivate(srv.ID) // the old token's worker stops before the token is revoked
	s.revoke(ctx, srv)   // replace, don't orphan, an existing session
	if err := s.st.SetSession(ctx, srv.ID, res.Token, u.ID, u.Username); err != nil {
		s.resume(ctx, srv)
		return s.loginFailed(res.ServerID, coded(CodeInternal, err))
	}
	s.activate(ctx, srv)
	slog.Info("signed in via GitLab", "srv", srv.ID, "username", u.Username)
	s.emit(EventServersChanged, nil)
	return nil
}

func (s *Service) loginFailed(serverID int64, ce *CodedError) error {
	payload := map[string]any{"code": ce.Code}
	if serverID != 0 {
		payload["server_id"] = serverID
	}
	slog.Warn("login failed", "code", ce.Code, "detail", ce.Detail)
	s.emit(EventLoginFailed, payload)
	return ce
}
