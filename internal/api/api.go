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

type BuildInfo struct {
	Version string
	Mode    string
}

type AppInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Mode    string `json:"mode"`
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
	// GetLayout returns the saved sidebar/thread-panel splitter widths
	// (0: never saved, the frontend uses its own default). App-wide, not
	// per server.
	GetLayout(ctx context.Context) (LayoutDTO, error)
	// SetSidebarWidth persists the sidebar splitter width, once per drag
	// (pointerup) or keyboard step, not per frame.
	SetSidebarWidth(ctx context.Context, width int) error
	// SetThreadWidth is SetSidebarWidth for the thread panel's splitter.
	SetThreadWidth(ctx context.Context, width int) error
	// GetFormattingBarHidden returns the composer's saved Aa toggle state
	// (false: never saved -- shown by default, the webapp's own default).
	// App-wide, not per server.
	GetFormattingBarHidden(ctx context.Context) (bool, error)
	// SetFormattingBarHidden persists the composer's Aa toggle.
	SetFormattingBarHidden(ctx context.Context, hidden bool) error
	SelectServer(ctx context.Context, id int64) error
	SetFocused(ctx context.Context, focused bool) error
	NetworkChanged(ctx context.Context) error
	OpenURL(ctx context.Context, rawURL string) error
	Sidebar(ctx context.Context, id int64, teamID string) (SidebarDTO, error)
	QuickChannels(ctx context.Context) ([]QuickChannelDTO, error)
	PinnedPosts(ctx context.Context, id int64, channelID string) ([]PinnedPostDTO, error)
	OpenChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error)
	GetChannel(ctx context.Context, id int64, channelID string) (ChannelDTO, error)
	LoadOlder(ctx context.Context, id int64, channelID string) error
	// JumpToPost shows postID of the open channel channelID: a post it
	// holds as is, else the segment around it replaces the held history,
	// behind a gap (ChannelDTO.gap) until proved to join the window. A new
	// navigation: history operations begun before are cancelled. Runs under
	// the caller's ctx (the UI aborts a stale jump). in_feed=false: a
	// collapsed reply (CRT) — the UI opens its thread. post_gone (404 or
	// deleted), forbidden (403), invalid_argument (bad id, another
	// channel's post), no_channel (not the open channel), cancelled.
	JumpToPost(ctx context.Context, id int64, channelID, postID string) (JumpDTO, error)
	// LoadNewer loads a page into the gap between the held history and the
	// window (ChannelDTO.gap). A stale segment is not reread by this call:
	// the page is loaded as is and the background reread is scheduled once
	// it is done (gap.stale until it ends). no_progress: two pages in a row
	// moved nothing — the user retries.
	LoadNewer(ctx context.Context, id int64, channelID string) error
	// RetryRevalidation rereads a stale segment (gap.stale) now.
	RetryRevalidation(ctx context.Context, id int64, channelID string) error
	// OpenThread opens a thread in the panel (replacing the open one): the
	// cached view at once — Loaded=false while the first page is read —
	// then thread_changed as it loads and changes. Retrying a failed load
	// is opening it again.
	OpenThread(ctx context.Context, id int64, channelID, rootID string) (ThreadDTO, error)
	// OpenThreadAt opens rootID's thread in the panel showing replyID (spec
	// «Поиск», Секция 1б): a reply it holds as is (focus unchanged — the UI
	// centers by its own nonce), else the segment around the reply becomes
	// the thread's focus (ThreadDTO.focus), the latest replies its tail,
	// with a gap between until proved closed. Cancels the focus operations
	// begun before; runs under the caller's ctx. post_gone (404 or
	// deleted), forbidden (403), invalid_argument (bad id, a post of
	// another channel or thread), no_channel, cancelled.
	OpenThreadAt(ctx context.Context, id int64, channelID, rootID, replyID string) (ThreadDTO, error)
	// LoadThreadFocus loads a page into the open thread's focus: older
	// replies, or newer ones into the gap to the tail (newer); the window
	// slides past 200. no_progress: two pages in a row moved nothing.
	LoadThreadFocus(ctx context.Context, id int64, rootID string, newer bool) error
	// RetryThreadRevalidation rereads the open thread's stale focus
	// (focus.gap.stale) now.
	RetryThreadRevalidation(ctx context.Context, id int64, rootID string) error
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

	// Autocomplete answers the composer's popup: kind users | channels |
	// emoji | commands, for the composer of channelID (rootID: a thread's
	// composer — its shape is checked, the channel scopes the search) and
	// the word typed after the trigger. Asked only as the user types (the
	// UI debounces and cancels stale calls through ctx); answers are cached
	// briefly per server; offline: empty (emoji: custom ones from the
	// index). invalid_argument for a bad kind or id.
	Autocomplete(ctx context.Context, id int64, kind, channelID, rootID, prefix string) (AutocompleteDTO, error)
	// ExecuteCommand runs a slash command the user sent from a composer
	// (rootID: a held thread's, else no_post); command_not_found when the
	// server has no such trigger. Never called but on an explicit send.
	ExecuteCommand(ctx context.Context, id int64, channelID, rootID, command string) error

	// SearchPosts searches teamID (a team of this server) for terms — the
	// server parses from:, in:, dates, "phrases", -exclusions, word* — and
	// returns page 0…24 (20 posts each, newest first); tzOffset: seconds
	// east of UTC (day bounds of on:/before:/after:). has_next: ask
	// page+1; limit_reached: more exists past the last page. Never retried
	// (a POST); the UI cancels a stale one through ctx. Errors: offline
	// (nothing asked), invalid_argument (team, terms empty or over 1000
	// bytes, page, offset), session_expired, forbidden, cancelled.
	SearchPosts(ctx context.Context, id int64, teamID, terms string, page, tzOffset int) (SearchPageDTO, error)
	// SearchSuggest answers the search box's suggestions in teamID, not
	// tied to a channel: kind users (from: — the team's members) |
	// channels (in: — the team's channels by slug, DMs as "@username", GMs
	// as "@a,b,c" with every member, me too). Cached briefly; offline: no
	// users and the DMs/GMs only. invalid_argument for a bad kind or team.
	SearchSuggest(ctx context.Context, id int64, teamID, kind, prefix string) (AutocompleteDTO, error)
}

