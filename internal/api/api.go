// Package api is the application surface the UI talks to, independent of
// transport (Wails bindings in desktop, HTTP+SSE in browser mode).
package api

import "context"

type ServerDTO struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	SignedIn bool   `json:"signed_in"`
	Username string `json:"username"`
	GitLab   bool   `json:"gitlab"`
}

type API interface {
	ListServers(ctx context.Context) ([]ServerDTO, error)
	AddServer(ctx context.Context, rawURL string) (ServerDTO, error)
	RemoveServer(ctx context.Context, id int64) error
	StartGitLabLogin(ctx context.Context, id int64) error
	LoginWithPassword(ctx context.Context, id int64, login, password string) (ServerDTO, error)
	Logout(ctx context.Context, id int64) error
}

// Event types pushed to the UI.
const (
	EventServersChanged = "servers_changed"
	EventLoginFailed    = "login_failed"  // payload: server_id (optional), code
	EventOpenExternal   = "open_external" // payload: url — browser mode opens it in a new tab
)

// Error codes. The UI maps them to localized messages (frontend/src/errors.ts).
const (
	CodeInvalidURL     = "invalid_url"
	CodeUnreachable    = "unreachable"
	CodeNotMattermost  = "not_mattermost"
	CodeServerExists   = "server_exists"
	CodeNotFound       = "not_found"
	CodeGitLabDisabled = "gitlab_disabled"
	CodeBadCredentials = "bad_credentials"
	CodeAuthFailed     = "auth_failed"
	CodeLoginMismatch  = "login_mismatch"
	CodeNoPendingLogin = "no_pending_login"
	CodeInternal       = "internal"
)

// CodedError is what API methods return: a stable code for the UI plus a
// technical detail for logs.
type CodedError struct {
	Code   string
	Detail string
}

func (e *CodedError) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func coded(code string, err error) *CodedError {
	if err == nil {
		return &CodedError{Code: code}
	}
	return &CodedError{Code: code, Detail: err.Error()}
}
