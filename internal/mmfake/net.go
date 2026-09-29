package mmfake

import (
	"net/http"
	"net/http/httptest"
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
		broken := false
		for part := range s.broken {
			if strings.Contains(r.URL.Path, part) {
				broken = true
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
		if broken {
			next.ServeHTTP(httptest.NewRecorder(), r)
			time.Sleep(brokenReplyDelay) // lets the event go out first, as a lost reply would
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

// brokenReplyDelay: how long a broken reply holds the connection after the
// request was applied before closing it.
const brokenReplyDelay = 50 * time.Millisecond

// BreakReplies makes every request whose path contains part be applied and
// then answered with a closed connection instead of a response — the client
// cannot know whether it went through (false restores replies).
func (s *Server) BreakReplies(part string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken == nil {
		s.broken = map[string]bool{}
	}
	if !on {
		delete(s.broken, part)
		return
	}
	s.broken[part] = true
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

// SetFileThrottle makes every plain file download (GET
// /api/v4/files/{id} — not its /thumbnail or /preview, and not /info)
// stream at roughly bytesPerSec, flushing after each small chunk instead of
// answering at once: dev/e2e only, so a screenshot or a manual check can
// catch a download mid-progress (0 restores full-speed serving). Clears any
// earlier SetFileThrottleFor scoping — this always applies to every file.
func (s *Server) SetFileThrottle(bytesPerSec int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileThrottle = bytesPerSec
	s.fileThrottleID = ""
}

// SetFileThrottleFor is SetFileThrottle scoped to one file id: only that
// file's plain download is slowed, so a test that needs a slow window on
// one file (e.g. the PDF mid-load-cancel e2e check) does not also throttle
// every other file in the channel and saturate the browser's connection
// pool with them (Task 5 final review I2). 0 restores full-speed serving
// and clears the scoping.
func (s *Server) SetFileThrottleFor(fileID string, bytesPerSec int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileThrottle = bytesPerSec
	s.fileThrottleID = fileID
}

// fileGetEvt is one file id's most recent plain GET /api/v4/files/{id}:
// whether it has started, and whether the client cancelled it before the
// body finished (streamThrottled sees this via r.Context().Done()). A new
// GET for the same id resets it — each open is its own observation.
type fileGetEvt struct {
	started   bool
	cancelled bool
}

func (s *Server) startFileGet(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileGets == nil {
		s.fileGets = map[string]*fileGetEvt{}
	}
	s.fileGets[id] = &fileGetEvt{started: true}
}

func (s *Server) cancelFileGet(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.fileGets[id]; e != nil {
		e.cancelled = true
	}
}

// FileGetStatus reports id's most recent plain-GET observation (Task 5
// final review I2): a dev/e2e knob so a test can prove the server side of a
// cancelled fetch — internal/media's abandoned-PDF-fetch cancellation —
// instead of inferring it from browser-side request timing, which a
// saturated connection pool (several throttled files at once) can trip
// before the request ever reaches this server.
func (s *Server) FileGetStatus(id string) (started, cancelled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.fileGets[id]
	if e == nil {
		return false, false
	}
	return e.started, e.cancelled
}

// SetUploadThrottle makes POST /api/v4/files (the simple upload mode) read
// its request body slowly, in small chunks, so an e2e test can catch an
// upload's progress mid-way — the upload-side counterpart of
// SetFileThrottle (0 restores full-speed reading).
func (s *Server) SetUploadThrottle(bytesPerSec int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploadThrottle = bytesPerSec
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