// Event types pushed to the UI.
const (
	EventServersChanged = "servers_changed" // also on connection state / badge changes
	EventLoginFailed    = "login_failed"    // payload: server_id (optional), code
	EventOpenExternal   = "open_external"   // payload: url — browser mode opens it in a new tab
	EventSidebarChanged = "sidebar_changed" // payload: server_id
	EventChannelChanged = "channel_changed" // payload: server_id, channel_id
	EventOpenChannel    = "open_channel"    // payload: server_id, channel_id, root_id ("" or the reply's thread to open after the channel) — a notification was clicked
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
	CodeInvalidArgument  = "invalid_argument"
	CodeCommandNotFound  = "command_not_found" // no slash command with that trigger (the UI offers to send it as text)
	CodePostGone         = "post_gone"         // the post jumped to was deleted or does not exist
	CodeNoProgress       = "no_progress"       // loading newer posts moved nothing twice in a row
	CodeCancelled        = "cancelled"         // a newer navigation (or the caller) cancelled it
	CodeOffline          = "offline"           // not connected to the server: nothing was asked (search)
	// CodeCommandUnsupportedInThread: /leave from a thread composer (the
	// server would leave the whole channel) — refused, nothing sent.
	CodeCommandUnsupportedInThread = "command_unsupported_in_thread"
	// CodeCommandUncertain: the command's request timed out or its
	// connection failed — it may or may not have run.
	CodeCommandUncertain = "command_uncertain"

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
