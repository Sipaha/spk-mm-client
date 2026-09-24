package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/spk/spk-mattermost/internal/auth"
	"github.com/spk/spk-mattermost/internal/events"
	"github.com/spk/spk-mattermost/internal/mm/rest"
	"github.com/spk/spk-mattermost/internal/store"
)

type Opener func(url string) error

type Service struct {
	st   *store.Store
	em   *events.Emitter
	sso  *auth.SSO
	open Opener
	hc   *http.Client
	// callTimeout bounds each interactive call to a Mattermost server as a
	// whole (all REST requests and retries): Wails bindings pass
	// context.Background(), so without it a hung server would freeze the
	// action for minutes.
	callTimeout time.Duration
}

const defaultCallTimeout = 15 * time.Second

var _ API = (*Service)(nil)

func NewService(st *store.Store, em *events.Emitter, open Opener, hc *http.Client) *Service {
	return &Service{st: st, em: em, sso: auth.NewSSO(), open: open, hc: hc, callTimeout: defaultCallTimeout}
}

func toDTO(s store.Server) ServerDTO {
	return ServerDTO{ID: s.ID, Name: s.Name, URL: s.URL, SignedIn: s.SignedIn(), Username: s.Username, GitLab: s.GitLab}
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
		out = append(out, toDTO(srv))
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
	return toDTO(srv), nil
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
	s.revoke(ctx, srv)
	s.sso.Cancel(id)
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
	if err := s.st.SetSession(ctx, id, tok, u.ID, u.Username); err != nil {
		return ServerDTO{}, coded(CodeInternal, err)
	}
	s.emit(EventServersChanged, nil)
	srv, err = s.getServer(ctx, id)
	if err != nil {
		return ServerDTO{}, err
	}
	return toDTO(srv), nil
}

func (s *Service) Logout(ctx context.Context, id int64) error {
	srv, err := s.getServer(ctx, id)
	if err != nil {
		return err
	}
	s.revoke(ctx, srv)
	if err := s.st.ClearSession(ctx, id); err != nil {
		return coded(CodeInternal, err)
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
		return s.loginFailed(res.ServerID, err.(*CodedError))
	}
	rctx, cancel := s.bounded(ctx)
	u, err := rest.New(srv.URL, res.Token, s.hc).Me(rctx)
	cancel()
	if err != nil {
		return s.loginFailed(res.ServerID, coded(CodeAuthFailed, err))
	}
	if err := s.st.SetSession(ctx, srv.ID, res.Token, u.ID, u.Username); err != nil {
		return s.loginFailed(res.ServerID, coded(CodeInternal, err))
	}
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
