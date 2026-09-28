// Package api is the application surface the UI talks to, independent of
// transport (Wails bindings in desktop, HTTP+SSE in browser mode).
package api

import (
	"context"

	"github.com/spk/spk-mm-client/internal/attach"
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
	// ThreadDTO is a thread in the side panel: root, replies, our replies
	// being sent (the Go thread cache — see AGENTS.md).
	ThreadDTO = state.ThreadView
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
	// OpenThread opens a thread in the panel (replacing the open one): the
	// cached view at once — Loaded=false while the first page is read —
	// then thread_changed as it loads and changes. Retrying a failed load
	// is opening it again.
	OpenThread(ctx context.Context, id int64, channelID, rootID string) (ThreadDTO, error)
	// GetThread is a cached thread's current view (no_post: not cached).
	GetThread(ctx context.Context, id int64, rootID string) (ThreadDTO, error)
	// CloseThread closes the panel; the thread stays cached, trimmed.
	CloseThread(ctx context.Context, id int64) error
	// LoadOlderReplies reads older replies of a thread, up to the cap of
	// 200 (ThreadDTO.Capped).
	LoadOlderReplies(ctx context.Context, id int64, rootID string) error
	// SendPost sends a message with the channel's attachments attachmentIDs
	// (none: text only; the text may be empty when there are some).
	SendPost(ctx context.Context, id int64, channelID, message string, attachmentIDs []string) error
	RetryPost(ctx context.Context, id int64, channelID, pendingID string) error
	DiscardPost(ctx context.Context, id int64, channelID, pendingID string) error
	EditPost(ctx context.Context, id int64, postID, message string) error
	DeletePost(ctx context.Context, id int64, postID string) error
	MarkUnread(ctx context.Context, id int64, postID string) error
	// SetPostSaved saves/unsaves a post for later (flagged_post preference).
	SetPostSaved(ctx context.Context, id int64, postID string, saved bool) error
	SaveDraft(ctx context.Context, id int64, channelID, text string) error
	// SendReply is SendPost for a reply: rootID must be a thread this
	// server's thread cache holds for channelID (open or recent — the
	// panel that offers "reply" always has it open); otherwise no_post.
	// Attachments move from the thread's composer (channelID, rootID), the
	// same way SendPost's move from the channel's.
	SendReply(ctx context.Context, id int64, channelID, rootID, message string, attachmentIDs []string) error
	// SaveThreadDraft is SaveDraft for a reply, bounded to
	// state.ThreadDraftCap entries; rootID must be held (ThreadHeld), else
	// no_post.
	SaveThreadDraft(ctx context.Context, id int64, rootID, text string) error
	DownloadFile(ctx context.Context, id int64, fileID string) (SavedFile, error)
	OpenFile(ctx context.Context, id int64, fileID string) (SavedFile, error)
	AddReaction(ctx context.Context, id int64, postID, emoji string) error
	RemoveReaction(ctx context.Context, id int64, postID, emoji string) error
	// ReactionUsers answers a reaction chip's "who reacted": everyone
	// except me (the UI adds "You"), oldest reaction first.
	ReactionUsers(ctx context.Context, id int64, postID, emoji string) (ReactionUsersDTO, error)
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

	// Attachments are the files attached to a message's composer, in the
	// order added (they come from Go: clipboard, drop, file dialog,
	// browser upload — the UI never sends paths). rootID: "" the channel's
	// own composer, else a thread's reply composer (state.ThreadHeld) —
	// see AGENTS.md "Вложения".
	Attachments(ctx context.Context, id int64, channelID, rootID string) ([]AttachmentView, error)
	// RemoveAttachment drops one (its upload is cancelled).
	RemoveAttachment(ctx context.Context, id int64, attachmentID string) error
	// RetryAttachment sends a failed one again (never done automatically).
	RetryAttachment(ctx context.Context, id int64, attachmentID string) error
	// AttachFromClipboard attaches what the system clipboard holds: copied
	// files by path, or a picture; it returns how many (desktop only — Go
	// reads the clipboard itself; browser mode: unsupported).
	AttachFromClipboard(ctx context.Context, id int64, channelID, rootID string) (int, error)
	// PickAttachments opens the file dialog and attaches the chosen files;
	// it returns how many (desktop only; browser mode: unsupported).
	PickAttachments(ctx context.Context, id int64, channelID, rootID string) (int, error)
}

// Event types pushed to the UI.
const (
	EventServersChanged = "servers_changed" // also on connection state / badge changes
	EventLoginFailed    = "login_failed"    // payload: server_id (optional), code
	EventOpenExternal   = "open_external"   // payload: url — browser mode opens it in a new tab
	EventSidebarChanged = "sidebar_changed" // payload: server_id
	EventChannelChanged = "channel_changed" // payload: server_id, channel_id
	EventOpenChannel    = "open_channel"    // payload: server_id, channel_id — a notification was clicked
	// EventThreadChanged: a cached thread's view changed (loaded, a reply,
	// an edit, a reaction…). Payload: server_id, root_id; coalesced per
	// thread.
	EventThreadChanged = "thread_changed"
	// EventDownloadsChanged: the downloads list changed. Payload: id of the
	// entry (none: many changed); with "received" — progress of a download
	// (at most ~4 a second each; not final — the last bytes may arrive
	// without an event); with "state" — it ended (done | failed): the UI
	// reloads the list then (and on events with neither field), and only
	// patches the row in place on "received".
	EventDownloadsChanged = "downloads_changed"
	// EventAttachmentsChanged: the attachments of a (channel, root)
	// composer changed (added, removed, state, upload progress — at most
	// ~4 a second per upload). Payload: server_id, channel_id, root_id
	// ("" the channel's own composer), items ([]AttachmentView, the whole
	// list).
	EventAttachmentsChanged = "attachments_changed"
	// EventAttachmentRefused: files dropped onto a channel or thread panel
	// were (partly) refused — a drop has no caller to answer. Payload:
	// server_id, channel_id, root_id, code (the first refusal: not_a_file,
	// too_large, …).
	EventAttachmentRefused = "attachment_refused"
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

	// Attachments (internal/attach codes).
	CodeTooLarge            = attach.CodeTooLarge
	CodeTooMany             = attach.CodeTooMany
	CodeAttachmentsDisabled = attach.CodeDisabled
	CodeNotAFile            = attach.CodeNotAFile
	CodeFileChanged         = attach.CodeChanged
	CodeEmptyFile           = attach.CodeEmptyFile
	CodeUnsupported         = "unsupported"      // not in this mode (clipboard, file dialog in browser mode)
	CodeClipboardFailed     = "clipboard_failed" // the clipboard did not answer in time or could not be read
	CodeNoPasteGesture      = "no_paste_gesture" // no Ctrl+V / Shift+Insert just pressed in the window: the clipboard is not read
	CodeNotDropped          = "not_dropped"      // a "dropped" path the native drop did not carry (forged by page script)
	CodeAppData             = "app_data"         // a file of the app's own data directory (database, caches, spools)
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
