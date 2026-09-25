package api

import (
	"context"
	"net/http"

	"github.com/spk/spk-mm-client/internal/media"
	"github.com/spk/spk-mm-client/internal/mmsync"
)

var _ media.Origin = (*Service)(nil)

// running is the worker of a signed-in server with a live session; media
// never fetches for a server that needs a new sign-in.
func (s *Service) running(id int64) *mmsync.Worker {
	m := s.manager()
	if m == nil {
		return nil
	}
	w := m.Worker(id)
	if w == nil || w.Status() == mmsync.StatusNeedsReauth {
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
	return w.REST().WithHTTPClient(s.transfer).Stream(ctx, path, hdr)
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
