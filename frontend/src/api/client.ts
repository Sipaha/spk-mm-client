import { Call, Events } from '@wailsio/runtime'
import type { ApiEvent, AppInfo, ChannelDTO, EmojiDTO, EventType, SavedFile, ServerDTO, SidebarDTO } from './types'

export class ApiError extends Error {
  constructor(public code: string, public detail: string) {
    super(detail ? `${code}: ${detail}` : code)
  }
}

export interface Client {
  listServers(): Promise<ServerDTO[]>
  addServer(url: string): Promise<ServerDTO>
  removeServer(id: number): Promise<void>
  startGitLabLogin(id: number): Promise<void>
  loginWithPassword(id: number, login: string, password: string): Promise<ServerDTO>
  logout(id: number): Promise<void>
  appInfo(): Promise<AppInfo>
  selectServer(id: number): Promise<void>
  setFocused(focused: boolean): Promise<void>
  networkChanged(): Promise<void>
  openURL(url: string): Promise<void>
  sidebar(id: number, teamId: string): Promise<SidebarDTO>
  openChannel(id: number, channelId: string): Promise<ChannelDTO>
  getChannel(id: number, channelId: string): Promise<ChannelDTO>
  loadOlder(id: number, channelId: string): Promise<void>
  sendPost(id: number, channelId: string, message: string): Promise<void>
  retryPost(id: number, channelId: string, pendingId: string): Promise<void>
  discardPost(id: number, channelId: string, pendingId: string): Promise<void>
  editPost(id: number, postId: string, message: string): Promise<void>
  deletePost(id: number, postId: string): Promise<void>
  markUnread(id: number, postId: string): Promise<void>
  saveDraft(id: number, channelId: string, text: string): Promise<void>
  downloadFile(id: number, fileId: string): Promise<SavedFile>
  openFile(id: number, fileId: string): Promise<SavedFile>
  addReaction(id: number, postId: string, emoji: string): Promise<void>
  removeReaction(id: number, postId: string, emoji: string): Promise<void>
  emojiInfo(id: number): Promise<EmojiDTO>
  /** Base of audio/video URLs: `${base}/${serverId}/stream/${fileId}` ("/media" in the browser, a loopback URL in desktop). */
  mediaStreamBase(): Promise<string>
  subscribeEvents(onEvent: (e: ApiEvent) => void): () => void
}

const tokenMeta = () =>
  document.querySelector('meta[name="spk-mm-client-api-token"]')?.getAttribute('content') ?? ''

async function post<T>(method: string, body: unknown): Promise<T> {
  const headers: Record<string, string> = { 'content-type': 'application/json' }
  const token = tokenMeta()
  if (token) headers.Authorization = `Bearer ${token}`
  const r = await fetch(`/api/${method}`, { method: 'POST', headers, body: JSON.stringify(body ?? {}) })
  const isJSON = r.headers.get('content-type')?.includes('application/json')
  if (!r.ok) {
    if (isJSON) {
      const e = (await r.json()) as { code?: string; detail?: string }
      throw new ApiError(e.code ?? 'internal', e.detail ?? '')
    }
    throw new ApiError('internal', `HTTP ${r.status}`)
  }
  return (isJSON ? await r.json() : undefined) as T
}

// HTTP bodies return {} for void methods; the Client contract is void.
const done = async (p: Promise<unknown>) => {
  await p
}

export const httpClient: Client = {
  listServers: () => post('ListServers', {}),
  addServer: (url) => post('AddServer', { url }),
  removeServer: (id) => done(post('RemoveServer', { id })),
  startGitLabLogin: (id) => done(post('StartGitLabLogin', { id })),
  loginWithPassword: (id, login, password) => post('LoginWithPassword', { id, login, password }),
  logout: (id) => done(post('Logout', { id })),
  appInfo: () => post('AppInfo', {}),
  selectServer: (id) => done(post('SelectServer', { id })),
  setFocused: (focused) => done(post('SetFocused', { focused })),
  networkChanged: () => done(post('NetworkChanged', {})),
  openURL: (url) => done(post('OpenURL', { url })),
  sidebar: (id, team_id) => post('Sidebar', { id, team_id }),
  openChannel: (id, channel_id) => post('OpenChannel', { id, channel_id }),
  getChannel: (id, channel_id) => post('GetChannel', { id, channel_id }),
  loadOlder: (id, channel_id) => done(post('LoadOlder', { id, channel_id })),
  sendPost: (id, channel_id, message) => done(post('SendPost', { id, channel_id, message })),
  retryPost: (id, channel_id, pending_id) => done(post('RetryPost', { id, channel_id, pending_id })),
  discardPost: (id, channel_id, pending_id) => done(post('DiscardPost', { id, channel_id, pending_id })),
  editPost: (id, post_id, message) => done(post('EditPost', { id, post_id, message })),
  deletePost: (id, post_id) => done(post('DeletePost', { id, post_id })),
  markUnread: (id, post_id) => done(post('MarkUnread', { id, post_id })),
  saveDraft: (id, channel_id, text) => done(post('SaveDraft', { id, channel_id, text })),
  downloadFile: (id, file_id) => post('DownloadFile', { id, file_id }),
  openFile: (id, file_id) => post('OpenFile', { id, file_id }),
  addReaction: (id, post_id, emoji) => done(post('AddReaction', { id, post_id, emoji })),
  removeReaction: (id, post_id, emoji) => done(post('RemoveReaction', { id, post_id, emoji })),
  emojiInfo: (id) => post('EmojiInfo', { id }),
  mediaStreamBase: () => post('MediaStreamBase', {}),
  subscribeEvents(onEvent) {
    const es = new EventSource(`/api/events?token=${encodeURIComponent(tokenMeta())}`)
    es.onmessage = (m) => onEvent(JSON.parse(m.data) as ApiEvent)
    return () => es.close()
  },
}

