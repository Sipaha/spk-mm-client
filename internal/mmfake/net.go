package mmfake

import (
	"net/http"
	"strings"
	"time"
)

// conditions applies the simulated network conditions to every request.
func (s *Server) conditions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		down := s.down
		var delay time.Duration
		for part, d := range s.latency {
			if strings.Contains(r.URL.Path, part) {
				delay = max(delay, d)
			}
		}
		s.mu.Unlock()
		if down {
			http.Error(w, "fake server is down", http.StatusServiceUnavailable)
			return
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// SetDown makes every request (REST and WebSocket upgrades) fail with 503
// while true — the client sees the server as unreachable. Open sockets are
// not touched; DropConnections closes them.
func (s *Server) SetDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

// SetLatency delays every request whose path contains part by d (0 removes
// the delay).
func (s *Server) SetLatency(part string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latency == nil {
		s.latency = map[string]time.Duration{}
	}
	if d <= 0 {
		delete(s.latency, part)
		return
	}
	s.latency[part] = d
}

// RejectResumes makes the WebSocket endpoint close every socket that asks
// to resume (connection_id given) right after the upgrade, without a hello —
// what the real server does with a connection_id it does not accept.
func (s *Server) RejectResumes(reject bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectResumes = reject
}
