export type ServerState = 'off' | 'connecting' | 'live' | 'reconnecting' | 'needs_reauth'

export interface ServerDTO {
  id: number
  name: string
  url: string
  signed_in: boolean
  username: string
  gitlab: boolean
  state: ServerState
  unread: boolean
  mentions: number
}

export interface AppInfo {
  name?: string
  version?: string
  mode?: string
  format_locale: string // BCP 47 for dates/times; '' = navigator.language
}

// LayoutDTO: saved splitter widths, app-wide (not per server). 0 = never
// saved -- the caller applies its own default.
export interface LayoutDTO {
  sidebar_width: number
  thread_width: number
}

export interface TeamItem {
  id: string
  name: string
  display_name: string
  unread: boolean
  mentions: number
}

export interface ChannelItem {
  id: string
  name: string
  type: string // O | P | D | G
  unread: boolean
  mentions: number
  muted: boolean
  // last_activity_at: last_root_post_at (CRT) or last_post_at, never older
  // than create_at (internal/state/sidebar.go lastActivityLocked) — the
  // Unreads section (sidebarSections.ts) sorts by it. Optional like the
  // other DTO fields below: only Sidebar.tsx's tests need it, and it's
  // always present on the wire (Go never omits a non-omitempty int field).
  last_activity_at?: number
  user_id?: string // DMs: the partner
  avatar?: string // DMs: picture version ('' / absent: profile not loaded)
  status?: string // DMs: presence
  bot?: boolean
  slug?: string // team channels: the URL name a ~channel link uses
}

// Go nil slices arrive as null.
export interface CategoryView {
  id: string
  type: string // favorites | channels | direct_messages | custom
  name: string
  collapsed: boolean
  channels: ChannelItem[] | null
}

export interface SidebarDTO {
  team_id: string
  selected_channel_id: string
  teams: TeamItem[] | null
  categories: CategoryView[] | null
}

export interface QuickChannelDTO {
  server_id: number
  server_name: string
  id: string
  name: string
  type: string
  team_id: string
  team_name: string
  team_display: string
  last_activity_at: number
}

export interface AttachmentField {
  title?: string
  value?: string
  short?: boolean
}

export interface Attachment {
  fallback?: string
  color?: string
  pretext?: string
  author_name?: string
  title?: string
  title_link?: string
  text?: string
  footer?: string
  fields?: AttachmentField[]
}

export interface FileView {
  id: string
  name: string
  ext?: string
  size: number
  mime: string
  width?: number
  height?: number
  has_preview?: boolean
  // staged: a file of a post being sent — id is the attachment id and its
  // picture comes from /media/<srv>/staged/<id>. state/sent/error are its
  // live upload progress (present only while staged): staged | uploading |
  // uploaded | failed, bytes sent so far, the error code of a failure.
  staged?: boolean
  state?: AttachmentState
  sent?: number
  error?: string
}

export type AttachmentState = 'staged' | 'uploading' | 'uploaded' | 'failed'

// AttachmentView is one file attached to a channel's next message (the
// composer tray) — see internal/attach.Attachment / api.AttachmentView.
export interface AttachmentView {
  id: string
  name: string
  size: number
  mime: string
  state: AttachmentState
  sent: number // bytes uploaded so far (== size once uploaded)
  error: string // error code of a failed upload, '' otherwise
}

export interface SavedFile {
  path: string
  opened: boolean
}

// DownloadView is one entry of the browser-like downloads list (newest
// first); see internal/api/downloads.go (api.DownloadView) for the exact
// contract this mirrors.
export interface DownloadView {
  id: number
  server_id: number
  file_id: string
  name: string
  path: string // '' until done
  size: number
  mime: string
  started_at: number // unix ms
  finished_at: number // unix ms, 0 while downloading
  state: 'downloading' | 'done' | 'failed'
  error: string // error code of a failed download
  received: number // bytes so far (downloading), size when done
  exists: boolean // done and the file is still there — else "file deleted", no actions
  openable: boolean // OpenDownload hands this type to the system; otherwise only "show in folder"
}

export interface ReactionView {
  emoji: string
  count: number
  mine: boolean
}

export interface Reactor {
  id: string
  name: string // '' = unknown/profile not loaded — shown as a placeholder
  avatar: string // picture version; '' = none yet
}

// "Who reacted" for a chip's tooltip/modal: everyone except me (the UI adds
// "You"), oldest reaction first.
export interface ReactionUsersDTO {
  users: Reactor[]
  unknown: number
}

export interface EmojiDTO {
  recent: string[] // most used first
  custom: string[]
  custom_enabled: boolean
}

export interface PostView {
  id: string
  user_id: string
  author: string
  real_author?: string // the account behind a webhook's own name (author), for a tooltip
  avatar?: string // picture version of the author; absent: profile not loaded
  // icon: a webhook's own picture — 'post' (/media/<srv>/posticon/<id>) or ':name:' (an emoji); absent: the avatar
  icon?: string
  icon_version?: string // with icon 'post': what the picture is (?v=), so an edited icon is fetched anew
  webhook?: boolean // from_webhook: never grouped with its neighbours
  status?: string // presence of the author (online | away | dnd | offline | ooo)
  root_id?: string
  message: string
  create_at: number
  edit_at?: number
  reply_count?: number
  last_reply_at?: number // the root's newest reply's create_at; 0 = unknown (a non-CRT page carries none)
  system?: boolean
  // ephemeral: only I see it (a slash command's answer), never stored by
  // the server — no actions apply to it.
  ephemeral?: boolean
  // system_author: the server's own ephemeral answer — shown from "System",
  // never from the user who ran the command (author/avatar are empty).
  system_author?: boolean
  bot?: boolean
  pending?: boolean
  failed?: boolean
  pending_post_id?: string
  attachments?: Attachment[]
  files?: FileView[]
  reactions?: ReactionView[]
  saved?: boolean
  // root_author/root_snippet: a reply's context line without CRT ("reply to
  // <author>: <snippet>") — the root's author and a collapsed, cut snippet
  // of its text; absent when the root is not held (show "reply in a thread").
  root_author?: string
  root_snippet?: string
}

