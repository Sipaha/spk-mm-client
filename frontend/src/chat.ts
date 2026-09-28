import { ApiError, client, uploadAttachmentBrowser } from './api/client'
import type { AttachmentView, ChannelDTO, DownloadView, PostView } from './api/types'
import { errorMessage } from './errors'
import { t } from './i18n'
import { useStore } from './store'

// Responses can land out of order (a click while a refresh is in flight):
// each request takes a sequence number and only the latest one lands.
let sidebarSeq = 0
let channelSeq = 0
let attachmentsSeq = 0
let wanted: { serverId: number; channelId: string } | null = null
let inFlight = false
let again = false

export const report = (e: unknown) => useStore.getState().setError(errorMessage(e))

export function resetChat() {
  sidebarSeq++
  channelSeq++
  attachmentsSeq++
  wanted = null
  inFlight = false
  again = false
  downloadsSeq++
}

export function selectServer(id: number | null) {
  resetChat()
  const s = useStore.getState()
  s.select(id)
  client.selectServer(id ?? 0).catch(report)
  const srv = s.servers.find((x) => x.id === id)
  if (srv?.signed_in) void loadSidebar(srv.id)
}

export async function refreshServers() {
  try {
    const list = await client.listServers()
    const before = useStore.getState().selectedId
    useStore.getState().setServers(list)
    const s = useStore.getState()
    if (s.selectedId !== before) {
      selectServer(s.selectedId)
      return
    }
    const sel = s.servers.find((x) => x.id === s.selectedId)
    if (!sel) return
    if (!sel.signed_in && (s.sidebar || s.channel)) {
      resetChat() // signed out: drop the views, a new sign-in starts fresh
      s.select(sel.id)
      return
    }
    if (sel.signed_in && !s.sidebar) void loadSidebar(sel.id)
  } catch (e) {
    report(e)
  }
}

export async function loadSidebar(serverId: number, teamId = '', openSelected = false) {
  const my = ++sidebarSeq
  try {
    const sb = await client.sidebar(serverId, teamId)
    if (my !== sidebarSeq) return
    useStore.getState().setSidebar(serverId, sb)
    const nothingOpen = !wanted || wanted.serverId !== serverId
    if ((openSelected || nothingOpen) && sb.selected_channel_id) await openChannel(serverId, sb.selected_channel_id)
  } catch (e) {
    if (my === sidebarSeq) report(e)
  }
}

export async function openChannel(serverId: number, channelId: string) {
  wanted = { serverId, channelId }
  useStore.getState().setEditing(null)
  await fetchChannel(serverId, channelId, true)
}

// refreshChannel re-reads the open channel after a channel_changed event.
export async function refreshChannel(serverId: number, channelId: string) {
  if (wanted?.serverId !== serverId || wanted.channelId !== channelId) return
  if (inFlight) {
    again = true // fetched right after the request in flight
    return
  }
  await fetchChannel(serverId, channelId, false)
}

async function fetchChannel(serverId: number, channelId: string, open: boolean) {
  const my = ++channelSeq
  inFlight = true
  again = false
  try {
    const ch = open ? await client.openChannel(serverId, channelId) : await client.getChannel(serverId, channelId)
    if (my !== channelSeq) return
    const s = useStore.getState()
    s.setChannel(serverId, ch)
    // Opened a channel of another team (e.g. from a notification): follow it.
    if (open && ch.team_id && s.sidebar && s.sidebar.team_id !== ch.team_id) void loadSidebar(serverId, ch.team_id)
    // The composer's tray belongs to the channel (a draft Go keeps per
    // channel — see AGENTS.md "Вложения"); fire-and-forget so a slow
    // attachments fetch never delays showing the channel itself.
    void refreshAttachments(serverId, channelId, '')
  } catch (e) {
    if (my === channelSeq) report(e)
  } finally {
    if (my === channelSeq) {
      inFlight = false
      if (again) {
        again = false
        void refreshChannel(serverId, channelId)
      }
    }
  }
}

export function openFromNotification(serverId: number, channelId: string) {
  if (useStore.getState().selectedId !== serverId) selectServer(serverId)
  void openChannel(serverId, channelId)
}

