// Package auth implements the GitLab SSO login through Mattermost's native
// app flow: /oauth/gitlab/mobile_login?redirect_to=mmauth://callback ends
// with the OS handing us mmauth://callback?MMAUTHTOKEN=…&MMCSRF=…&srv=….
// mmauth:// is the server's default allowed scheme; loopback redirects are
// rejected by the server (verified 2026-09-24), so there is no alternative.
package auth

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-mattermost/internal/mm/rest"
)

const (
	CallbackURL = "mmauth://callback"
	scheme      = "mmauth"
	pendingTTL  = 10 * time.Minute
	recentTTL   = 2 * time.Minute
)

var (
	ErrNoPendingLogin    = errors.New("no login in progress")
	ErrServerMismatch    = errors.New("login callback does not match a server being signed in")
	ErrMalformedCallback = errors.New("malformed login callback")
	// ErrAlreadyCompleted: the same callback delivered twice (Wails launch
	// event + second-instance args, or a double click). Callers ignore it.
	ErrAlreadyCompleted = errors.New("login callback already handled")
)

type Result struct {
	ServerID int64
	Token    string
}

func GitLabLoginURL(serverURL string) string {
	return serverURL + "/oauth/gitlab/mobile_login?redirect_to=" + url.QueryEscape(CallbackURL)
}

func IsCallbackURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), scheme+"://")
}

func FindCallbackArg(args []string) (string, bool) {
	for _, a := range args {
		if IsCallbackURL(a) {
			return a, true
		}
	}
	return "", false
}

type pending struct {
	urls    []string
	started time.Time
}

type SSO struct {
	mu      sync.Mutex
	pending map[int64]pending
	recent  map[string]time.Time // completed tokens
	now     func() time.Time
}

func NewSSO() *SSO {
	return &SSO{pending: map[int64]pending{}, recent: map[string]time.Time{}, now: time.Now}
}

// Begin records that serverID is signing in; urls are every URL the server
// may report as srv (entered URL and SiteURL). Restarting replaces the entry.
func (s *SSO) Begin(serverID int64, urls ...string) {
	var norm []string
	for _, u := range urls {
		if n, err := rest.NormalizeURL(u); err == nil {
			norm = append(norm, n)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[serverID] = pending{urls: norm, started: s.now()}
}

func (s *SSO) Cancel(serverID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, serverID)
}

func (s *SSO) Complete(raw string) (Result, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, scheme) {
		return Result{}, ErrMalformedCallback
	}
	q := u.Query()
	token := q.Get("MMAUTHTOKEN")
	if token == "" {
		return Result{}, ErrMalformedCallback
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, p := range s.pending {
		if now.Sub(p.started) > pendingTTL {
			delete(s.pending, id)
		}
	}
	for tok, at := range s.recent {
		if now.Sub(at) > recentTTL {
			delete(s.recent, tok)
		}
	}
	if _, dup := s.recent[token]; dup {
		return Result{}, ErrAlreadyCompleted
	}
	if len(s.pending) == 0 {
		return Result{}, ErrNoPendingLogin
	}

	var id int64
	if srv := q.Get("srv"); srv != "" {
		n, err := rest.NormalizeURL(srv)
		if err != nil {
			return Result{}, ErrMalformedCallback
		}
		found := false
		for pid, p := range s.pending {
			for _, pu := range p.urls {
				if pu == n {
					id, found = pid, true
				}
			}
		}
		if !found {
			return Result{}, ErrServerMismatch
		}
	} else {
		// Servers older than the srv parameter: only unambiguous when a
		// single login is in flight.
		if len(s.pending) != 1 {
			return Result{}, ErrServerMismatch
		}
		for pid := range s.pending {
			id = pid
		}
	}
	delete(s.pending, id)
	s.recent[token] = now
	return Result{ServerID: id, Token: token}, nil
}
