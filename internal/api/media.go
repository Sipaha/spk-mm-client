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

// EmojiID implements media.Origin.
func (s *Service) EmojiID(ctx context.Context, serverID int64, name string) (string, error) {
	w := s.running(serverID)
	if w == nil {
		return "", media.ErrNoServer
	}
	return w.EmojiID(ctx, name)
}

func transportOf(hc *http.Client) http.RoundTripper {
	if hc != nil && hc.Transport != nil {
		return hc.Transport
	}
	return http.DefaultTransport
}
