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
	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mmsync"
	"github.com/spk/spk-mm-client/internal/store"
)

type Opener func(url string) error

type Service struct {
	build      BuildInfo // immutable constructor metadata, never server credentials
	life       *lifetime
	closeOnce  sync.Once
	sessionsMu sync.Mutex
	sessions   map[int64]*sessionChange

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
	protected string // the app's data directory: never staged
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

func NewService(st *store.Store, em *events.Emitter, open Opener, hc *http.Client, builds ...BuildInfo) *Service {
	build := BuildInfo{Version: "dev"}
	if len(builds) > 0 {
		build = builds[0]
	}
	s := &Service{build: build, life: newLifetime(), sessions: map[int64]*sessionChange{}, st: st, em: em, sso: auth.NewSSO(), open: open, hc: hc, transfer: &http.Client{Transport: transportOf(hc)}, callTimeout: defaultCallTimeout,
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
	if srv.SignedIn() {
		d.MediaScope = media.AccountScope(srv.UserID)
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

func (s *Service) loginFailed(serverID int64, ce *CodedError) error {
	payload := map[string]any{"code": ce.Code}
	if serverID != 0 {
		payload["server_id"] = serverID
	}
	slog.Warn("login failed", "code", ce.Code, "detail", ce.Detail)
	s.emit(EventLoginFailed, payload)
	return ce
}