// --- Attachments (the composer's tray) ----------------------------------

// refreshAttachments re-reads a (channel, root) composer's tray — on open,
// and as a fallback for onAttachmentsChanged (e.g. the very first load,
// before any attachments_changed event exists to react to). A failure is
// silent, like a lost draft: the tray simply stays empty until the next
// event or open. rootId: '' the channel's own composer (the only one this
// task wires into the UI — the thread panel is Task 5/6).
export async function refreshAttachments(serverId: number, channelId: string, rootId: string) {
  const my = ++attachmentsSeq
  try {
    const list = await client.attachments(serverId, channelId, rootId)
    if (my !== attachmentsSeq) return
    const s = useStore.getState()
    if (rootId === '' && s.selectedId === serverId && s.channel?.id === channelId) s.setAttachments(list)
  } catch {
    /* best-effort */
  }
}

// onAttachmentsChanged applies an EventAttachmentsChanged payload (the
// whole list, coalesced ~4/s during an upload) — only for the channel's own
// composer (root_id '') on screen; a thread's (root_id set) has no panel
// yet to show it in (Task 5/6) and is ignored here. Another channel's tray
// is re-read fresh when it is opened.
export function onAttachmentsChanged(payload: Record<string, unknown> | undefined) {
  const p = payload ?? {}
  if ((p.root_id ?? '') !== '') return
  const s = useStore.getState()
  if (Number(p.server_id) === s.selectedId && p.channel_id === s.channel?.id) {
    // Bump the sequence: a refreshAttachments request made before this event
    // can still reply after it (it's a separate round trip); without this,
    // that older, now-stale reply would win the race and overwrite the list
    // this event just applied.
    attachmentsSeq++
    s.setAttachments((p.items as AttachmentView[] | undefined) ?? [])
  }
}

// onAttachmentRefused applies an EventAttachmentRefused payload: a drop had
// no caller to report its refusal to (too_many, not_dropped, …) — shown the
// same way a send error is, in the composer. Only the channel's own
// composer (root_id '') has anywhere to show it yet (Task 5/6).
export function onAttachmentRefused(payload: Record<string, unknown> | undefined) {
  const p = payload ?? {}
  if ((p.root_id ?? '') !== '') return
  const s = useStore.getState()
  if (Number(p.server_id) === s.selectedId && p.channel_id === s.channel?.id) {
    s.setAttachError(errorMessage(new ApiError(String(p.code ?? 'internal'), '')))
  }
}

// removeAttachment/retryAttachment: one-off actions on an existing chip,
// reported like other such actions (deletePost, markUnread) — a global
// error, not the composer's inline one (nothing the user just typed is at
// risk here). pickAttachments/attachFromClipboard are left uncaught: the
// Composer shows their failure (a limit, "unsupported" in browser mode, a
// quiet no_paste_gesture) inline, the same way a send error is shown.
// rootId: '' the channel's own composer (Composer.tsx only ever passes '' —
// the thread panel is Task 5/6).
export const removeAttachment = (serverId: number, attachmentId: string) =>
  client.removeAttachment(serverId, attachmentId).catch(report)
export const retryAttachment = (serverId: number, attachmentId: string) =>
  client.retryAttachment(serverId, attachmentId).catch(report)
export const pickAttachments = (serverId: number, channelId: string, rootId: string) => client.pickAttachments(serverId, channelId, rootId)
export const attachFromClipboard = (serverId: number, channelId: string, rootId: string) => client.attachFromClipboard(serverId, channelId, rootId)

// uploadAttachments (browser mode only): every file is tried, even if one
// fails — a bad file among several must not block the rest (like Go's
// addPaths) — and the first failure, if any, is thrown so the caller shows
// it like a send error. rootId: '' the channel's own composer.
export async function uploadAttachments(serverId: number, channelId: string, files: File[], rootId: string): Promise<void> {
  const results = await Promise.allSettled(files.map((f) => uploadAttachmentBrowser(serverId, channelId, f, rootId)))
  const failed = results.find((r): r is PromiseRejectedResult => r.status === 'rejected')
  if (failed) throw failed.reason
}

export const openLink = (href: string) => {
  client.openURL(href).catch(report)
}

