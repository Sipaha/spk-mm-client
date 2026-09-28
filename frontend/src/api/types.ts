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
  format_locale: string // BCP 47 for dates/times; '' = navigator.language
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
  user_id?: string // DMs: the partner
  avatar?: string // DMs: picture version ('' / absent: profile not loaded)
  status?: string // DMs: presence
  bot?: boolean
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
  avatar?: string // picture version of the author; absent: profile not loaded
  status?: string // presence of the author (online | away | dnd | offline | ooo)
  root_id?: string
  message: string
  create_at: number
  edit_at?: number
  reply_count?: number
  system?: boolean
  bot?: boolean
  pending?: boolean
  failed?: boolean
  pending_post_id?: string
  attachments?: Attachment[]
  files?: FileView[]
  reactions?: ReactionView[]
  saved?: boolean
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
  crt: boolean
  muted: boolean
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