export interface ChannelDTO {
  id: string
  name: string
  type: string
  header: string
  purpose: string
  team_id: string
  team_name: string
  posts: PostView[]
  new_since: number
  has_more: boolean
  loaded: boolean
  syncing: boolean
  gap_after: string
  draft: string
  me_id: string
  me_avatar?: string // my own picture version; absent/'' = not loaded yet
  crt: boolean
  muted: boolean
  // gap: the held history (above the window: scrolled up to, or the segment
  // around a post jumped to) may not join the window — open until a page
  // proves it (loadNewer). before_id: the window's first post, the gap row
  // goes before it ('' while the window is empty: at the end of the
  // history). gen moves with each gap opened and each reconnect (a row's
  // key). stale: the history may miss edits/deletions after a catch-up
  // overflow until reread (retryRevalidation) — independent of open.
  gap: HistGap
  // hist_rev moves with every history page applied (anchors tie a pending
  // correction to it).
  hist_rev: number
}

export interface HistGap {
  open: boolean
  gen: number
  before_id: string
  stale: boolean
}

// JumpDTO is where a jump landed; in_feed=false: a reply of a collapsed
// thread (CRT) — the feed does not show it, its thread does.
export interface JumpDTO {
  post_id: string
  root_id: string
  in_feed: boolean
}

// ThreadDTO is a thread in the side panel (Go's bounded thread cache): the
// root first (absent while loading, and once deleted), the replies oldest
// first, then our replies being sent. has_more: older replies can be
// loaded; capped: the 200-reply cap is reached (nothing older is loaded).
// new_since/gap_after are always empty (ChannelDTO's shape).
export interface ThreadDTO {
  root_id: string
  channel_id: string
  channel_name: string
  team_name: string
  posts: PostView[]
  has_more: boolean
  capped: boolean
  loaded: boolean
  syncing: boolean
  root_deleted: boolean
  error: string // code of a failed load ('' none); retry = openThread again
  draft: string
  me_id: string
  crt: boolean
  new_since: number
  gap_after: string
  // focus: the segment around a reply opened with openThreadAt (null: a
  // plain thread) — posts then hold the root, the segment and the tail
  // (the latest ≤ 60 replies), each reply once.
  focus: ThreadFocus | null
}

// ThreadFocus: the reply focused (its permalink is "open in browser");
// has_older/has_newer: a page can be loaded that way (loadThreadFocus) —
// the window holds ≤ 200 replies and lets go of the far edge; gap: between
// the segment and the tail until a page proves they join (before_id: the
// tail's first reply, '' while it is empty; gen: its row's key; stale: the
// segment waits for a reread after a reconnect). rev moves with every page
// or reread applied (anchors); centering is the UI's own nonce.
export interface ThreadFocus {
  target_id: string
  has_older: boolean
  has_newer: boolean
  gap: HistGap
  rev: number
}

export type EventType =
  | 'servers_changed'
  | 'login_failed'
  | 'open_external'
  | 'sidebar_changed'
  | 'channel_changed'
  | 'open_channel'
  | 'downloads_changed'
  | 'attachments_changed'
  | 'attachment_refused'
  | 'thread_changed' // payload: server_id, root_id

export interface ApiEvent {
  type: EventType
  payload?: Record<string, unknown>
}

// Composer autocomplete (Go's mmsync.Autocomplete): only the kind asked for
// is filled. users: the channel's members, others: the team's other
// members; channels: joined ones first; emoji: custom names only (the
// UI adds the standard set); commands: slash commands.
export type AutocompleteKind = 'users' | 'channels' | 'emoji' | 'commands'

export interface ACUser {
  id: string
  username: string
  full_name?: string
  nickname?: string
  avatar?: string // picture version for /media avatars
  status?: string
  bot?: boolean
  me?: boolean
}

export interface ACChannel {
  id: string
  // what ~ inserts; in searchSuggest's answer what in: takes — a team
  // channel's slug, '@username' for a DM, '@a,b,c' for a GM (all members, me too)
  name: string
  display_name: string
  type: string // O | P (searchSuggest: D | G too)
  joined?: boolean
}

export interface ACCommand {
  trigger: string
  hint?: string
  description?: string
}

// Go nil slices arrive as null.
export interface AutocompleteDTO {
  users: ACUser[] | null
  others: ACUser[] | null
  channels: ACChannel[] | null
  emoji: string[] | null
  commands: ACCommand[] | null
}

// Message search (Go's mmsync.SearchPage). A hit is the post as the feed
// shows it plus its channel: channel_name is what in: takes, without a
// DM/GM's '@' (a team channel's slug, a DM partner's username, a GM's
// 'a,b,c'); channel_display the sidebar's name. jumpable=false: a channel
// the client does not know (yet) — channel_name/display/type empty.
// matches: the words the server matched in this very post (Elasticsearch
// only; empty on Bleve/SQL — highlight by the query's terms then).
export interface SearchHit extends PostView {
  channel_id: string
  channel_name: string
  channel_display: string
  channel_type: string
  jumpable: boolean
  matches: string[]
}

// has_next: ask page+1; limit_reached: more exists, but page 24 was the
// last one a search session asks (show "refine the query").
export interface SearchPageDTO {
  hits: SearchHit[]
  has_next: boolean
  limit_reached: boolean
}

// searchSuggest kinds: users (from:), channels (in:).
export type SearchSuggestKind = 'users' | 'channels'
