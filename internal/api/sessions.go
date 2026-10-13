package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/spk/spk-mm-client/internal/auth"
	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/store"
)

func (s *Service) StartGitLabLogin(ctx context.Context, id int64) error {
	ctx, finish, err := s.life.start(ctx)
	if err != nil {
		return err
	}
	defer finish()
	g := s.sessionChange(id)
	g.mu.Lock()
	srv, err := s.getServer(ctx, id)
	if err != nil {
		g.mu.Unlock()
		return err
	}
	if !srv.GitLab {
		g.mu.Unlock()
		return coded(CodeGitLabDisabled, nil)
	}
	_, gen := g.advance(s.life.ctx)
	s.sso.BeginAttempt(id, gen, srv.URL, srv.SiteURL)
	g.mu.Unlock()
	if err := s.open(auth.GitLabLoginURL(srv.URL)); err != nil {
		g.mu.Lock()
		if g.current(gen) {
			g.advance(s.life.ctx)
			s.sso.Cancel(id)
		}
		g.mu.Unlock()
		return coded(CodeInternal, err)
	}
	return nil
}

func (s *Service) LoginWithPassword(ctx context.Context, id int64, login, password string) (ServerDTO, error) {
	ctx, finish, err := s.life.start(ctx)
	if err != nil {
		return ServerDTO{}, err
	}
	defer finish()
	g := s.sessionChange(id)
	g.mu.Lock()
	srv, err := s.getServer(ctx, id)
	if err != nil {
		g.mu.Unlock()
		return ServerDTO{}, err
	}
	lctx, gen := g.advance(ctx)
	s.sso.Cancel(id)
	g.mu.Unlock()
	rctx, cancel := s.bounded(lctx)
	tok, u, err := rest.New(srv.URL, "", s.hc).Login(rctx, login, password)
	cancel()
	if err != nil {
		g.mu.Lock()
		current := g.current(gen)
		g.mu.Unlock()
		if !current {
			return ServerDTO{}, coded(CodeCancelled, nil)
		}
		var re *rest.Error
		if errors.As(err, &re) && (re.Kind == rest.KindAuth || re.Status == http.StatusBadRequest) {
			return ServerDTO{}, coded(CodeBadCredentials, err)
		}
		if rest.IsNetwork(err) {
			return ServerDTO{}, coded(CodeUnreachable, err)
		}
		return ServerDTO{}, coded(CodeInternal, err)
	}
	dto, err := s.installSession(lctx, id, gen, tok, u)
	if err != nil {
		s.revoke(s.life.ctx, store.Server{URL: srv.URL, Token: tok})
	}
	return dto, err
}

// installSession checks ownership both before stopping the old worker and
// after best-effort network revocation. No network operation holds g.mu.
func (s *Service) installSession(ctx context.Context, id int64, gen uint64, token string, u model.User) (ServerDTO, error) {
	g := s.sessionChange(id)
	g.mu.Lock()
	if !g.current(gen) || ctx.Err() != nil || s.life.ctx.Err() != nil {
		g.mu.Unlock()
		return ServerDTO{}, coded(CodeCancelled, nil)
	}
	before, err := s.getServer(ctx, id)
	if err != nil {
		g.mu.Unlock()
		return ServerDTO{}, err
	}
	s.deactivate(id)
	g.mu.Unlock()
	if before.Token != token {
		s.revoke(ctx, before)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.current(gen) || ctx.Err() != nil || s.life.ctx.Err() != nil {
		if g.gen == gen && s.life.ctx.Err() == nil {
			s.resume(s.life.ctx, before)
		}
		return ServerDTO{}, coded(CodeCancelled, nil)
	}
	// Once the local commit begins, cancellation of a UI waiter must not
	// leave persisted credentials without their worker. Shutdown still cancels it.
	local, cancel := s.bounded(s.life.ctx)
	defer cancel()
	if err := s.st.ReplaceSession(local, id, token, u.ID, u.Username); err != nil {
		s.resume(local, before)
		return ServerDTO{}, coded(CodeInternal, err)
	}
	s.activate(local, before)
	s.emit(EventServersChanged, nil)
	after, err := s.getServer(local, id)
	if err != nil {
		return ServerDTO{}, err
	}
	return s.dto(after), nil
}

func (s *Service) Logout(ctx context.Context, id int64) error { return s.endSession(ctx, id, false) }
func (s *Service) RemoveServer(ctx context.Context, id int64) error {
	return s.endSession(ctx, id, true)
}

func (s *Service) endSession(ctx context.Context, id int64, remove bool) error {
	ctx, finish, err := s.life.start(ctx)
	if err != nil {
		return err
	}
	defer finish()
	g := s.sessionChange(id)
	g.mu.Lock()
	srv, err := s.getServer(ctx, id)
	if err != nil {
		g.mu.Unlock()
		return err
	}
	g.advance(s.life.ctx)
	s.sso.Cancel(id)
	s.deactivate(id)
	s.dropAttachments(id)
	if remove {
		err = s.st.DeleteServer(ctx, id)
	} else {
		err = s.st.EndSession(ctx, id)
	}
	g.mu.Unlock()
	if err != nil {
		return coded(CodeInternal, err)
	}
	s.emit(EventServersChanged, nil)
	s.revoke(ctx, srv)
	return nil
}

// HandleDeepLink carries the generation captured when the browser login
// began. An obsolete callback completes quietly and can never install a token.
func (s *Service) HandleDeepLink(ctx context.Context, raw string) error {
	ctx, finish, err := s.life.start(ctx)
	if err != nil {
		return err
	}
	defer finish()
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
	g := s.sessionChange(res.ServerID)
	g.mu.Lock()
	if !g.current(res.Generation) {
		g.mu.Unlock()
		return nil
	}
	lctx := g.ctx
	srv, err := s.getServer(ctx, res.ServerID)
	g.mu.Unlock()
	if err != nil {
		return s.loginFailed(res.ServerID, coded(CodeInternal, err))
	}
	actx, acancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lctx, acancel)
	defer func() { stop(); acancel() }()
	rctx, cancel := s.bounded(actx)
	u, err := rest.New(srv.URL, res.Token, s.hc).Me(rctx)
	cancel()
	if err == nil {
		_, err = s.installSession(actx, srv.ID, res.Generation, res.Token, u)
	}
	if err != nil {
		g.mu.Lock()
		current := g.current(res.Generation)
		g.mu.Unlock()
		s.revoke(s.life.ctx, store.Server{URL: srv.URL, Token: res.Token})
		if !current || ctx.Err() != nil || codeOfSessionError(err) == CodeCancelled {
			return nil
		}
		return s.loginFailed(res.ServerID, coded(CodeAuthFailed, err))
	}
	slog.Info("signed in via GitLab", "srv", srv.ID, "username", u.Username)
	return nil
}

func codeOfSessionError(err error) string {
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}
