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
  name: string
  size: number
  mime: string
}

export interface ReactionView {
  emoji: string
  count: number
  mine: boolean
}

export interface PostView {
  id: string
  user_id: string
  author: string
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

export type EventType =
  | 'servers_changed'
  | 'login_failed'
  | 'open_external'
  | 'sidebar_changed'
  | 'channel_changed'
  | 'open_channel'

export interface ApiEvent {
  type: EventType
  payload?: Record<string, unknown>
}
