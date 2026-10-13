package api

import (
	"context"
	"net/http"

	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mmsync"
)

var (
	_ media.Origin    = (*Service)(nil)
	_ media.PostIcons = (*Service)(nil)
)

// running is the worker of a signed-in server whose session is live. Media
// never fetches otherwise: offline or reconnecting the request would fail
// (a 502 the UI remembers per picture), and a server that needs a new
// sign-in must not be asked at all. ErrNoServer (404) is not remembered by
// the cache, and the UI retries its failed pictures when the server goes
// live again.
func (s *Service) running(id int64) *mmsync.Worker {
	m := s.manager()
	if m == nil {
		return nil
	}
	w := m.Worker(id)
	if w == nil || w.Status() != mmsync.StatusLive {
		return nil
	}
	return w
}

// Get implements media.Origin: one GET through the REST client of the
// server — its token and its rate limiter, shared with sync — on the
// transfer HTTP client (no whole-request timeout; ctx bounds it).
func (s *Service) Get(ctx context.Context, serverID int64, path string, hdr http.Header) (*http.Response, error) {
	w := s.mediaWorker(ctx, serverID)
	if w == nil {
		return nil, media.ErrNoServer
	}
	resp, err := w.REST().WithHTTPClient(s.transfer).Stream(ctx, path, hdr)
	if err != nil {
		w.CheckAuth(err) // a 401: the session died, ask for a new sign-in
	}
	return resp, err
}

// EmojiID implements media.Origin. A name already in the worker's list
// resolves while offline too (no request; its picture may be on disk);
// asking the server needs a live worker.
func (s *Service) EmojiID(ctx context.Context, serverID int64, name string) (string, error) {
	if m := s.manager(); m != nil {
		w := m.Worker(serverID)
		if bound, ok := ctx.Value(mediaWorkerKey{}).(*mmsync.Worker); ok {
			w = bound
		}
		if w != nil && ctx.Err() == nil && w.Status() != mmsync.StatusNeedsReauth {
			if id, ok := w.State().CustomEmojiID(name); ok {
				return id, nil
			}
		}
	}
	w := s.mediaWorker(ctx, serverID)
	if w == nil {
		return "", media.ErrNoServer
	}
	return w.EmojiID(ctx, name)
}

// PostIcon implements media.PostIcons: the override_icon_url of a post the
// server's worker holds (state decides whether it applies — the server's
// EnablePostIconOverride, a webhook post), with the server's base URL and
// image-proxy flag. Known offline too (its picture may be on disk); Live
// says whether one may be fetched.
func (s *Service) PostIcon(serverID int64, postID string) (media.PostIcon, bool) {
	return s.PostIconContext(context.Background(), serverID, postID)
}

func (s *Service) PostIconContext(ctx context.Context, serverID int64, postID string) (media.PostIcon, bool) {
	m := s.manager()
	if m == nil {
		return media.PostIcon{}, false
	}
	w := m.Worker(serverID)
	if bound, ok := ctx.Value(mediaWorkerKey{}).(*mmsync.Worker); ok {
		w = bound
	}
	if w == nil || ctx.Err() != nil {
		return media.PostIcon{}, false
	}
	st := w.State()
	u, ok := st.PostIconURL(postID)
	if !ok {
		return media.PostIcon{}, false
	}
	return media.PostIcon{URL: u, Base: w.REST().Base(), ImageProxy: st.Config().ImageProxy,
		Live: w.Status() == mmsync.StatusLive}, true
}

// GetIcon implements media.PostIcons: a GET of a live server's image path
// with its session and limiter, on a client that follows only the redirects
// redirect allows (the transfer transport, no whole-request timeout). A
// 401 here is the path's answer, not the session's: no CheckAuth.
func (s *Service) GetIcon(ctx context.Context, serverID int64, path string, redirect func(*http.Request, []*http.Request) error) (*http.Response, error) {
	w := s.mediaWorker(ctx, serverID)
	if w == nil {
		return nil, media.ErrNoServer
	}
	hc := &http.Client{Transport: transportOf(s.transfer), CheckRedirect: redirect}
	return w.REST().WithHTTPClient(hc).Stream(ctx, path, nil)
}

// SetMediaStreamBase makes MediaStreamBase ask fn (the desktop's lazily
// started loopback stream server) instead of answering "/media".
func (s *Service) SetMediaStreamBase(fn func() (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamBase = fn
}

// MediaStreamBase implements API.
func (s *Service) MediaStreamBase(context.Context) (string, error) {
	s.mu.Lock()
	fn := s.streamBase
	s.mu.Unlock()
	if fn == nil {
		return "/media", nil
	}
	base, err := fn()
	if err != nil {
		return "", coded(CodeInternal, err)
	}
	return base, nil
}

func transportOf(hc *http.Client) http.RoundTripper {
	if hc != nil && hc.Transport != nil {
		return hc.Transport
	}
	return http.DefaultTransport
}

type mediaWorkerKey struct{}

// MediaAccount captures the worker before any cache lookup; a stopped or expired
// session cannot authorize disk reads. The context binds upstream fetches too.
func (s *Service) MediaAccount(id int64) (string, context.Context, error) {
	m := s.manager()
	if m == nil {
		return "", nil, media.ErrNoServer
	}
	w, ctx := m.Session(id)
	if w == nil || ctx.Err() != nil || w.Status() == mmsync.StatusNeedsReauth {
		return "", nil, media.ErrNoServer
	}
	scope := media.AccountScope(w.AccountID())
	if scope == "" {
		return "", nil, media.ErrNoServer
	}
	return scope, context.WithValue(ctx, mediaWorkerKey{}, w), nil
}

func (s *Service) mediaWorker(ctx context.Context, id int64) *mmsync.Worker {
	if w, ok := ctx.Value(mediaWorkerKey{}).(*mmsync.Worker); ok {
		if ctx.Err() != nil || w.Status() != mmsync.StatusLive {
			return nil
		}
		return w
	}
	return s.running(id)
}
