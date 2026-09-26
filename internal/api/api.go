// Package api is the application surface the UI talks to, independent of
// transport (Wails bindings in desktop, HTTP+SSE in browser mode).
package api

import (
	"context"

	"github.com/spk/spk-mm-client/internal/state"
)

type ServerDTO struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	SignedIn bool   `json:"signed_in"`
	Username string `json:"username"`
	GitLab   bool   `json:"gitlab"`
	// State of the sync worker: off | connecting | live | reconnecting | needs_reauth.
	State    string `json:"state"`
	Unread   bool   `json:"unread"`
	Mentions int    `json:"mentions"`
}

// Views of the hot layer are the UI DTOs.
type (
	// SidebarDTO is a server's sidebar for one team.
	SidebarDTO = state.SidebarView
	// ChannelDTO is an open channel with its posts.
	ChannelDTO = state.ChannelView
	// Badge is the unread/mention summary of a server or of all servers.
	Badge = state.Badge
)

type AppInfo struct {
	// FormatLocale is a BCP 47 tag for dates and times (from LC_TIME), "" = use the UI language.
	FormatLocale string `json:"format_locale"`
}

type API interface {
	ListServers(ctx context.Context) ([]ServerDTO, error)
	AddServer(ctx context.Context, rawURL string) (ServerDTO, error)
	RemoveServer(ctx context.Context, id int64) error
	StartGitLabLogin(ctx context.Context, id int64) error
	LoginWithPassword(ctx context.Context, id int64, login, password string) (ServerDTO, error)
	Logout(ctx context.Context, id int64) error

	AppInfo(ctx context.Context) (AppInfo, error)
	SelectServer(ctx context.Context, id int64) error
	SetFocused(ctx context.Context, focused bool) error
	NetworkChanged(ctx context.Context) error
	OpenURL(ctx context.Context, rawURL string) error
	Sidebar(ctx context.Context, id int64, teamID string) (SidebarDTO, error)
	OpenChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error)
	GetChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error)
	LoadOlder(ctx context.Context, id int64, channelID string) error
	SendPost(ctx context.Context, id int64, channelID, message string) error
	RetryPost(ctx context.Context, id int64, channelID, pendingID string) error
	DiscardPost(ctx context.Context, id int64, channelID, pendingID string) error
	EditPost(ctx context.Context, id int64, postID, message string) error
	DeletePost(ctx context.Context, id int64, postID string) error
	MarkUnread(ctx context.Context, id int64, postID string) error
	SaveDraft(ctx context.Context, id int64, channelID, text string) error
	DownloadFile(ctx context.Context, id int64, fileID string) (SavedFile, error)
	OpenFile(ctx context.Context, id int64, fileID string) (SavedFile, error)
	AddReaction(ctx context.Context, id int64, postID, emoji string) error
	RemoveReaction(ctx context.Context, id int64, postID, emoji string) error
	EmojiInfo(ctx context.Context, id int64) (EmojiDTO, error)
	// MediaStreamBase is where <video>/<audio> take files from:
	// <base>/<server id>/stream/<file id> (the /media/<srv>/<kind>/<key>
	// shape). Browser mode: "/media"; desktop: the loopback stream server
	// with its token (media.Loopback). The desktop value carries that token:
	// Wails logs binding results at Debug, so its log level must never be
	// raised to Debug in a build that ships (see internal/desktop/run.go).
	MediaStreamBase(ctx context.Context) (string, error)

	// Downloads is the browser-like list of saved files, newest first.
	Downloads(ctx context.Context) ([]DownloadView, error)
	// OpenDownload opens a listed file (OpenFile's allowlist): false when
	// its type is only saved.
	OpenDownload(ctx context.Context, id int64) (bool, error)
	// RevealDownload shows a listed file in the file manager.
	RevealDownload(ctx context.Context, id int64) error
	// RemoveDownload drops an entry (not the file); ClearDownloads drops
	// every finished one. Downloads in progress stay.
	RemoveDownload(ctx context.Context, id int64) error
	ClearDownloads(ctx context.Context) error
}

// Event types pushed to the UI.
const (
	EventServersChanged = "servers_changed" // also on connection state / badge changes
	EventLoginFailed    = "login_failed"    // payload: server_id (optional), code
	EventOpenExternal   = "open_external"   // payload: url — browser mode opens it in a new tab
	EventSidebarChanged = "sidebar_changed" // payload: server_id
	EventChannelChanged = "channel_changed" // payload: server_id, channel_id
	EventOpenChannel    = "open_channel"    // payload: server_id, channel_id — a notification was clicked
	// EventDownloadsChanged: the downloads list changed. Payload: id of the
	// entry (none: many changed); with "received" — progress of a download
	// (at most ~4 a second each); with "state" — it ended (done | failed).
	EventDownloadsChanged = "downloads_changed"
)

// Error codes. The UI maps them to localized messages (frontend/src/errors.ts).
const (
	CodeInvalidURL       = "invalid_url"
	CodeUnreachable      = "unreachable"
	CodeNotMattermost    = "not_mattermost"
	CodeServerExists     = "server_exists"
	CodeNotFound         = "not_found"
	CodeGitLabDisabled   = "gitlab_disabled"
	CodeBadCredentials   = "bad_credentials"
	CodeAuthFailed       = "auth_failed"
	CodeLoginMismatch    = "login_mismatch"
	CodeNoPendingLogin   = "no_pending_login"
	CodeInternal         = "internal"
	CodeNotSignedIn      = "not_signed_in"
	CodeSessionExpired   = "session_expired"
	CodeNoChannel        = "no_channel"
	CodeEmptyMessage     = "empty_message"
	CodeForbidden        = "forbidden"
	CodeNoFile           = "no_file"
	CodeNoPost           = "no_post"
	CodeTooManyReactions = "too_many_reactions"
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
