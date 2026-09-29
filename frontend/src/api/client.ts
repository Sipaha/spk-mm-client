import { Call, Events } from '@wailsio/runtime'
import type {
  ApiEvent,
  AutocompleteDTO,
  AutocompleteKind,
  AppInfo,
  AttachmentView,
  ChannelDTO,
  DownloadView,
  EmojiDTO,
  EventType,
  LayoutDTO,
  ReactionUsersDTO,
  SavedFile,
  ServerDTO,
  SidebarDTO,
  ThreadDTO,
} from './types'

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
  /** Saved splitter widths (0: never saved -- apply the built-in default). */
  getLayout(): Promise<LayoutDTO>
  /** Persists the sidebar splitter width; call once per commit (pointerup/keyboard step), not per drag frame. */
  setSidebarWidth(width: number): Promise<void>
  /** setSidebarWidth's counterpart for the thread panel's splitter. */
  setThreadWidth(width: number): Promise<void>
  /** The composer's Aa toggle (false: never saved -- shown by default). App-wide. */
  getFormattingBarHidden(): Promise<boolean>
  /** Persists the composer's Aa toggle. */
  setFormattingBarHidden(hidden: boolean): Promise<void>
  selectServer(id: number): Promise<void>
  setFocused(focused: boolean): Promise<void>
  networkChanged(): Promise<void>
  openURL(url: string): Promise<void>
  sidebar(id: number, teamId: string): Promise<SidebarDTO>
  openChannel(id: number, channelId: string): Promise<ChannelDTO>
  getChannel(id: number, channelId: string): Promise<ChannelDTO>
  loadOlder(id: number, channelId: string): Promise<void>
  /** Opens a thread in the panel (replacing the open one); may still be loading — thread_changed follows. Retry = open again. */
  openThread(id: number, channelId: string, rootId: string): Promise<ThreadDTO>
  /** A cached thread's current view (no_post: not cached). */
  getThread(id: number, rootId: string): Promise<ThreadDTO>
  /** Closes the panel; the thread stays cached, trimmed. */
  closeThread(id: number): Promise<void>
  /** Loads older replies, up to the cap of 200 (ThreadDTO.capped). */
  loadOlderReplies(id: number, rootId: string): Promise<void>
  // attachmentIds: the channel's composer attachments sent with the message
  // (none: text only; the text may be empty when there are some).
  sendPost(id: number, channelId: string, message: string, attachmentIds?: string[]): Promise<void>
  retryPost(id: number, channelId: string, pendingId: string): Promise<void>
  discardPost(id: number, channelId: string, pendingId: string): Promise<void>
  editPost(id: number, postId: string, message: string): Promise<void>
  deletePost(id: number, postId: string): Promise<void>
  markUnread(id: number, postId: string): Promise<void>
  setPostSaved(id: number, postId: string, saved: boolean): Promise<void>
  saveDraft(id: number, channelId: string, text: string): Promise<void>
  /** SendPost for a reply: rootId must be a thread the panel has open or recently had open (state.ThreadHeld), else no_post. */
  sendReply(id: number, channelId: string, rootId: string, message: string, attachmentIds?: string[]): Promise<void>
  /** saveDraft for a reply, keyed by rootId alone (state.ThreadDraftCap-bounded); rootId must be held, else no_post. */
  saveThreadDraft(id: number, rootId: string, text: string): Promise<void>
  downloadFile(id: number, fileId: string): Promise<SavedFile>
  openFile(id: number, fileId: string): Promise<SavedFile>
  addReaction(id: number, postId: string, emoji: string): Promise<void>
  removeReaction(id: number, postId: string, emoji: string): Promise<void>
  /** Who reacted with emoji on postId: everyone except me (the UI adds "You"), oldest first. */
  reactionUsers(id: number, postId: string, emoji: string): Promise<ReactionUsersDTO>
  emojiInfo(id: number): Promise<EmojiDTO>
  /** Base of audio/video URLs: `${base}/${serverId}/stream/${fileId}` ("/media" in the browser, a loopback URL in desktop). */
  mediaStreamBase(): Promise<string>
  /** The browser-like downloads list, newest first. */
  downloads(): Promise<DownloadView[]>
  /** Opens a listed file (OpenFile's allowlist); false: only "show in folder" applies. */
  openDownload(id: number): Promise<boolean>
  /** Shows a listed file in the file manager (or its folder as a fallback). */
  revealDownload(id: number): Promise<void>
  /** Drops one finished/failed entry (the file stays); a download in progress is kept. */
  removeDownload(id: number): Promise<void>
  /** Drops every finished/failed entry. */
  clearDownloads(): Promise<void>
  // Attachments: files attached to a message's composer (a draft, in Go's
  // memory — the UI never sends paths; see AGENTS.md "Вложения — модель
  // угроз"). rootId: '' the channel's own composer, else a thread's reply
  // composer (state.ThreadHeld). Not available in the transport itself:
  // the desktop clipboard/dialog sources come from
  // AttachFromClipboard/PickAttachments (unsupported in browser mode — Go
  // answers "unsupported" there); a browser upload instead goes through
  // uploadAttachmentBrowser below.
  attachments(id: number, channelId: string, rootId: string): Promise<AttachmentView[]>
  removeAttachment(id: number, attachmentId: string): Promise<void>
  retryAttachment(id: number, attachmentId: string): Promise<void>
  /** Desktop only: reads the system clipboard (a native paste just happened); returns how many were attached. */
  attachFromClipboard(id: number, channelId: string, rootId: string): Promise<number>
  /** Desktop only: opens the native file dialog; returns how many were attached (none: cancelled). */
  pickAttachments(id: number, channelId: string, rootId: string): Promise<number>
  /**
   * The composer's popup: suggestions of a kind for the word typed after its
   * trigger. Aborting `signal` cancels the call in Go too (a stale request).
   */
  autocomplete(id: number, kind: AutocompleteKind, channelId: string, rootId: string, prefix: string, signal?: AbortSignal): Promise<AutocompleteDTO>
  /** Runs a slash command the user sent (rootId: a held thread's composer); command_not_found for an unknown trigger. */
  executeCommand(id: number, channelId: string, rootId: string, command: string): Promise<void>
  subscribeEvents(onEvent: (e: ApiEvent) => void): () => void
}