// loadOlder fetches one page of history above the window; resolves true
// when the channel was re-read with it.
export async function loadOlder(serverId: number, channelId: string): Promise<boolean> {
  try {
    await client.loadOlder(serverId, channelId)
    await refreshChannel(serverId, channelId)
    return true
  } catch (e) {
    report(e)
    return false
  }
}

export const retryPost = (serverId: number, channelId: string, pendingId: string) => {
  client.retryPost(serverId, channelId, pendingId).catch(report)
}

export const discardPost = (serverId: number, channelId: string, pendingId: string) => {
  client.discardPost(serverId, channelId, pendingId).catch(report)
}

export const sendPost = (serverId: number, channelId: string, message: string, attachmentIds: string[] = []) =>
  client.sendPost(serverId, channelId, message, attachmentIds)

export const saveDraft = (serverId: number, channelId: string, text: string) => {
  client.saveDraft(serverId, channelId, text).catch(() => {}) // a lost draft is not worth an error banner
}

export async function editPost(serverId: number, postId: string, message: string) {
  await client.editPost(serverId, postId, message)
  // Only close the box this save opened: the user may have already moved on
  // to editing a different post while this request was in flight.
  if (useStore.getState().editingId === postId) useStore.getState().setEditing(null)
}

export const deletePost = (serverId: number, postId: string) => {
  client.deletePost(serverId, postId).catch(report)
}

export const markUnread = (serverId: number, postId: string) => {
  client.markUnread(serverId, postId).catch(report)
}

// setPostSaved: the toolbar's bookmark button. State only ever comes from
// post.saved (the server's flagged_post preference, applied to the feed by
// preferences_changed/preferences_deleted like a reaction) — no optimistic
// local flip to roll back, so a failure (offline, session_expired) just
// reports like any other action and the button's next render reflects
// whatever post.saved still says.
export const setPostSaved = (serverId: number, post: PostView, saved: boolean) => {
  client.setPostSaved(serverId, post.id, saved).catch(report)
}

// Same permalink form as Mattermost: <server>/<team>/pl/<post id>.
export const copyLink = (serverURL: string, teamName: string, postId: string) => {
  navigator.clipboard?.writeText(`${serverURL}/${teamName}/pl/${postId}`).catch(report)
}

// revealSavedFile backs the saved notice's "Show in folder" button: it
// resolves the download's list entry by its (unique) saved path and asks
// Go to show it — RevealDownload takes a downloads-list id, which
// DownloadFile/OpenFile's result does not carry.
async function revealSavedFile(path: string) {
  try {
    const list = await client.downloads()
    const entry = list.find((d) => d.path === path)
    // No matching list entry (e.g. the download's SQLite insert failed
    // even though the file itself was saved) — report it instead of
    // silently doing nothing; there is no id to reveal by otherwise.
    if (!entry) throw new Error(`no downloads-list entry for ${path}`)
    await client.revealDownload(entry.id)
  } catch (e) {
    report(e)
  }
}

// saving shows "Downloading <name>…" at once — a big file takes a while —
// as a sticky notice (no auto-dismiss); the outcome replaces it. Go joins
// clicks on a file already downloading to that download. A notice with a
// path (saved, whether or not it was opened) gets a "Show in folder" action.
async function saving(name: string, save: () => Promise<{ path: string; opened: boolean }>, done: (r: { path: string; opened: boolean }) => string | null) {
  const s = useStore.getState()
  const busy = t('file.downloading', { name })
  s.setNotice(busy, true)
  try {
    const r = await save()
    const msg = done(r)
    if (msg !== null || useStore.getState().notice === busy) {
      const action = msg !== null ? { label: t('downloads.showInFolder'), onClick: () => void revealSavedFile(r.path) } : null
      useStore.getState().setNotice(msg, false, action)
    }
  } catch (e) {
    if (useStore.getState().notice === busy) useStore.getState().setNotice(null)
    report(e)
  }
}

export const downloadFile = (serverId: number, file: { id: string; name: string }) =>
  saving(file.name, () => client.downloadFile(serverId, file.id), (r) => t('file.saved', { path: r.path }))

