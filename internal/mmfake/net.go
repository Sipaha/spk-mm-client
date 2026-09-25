package mmfake

import (
	"net/http"
	"strings"
	"time"
)

// failure is an injected error response: status and the AppError id the
// client sees in the body.
type failure struct {
	status int
	id     string
}

// conditions applies the simulated network conditions to every request.
func (s *Server) conditions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		down := s.down
		var fail failure
		for part, f := range s.failures {
			if strings.Contains(r.URL.Path, part) {
				fail = f
			}
		}
		var delay time.Duration
		for part, d := range s.latency {
			if strings.Contains(r.URL.Path, part) {
				delay = max(delay, d)
			}
		}
		if s.hits == nil {
			s.hits = map[string]int{}
		}
		s.hits[r.Method+" "+r.URL.Path]++
		s.mu.Unlock()
		if down {
			http.Error(w, "fake server is down", http.StatusServiceUnavailable)
			return
		}
		if fail.status != 0 {
			appError(w, fail.status, fail.id, "injected failure")
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

// SetFailure makes every request whose path contains part fail with the
// given status (0 removes the failure).
func (s *Server) SetFailure(part string, status int) {
	s.FailWith(part, status, "mmfake.injected_failure")
}

// FailWith is SetFailure with the AppError id the client gets (e.g.
// app.reaction.save.save.too_many_reactions).
func (s *Server) FailWith(part string, status int, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures == nil {
		s.failures = map[string]failure{}
	}
	if status == 0 {
		delete(s.failures, part)
		return
	}
	s.failures[part] = failure{status: status, id: id}
}

// Hits counts requests by method and path (query excluded) since Start.
func (s *Server) Hits(method, path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[method+" "+path]
}