const tokenMeta = () =>
  document.querySelector('meta[name="spk-mm-client-api-token"]')?.getAttribute('content') ?? ''

async function post<T>(method: string, body: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { 'content-type': 'application/json' }
  const token = tokenMeta()
  if (token) headers.Authorization = `Bearer ${token}`
  const init: RequestInit = { method: 'POST', headers, body: JSON.stringify(body ?? {}) }
  if (signal) init.signal = signal
  const r = await fetch(`/api/${method}`, init)
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
  getLayout: () => post('GetLayout', {}),
  setSidebarWidth: (width) => done(post('SetSidebarWidth', { width })),
  setThreadWidth: (width) => done(post('SetThreadWidth', { width })),
  getFormattingBarHidden: () => post('GetFormattingBarHidden', {}),
  setFormattingBarHidden: (hidden) => done(post('SetFormattingBarHidden', { hidden })),
  selectServer: (id) => done(post('SelectServer', { id })),
  setFocused: (focused) => done(post('SetFocused', { focused })),
  networkChanged: () => done(post('NetworkChanged', {})),
  openURL: (url) => done(post('OpenURL', { url })),
  sidebar: (id, team_id) => post('Sidebar', { id, team_id }),
  openChannel: (id, channel_id) => post('OpenChannel', { id, channel_id }),
  getChannel: (id, channel_id) => post('GetChannel', { id, channel_id }),
  loadOlder: (id, channel_id) => done(post('LoadOlder', { id, channel_id })),
  openThread: (id, channel_id, root_id) => post('OpenThread', { id, channel_id, root_id }),
  getThread: (id, root_id) => post('GetThread', { id, root_id }),
  closeThread: (id) => done(post('CloseThread', { id })),
  loadOlderReplies: (id, root_id) => done(post('LoadOlderReplies', { id, root_id })),
  sendPost: (id, channel_id, message, attachmentIds = []) =>
    done(post('SendPost', { id, channel_id, message, attachment_ids: attachmentIds })),
  retryPost: (id, channel_id, pending_id) => done(post('RetryPost', { id, channel_id, pending_id })),
  discardPost: (id, channel_id, pending_id) => done(post('DiscardPost', { id, channel_id, pending_id })),
  editPost: (id, post_id, message) => done(post('EditPost', { id, post_id, message })),
  deletePost: (id, post_id) => done(post('DeletePost', { id, post_id })),
  markUnread: (id, post_id) => done(post('MarkUnread', { id, post_id })),
  setPostSaved: (id, post_id, saved) => done(post('SetPostSaved', { id, post_id, saved })),
  saveDraft: (id, channel_id, text) => done(post('SaveDraft', { id, channel_id, text })),
  sendReply: (id, channel_id, root_id, message, attachmentIds = []) =>
    done(post('SendReply', { id, channel_id, root_id, message, attachment_ids: attachmentIds })),
  saveThreadDraft: (id, root_id, text) => done(post('SaveThreadDraft', { id, root_id, text })),
  downloadFile: (id, file_id) => post('DownloadFile', { id, file_id }),
  openFile: (id, file_id) => post('OpenFile', { id, file_id }),
  addReaction: (id, post_id, emoji) => done(post('AddReaction', { id, post_id, emoji })),
  removeReaction: (id, post_id, emoji) => done(post('RemoveReaction', { id, post_id, emoji })),
  reactionUsers: (id, post_id, emoji) => post('ReactionUsers', { id, post_id, emoji }),
  emojiInfo: (id) => post('EmojiInfo', { id }),
  mediaStreamBase: () => post('MediaStreamBase', {}),
  downloads: () => post('Downloads', {}),
  openDownload: (id) => post('OpenDownload', { id }),
  revealDownload: (id) => done(post('RevealDownload', { id })),
  removeDownload: (id) => done(post('RemoveDownload', { id })),
  clearDownloads: () => done(post('ClearDownloads', {})),
  attachments: (id, channel_id, root_id) => post('Attachments', { id, channel_id, root_id }),
  removeAttachment: (id, attachment_id) => done(post('RemoveAttachment', { id, attachment_id })),
  retryAttachment: (id, attachment_id) => done(post('RetryAttachment', { id, attachment_id })),
  attachFromClipboard: (id, channel_id, root_id) => post('AttachFromClipboard', { id, channel_id, root_id }),
  pickAttachments: (id, channel_id, root_id) => post('PickAttachments', { id, channel_id, root_id }),
  autocomplete: (id, kind, channel_id, root_id, prefix, signal) =>
    post('Autocomplete', { id, kind, channel_id, root_id, prefix }, signal),
  executeCommand: (id, channel_id, root_id, command) => done(post('ExecuteCommand', { id, channel_id, root_id, command })),
  subscribeEvents(onEvent) {
    const es = new EventSource(`/api/events?token=${encodeURIComponent(tokenMeta())}`)
    es.onmessage = (m) => onEvent(JSON.parse(m.data) as ApiEvent)
    return () => es.close()
  },
}