// Go returns CodedError whose text is "<code>: <detail>" (or just "<code>").
export function parseWailsError(err: unknown): ApiError {
  const msg = err instanceof Error ? err.message : String((err as { message?: string })?.message ?? err)
  const i = msg.indexOf(': ')
  return i < 0 ? new ApiError(msg, '') : new ApiError(msg.slice(0, i), msg.slice(i + 2))
}

const FQN = 'github.com/spk/spk-mm-client/internal/api/transport.API.'

async function wcall<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    return (await Call.ByName(FQN + method, ...args)) as T
  } catch (e) {
    throw parseWailsError(e)
  }
}

const EVENT_TYPES: EventType[] = [
  'servers_changed',
  'login_failed',
  'open_external',
  'sidebar_changed',
  'channel_changed',
  'open_channel',
]

export const wailsClient: Client = {
  listServers: () => wcall('ListServers'),
  addServer: (url) => wcall('AddServer', url),
  removeServer: (id) => wcall('RemoveServer', id),
  startGitLabLogin: (id) => wcall('StartGitLabLogin', id),
  loginWithPassword: (id, login, password) => wcall('LoginWithPassword', id, login, password),
  logout: (id) => wcall('Logout', id),
  appInfo: () => wcall('AppInfo'),
  selectServer: (id) => wcall('SelectServer', id),
  setFocused: (focused) => wcall('SetFocused', focused),
  networkChanged: () => wcall('NetworkChanged'),
  openURL: (url) => wcall('OpenURL', url),
  sidebar: (id, teamId) => wcall('Sidebar', id, teamId),
  openChannel: (id, channelId) => wcall('OpenChannel', id, channelId),
  getChannel: (id, channelId) => wcall('GetChannel', id, channelId),
  loadOlder: (id, channelId) => wcall('LoadOlder', id, channelId),
  sendPost: (id, channelId, message) => wcall('SendPost', id, channelId, message),
  retryPost: (id, channelId, pendingId) => wcall('RetryPost', id, channelId, pendingId),
  discardPost: (id, channelId, pendingId) => wcall('DiscardPost', id, channelId, pendingId),
  editPost: (id, postId, message) => wcall('EditPost', id, postId, message),
  deletePost: (id, postId) => wcall('DeletePost', id, postId),
  markUnread: (id, postId) => wcall('MarkUnread', id, postId),
  saveDraft: (id, channelId, text) => wcall('SaveDraft', id, channelId, text),
  downloadFile: (id, fileId) => wcall('DownloadFile', id, fileId),
  openFile: (id, fileId) => wcall('OpenFile', id, fileId),
  addReaction: (id, postId, emoji) => wcall('AddReaction', id, postId, emoji),
  removeReaction: (id, postId, emoji) => wcall('RemoveReaction', id, postId, emoji),
  emojiInfo: (id) => wcall('EmojiInfo', id),
  mediaStreamBase: () => wcall('MediaStreamBase'),
  subscribeEvents(onEvent) {
    const offs = EVENT_TYPES.map((type) =>
      Events.On(type, (ev: { data: unknown }) => {
        // Emit(name, payload) arrives as data=payload; tolerate a 1-element array.
        const d = Array.isArray(ev.data) && ev.data.length === 1 ? ev.data[0] : ev.data
        onEvent({ type, payload: (d ?? undefined) as Record<string, unknown> | undefined })
      }),
    )
    return () => offs.forEach((off) => off())
  },
}

// Wails v3 beta.25 serves the UI from wails://localhost on Linux and macOS
// (internal/assetserver/assetserver_{linux,darwin}.go) and from
// http://wails.localhost on Windows (assetserver_windows.go), optionally with a port.
export const isDesktopLocation = (loc: Pick<Location, 'protocol' | 'hostname'>) =>
  loc.protocol === 'wails:' || loc.hostname === 'wails.localhost'
export const isDesktop = () => isDesktopLocation(window.location)
export const client: Client = isDesktop() ? wailsClient : httpClient
