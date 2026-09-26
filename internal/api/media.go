package api

import (
	"context"
	"net/http"

	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mmsync"
)

var _ media.Origin = (*Service)(nil)

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
	w := s.running(serverID)
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
		if w := m.Worker(serverID); w != nil && w.Status() != mmsync.StatusNeedsReauth {
			if id, ok := w.State().CustomEmojiID(name); ok {
				return id, nil
			}
		}
	}
	w := s.running(serverID)
	if w == nil {
		return "", media.ErrNoServer
	}
	return w.EmojiID(ctx, name)
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