// openFile saves and opens with the system app; Go refuses to open
// launchers (.desktop, scripts…) — then the user is told where the file is.
export const openFile = (serverId: number, file: { id: string; name: string }) =>
  saving(file.name, () => client.openFile(serverId, file.id), (r) => (r.opened ? null : t('file.savedNotOpened', { path: r.path })))

// --- Downloads panel ---------------------------------------------------

// downloadsSeq: two overlapping reloads (e.g. a fast progress-driven refresh
// racing a slower one from the panel's own open) can resolve out of order;
// only the latest request's result is applied — same pattern as
// sidebarSeq/channelSeq above.
let downloadsSeq = 0

export async function refreshDownloads() {
  const my = ++downloadsSeq
  try {
    const list = await client.downloads()
    if (my !== downloadsSeq) return
    useStore.getState().setDownloads(list)
  } catch (e) {
    if (my === downloadsSeq) report(e)
  }
}

// openDownloadsPanel always re-reads Downloads(): the store only *holds*
// the list while the panel is open or a download is active
// (store.ts's setDownloads/setDownloadsOpen drop it back to [] otherwise,
// so the list never grows across a long session) — so on every open the
// in-memory copy may already be stale or empty, even if a downloads_changed
// event refreshed it (and immediately dropped the result again) while the
// panel was closed. A "fetch only the first time" gate looked like a
// reasonable dedupe but was wrong for exactly that reason: it could latch
// on a closed-panel refresh and leave a later open stuck showing nothing
// forever (Task 6 e2e finding — "download, then open the panel" with
// nothing else happening in between never fetched again).
export function openDownloadsPanel() {
  useStore.getState().setDownloadsOpen(true)
  void refreshDownloads()
}

export const closeDownloadsPanel = () => useStore.getState().setDownloadsOpen(false)

// onDownloadsChanged applies an EventDownloadsChanged payload: "received"
// alone patches that row in place (not final — throttled, at most ~4/s);
// anything else (a new download listed, "state" on completion, or no
// payload at all — a remove/clear) re-reads the whole list.
export function onDownloadsChanged(payload: Record<string, unknown> | undefined) {
  const p = payload ?? {}
  if ('received' in p && !('state' in p) && typeof p.id === 'number') {
    useStore.getState().patchDownloadProgress(p.id, Number(p.received))
    return
  }
  void refreshDownloads()
}

export const openDownload = (id: number) => client.openDownload(id).catch(report)
export const revealDownload = (id: number) => client.revealDownload(id).catch(report)
export const removeDownload = (id: number) => client.removeDownload(id).catch(report)
export const clearDownloads = () => client.clearDownloads().catch(report)

// downloadPrimaryAction: what a click on a finished row / Enter does —
// "Open" when possible, else "Show in folder"; nothing for an in-progress
// or deleted entry (Go's RevealDownload/OpenDownload would just 404).
export function downloadPrimaryAction(d: DownloadView): (() => void) | null {
  if (d.state !== 'done' || !d.exists) return null
  if (d.openable) return () => openDownload(d.id)
  return () => revealDownload(d.id)
}

// react: the promise always resolves (never rejects) once the call has
// settled either way, reporting a failure the same as before — PostItem's
// quick-reactions cache invalidation (emoji/recent.ts) waits for it to
// settle before marking the cache stale, so the re-fetch it triggers
// cannot outrace Go's own handling of this very call (final-review
// finding, UI pass 2026-09-28: invalidating synchronously could refetch
// before the backend had bumped its recent list at all).
export function react(serverId: number, postId: string, emoji: string, add: boolean): Promise<void> {
  return (add ? client.addReaction(serverId, postId, emoji) : client.removeReaction(serverId, postId, emoji)).then(
    () => {},
    (e) => report(e),
  )
}

export const emojiInfo = (serverId: number) => client.emojiInfo(serverId)

export function editLastOwn(ch: ChannelDTO) {
  for (let i = ch.posts.length - 1; i >= 0; i--) {
    const p = ch.posts[i]
    if (p.user_id === ch.me_id && !p.pending && !p.failed && !p.system) {
      useStore.getState().setEditing(p.id)
      return
    }
  }
}