// uploadAttachmentBrowser (browser mode only): the one place a File's bytes
// leave the page — a raw POST body to /api/attachments/{srv}/{channel}
// (bearer auth like every other /api/ call). Desktop never does this: Go
// reads the clipboard/dialog/drop itself and never sees a Blob/File/
// FormData body (that crashes the whole app against wails:// — AGENTS.md
// "Things that bite"). Not part of Client: desktop has no equivalent.
export async function uploadAttachmentBrowser(serverId: number, channelId: string, file: File, rootId: string): Promise<AttachmentView> {
  const headers: Record<string, string> = {}
  const token = tokenMeta()
  if (token) headers.Authorization = `Bearer ${token}`
  const qs = new URLSearchParams({ root: rootId, name: file.name, mime: file.type || 'application/octet-stream' })
  const r = await fetch(`/api/attachments/${serverId}/${encodeURIComponent(channelId)}?${qs}`, { method: 'POST', headers, body: file })
  const isJSON = r.headers.get('content-type')?.includes('application/json')
  if (!r.ok) {
    if (isJSON) {
      const e = (await r.json()) as { code?: string; detail?: string }
      throw new ApiError(e.code ?? 'internal', e.detail ?? '')
    }
    throw new ApiError('internal', `HTTP ${r.status}`)
  }
  return (await r.json()) as AttachmentView
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
  'downloads_changed',
  'attachments_changed',
  'attachment_refused',
  'thread_changed',
]

export const wailsClient: Client = {
  listServers: () => wcall('ListServers'),
  addServer: (url) => wcall('AddServer', url),
  removeServer: (id) => wcall('RemoveServer', id),
  startGitLabLogin: (id) => wcall('StartGitLabLogin', id),
  loginWithPassword: (id, login, password) => wcall('LoginWithPassword', id, login, password),
  logout: (id) => wcall('Logout', id),
  appInfo: () => wcall('AppInfo'),
  getLayout: () => wcall('GetLayout'),
  setSidebarWidth: (width) => wcall('SetSidebarWidth', width),
  setThreadWidth: (width) => wcall('SetThreadWidth', width),
  getFormattingBarHidden: () => wcall('GetFormattingBarHidden'),
  setFormattingBarHidden: (hidden) => wcall('SetFormattingBarHidden', hidden),
  selectServer: (id) => wcall('SelectServer', id),
  setFocused: (focused) => wcall('SetFocused', focused),
  networkChanged: () => wcall('NetworkChanged'),
  openURL: (url) => wcall('OpenURL', url),
  sidebar: (id, teamId) => wcall('Sidebar', id, teamId),
  openChannel: (id, channelId) => wcall('OpenChannel', id, channelId),
  getChannel: (id, channelId) => wcall('GetChannel', id, channelId),
  loadOlder: (id, channelId) => wcall('LoadOlder', id, channelId),
  openThread: (id, channelId, rootId) => wcall('OpenThread', id, channelId, rootId),
  getThread: (id, rootId) => wcall('GetThread', id, rootId),
  closeThread: (id) => wcall('CloseThread', id),
  loadOlderReplies: (id, rootId) => wcall('LoadOlderReplies', id, rootId),
  sendPost: (id, channelId, message, attachmentIds = []) => wcall('SendPost', id, channelId, message, attachmentIds),
  retryPost: (id, channelId, pendingId) => wcall('RetryPost', id, channelId, pendingId),
  discardPost: (id, channelId, pendingId) => wcall('DiscardPost', id, channelId, pendingId),
  editPost: (id, postId, message) => wcall('EditPost', id, postId, message),
  deletePost: (id, postId) => wcall('DeletePost', id, postId),
  markUnread: (id, postId) => wcall('MarkUnread', id, postId),
  setPostSaved: (id, postId, saved) => wcall('SetPostSaved', id, postId, saved),
  saveDraft: (id, channelId, text) => wcall('SaveDraft', id, channelId, text),
  sendReply: (id, channelId, rootId, message, attachmentIds = []) => wcall('SendReply', id, channelId, rootId, message, attachmentIds),
  saveThreadDraft: (id, rootId, text) => wcall('SaveThreadDraft', id, rootId, text),
  downloadFile: (id, fileId) => wcall('DownloadFile', id, fileId),
  openFile: (id, fileId) => wcall('OpenFile', id, fileId),
  addReaction: (id, postId, emoji) => wcall('AddReaction', id, postId, emoji),
  removeReaction: (id, postId, emoji) => wcall('RemoveReaction', id, postId, emoji),
  reactionUsers: (id, postId, emoji) => wcall('ReactionUsers', id, postId, emoji),
  emojiInfo: (id) => wcall('EmojiInfo', id),
  mediaStreamBase: () => wcall('MediaStreamBase'),
  downloads: () => wcall('Downloads'),
  openDownload: (id) => wcall('OpenDownload', id),
  revealDownload: (id) => wcall('RevealDownload', id),
  removeDownload: (id) => wcall('RemoveDownload', id),
  clearDownloads: () => wcall('ClearDownloads'),
  attachments: (id, channelId, rootId) => wcall('Attachments', id, channelId, rootId),
  removeAttachment: (id, attachmentId) => wcall('RemoveAttachment', id, attachmentId),
  retryAttachment: (id, attachmentId) => wcall('RetryAttachment', id, attachmentId),
  attachFromClipboard: (id, channelId, rootId) => wcall('AttachFromClipboard', id, channelId, rootId),
  pickAttachments: (id, channelId, rootId) => wcall('PickAttachments', id, channelId, rootId),
  async autocomplete(id, kind, channelId, rootId, prefix, signal) {
    // Call.ByName's promise is a CancellablePromise: cancel() cancels the
    // Go method's context (it takes a context.Context — transport/wails.go).
    const p = Call.ByName(FQN + 'Autocomplete', id, kind, channelId, rootId, prefix)
    const onAbort = () => void (p as { cancel?: () => unknown }).cancel?.()
    signal?.addEventListener('abort', onAbort, { once: true })
    try {
      return (await p) as AutocompleteDTO
    } catch (e) {
      throw parseWailsError(e)
    } finally {
      signal?.removeEventListener('abort', onAbort)
    }
  },
  executeCommand: (id, channelId, rootId, command) => wcall('ExecuteCommand', id, channelId, rootId, command),
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
