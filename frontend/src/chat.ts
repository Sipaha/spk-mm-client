import { ApiError, client, uploadAttachmentBrowser } from './api/client'
import type { AttachmentView, ChannelDTO, ChannelItem, DownloadView, PostView, SidebarDTO } from './api/types'
import { errorMessage } from './errors'
import { t } from './i18n'
import { useStore } from './store'

// findChannelItem: the sidebar's own record for channelId, if the given
// sidebar is currently loaded and has it — used by openChannel below to
// snapshot unread/mentions before the switch. Mirrors the by-slug lookup in
// Markdown.tsx's ChannelMention, but by id.
function findChannelItem(sidebar: SidebarDTO | null, channelId: string): ChannelItem | null {
  for (const c of sidebar?.categories ?? []) for (const it of c.channels ?? []) if (it.id === channelId) return it
  return null
}

// Responses can land out of order (a click while a refresh is in flight):
// each request takes a sequence number and only the latest one lands.
let sidebarSeq = 0
let channelSeq = 0
let attachmentsSeq = 0
let threadSeq = 0
let threadAttachmentsSeq = 0
let wanted: { serverId: number; channelId: string } | null = null
// threadWanted: the server/root the panel is open for — mirrors `wanted`
// above, but for the thread panel (Task 6). null: no thread open.
let threadWanted: { serverId: number; rootId: string } | null = null
let inFlight = false
let again = false
// jumpSeq: the latest jump (jumpToPost) — checked after each of its awaits,
// so a jump overtaken by another one, a channel or server switch does
// nothing more (no scroll, no thread). jumpAbort lets go of its request.
let jumpSeq = 0
let jumpAbort: AbortController | null = null

// cancelled: a history operation a newer navigation replaced (Go's
// CodeCancelled) — never shown.
const cancelled = (e: unknown) => e instanceof ApiError && e.code === 'cancelled'

export const report = (e: unknown) => useStore.getState().setError(errorMessage(e))

export function resetChat() {
  sidebarSeq++
  channelSeq++
  attachmentsSeq++
  threadSeq++
  threadAttachmentsSeq++
  wanted = null
  threadWanted = null
  inFlight = false
  again = false
  downloadsSeq++
  dropJump()
}

function dropJump() {
  jumpSeq++
  jumpAbort?.abort()
  jumpAbort = null
}

export function selectServer(id: number | null) {
  // A thread open on the server we're switching away from closes with it
  // (Task 6 brief: "the panel closes on channel or server switch") —
  // captured before resetChat() clears threadWanted.
  const closingThread = threadWanted
  resetChat()
  const s = useStore.getState()
  s.select(id)
  client.selectServer(id ?? 0).catch(report)
  if (closingThread) client.closeThread(closingThread.serverId).catch(() => {})
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
  dropJump() // the user went somewhere: a jump still on its way stops
  await enterChannel(serverId, channelId)
}

async function enterChannel(serverId: number, channelId: string) {
  // Navigating to a channel closes any thread panel open for this server
  // (Task 6 brief) — even the same channel's, re-clicked: opening always
  // starts fresh, same as the channel fetch itself below.
  closeThread(serverId)
  wanted = { serverId, channelId }
  const s = useStore.getState()
  // heldChannel (sidebar-sections-brief.md, fix round 1): snapshotted here
  // because this is the one gateway every channel switch funnels through —
  // a Sidebar row click, a notification click (openFromNotification below),
  // or a ~channel link in the feed or thread panel (Markdown.tsx's
  // ChannelMention) — read synchronously from the *current* store, before
  // fetchChannel's async mark-as-read can flip the channel's own `unread`
  // back to false. Mirrors the webapp's setLastUnreadChannel, dispatched
  // synchronously with SELECT_CHANNEL before its own async view-channel
  // call. `s.sidebar` only reflects `s.selectedId`'s server (store.ts), so
  // a cross-server open (not yet on serverId) can't find a match here and
  // correctly holds nothing.
  const item = s.selectedId === serverId ? findChannelItem(s.sidebar, channelId) : null
  s.setHeldChannel(item?.unread ? { id: item.id, hadMentions: item.mentions > 0 } : null)
  s.setEditing(null)
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

// openFromNotification opens a reply notification's channel, then (rootId
// set) its thread panel — the channel fetch is awaited first so the thread's
// own channel-membership check (state.Server.ThreadHeld's channel lookup)
// and the sidebar/team follow-along above have already happened.
export async function openFromNotification(serverId: number, channelId: string, rootId?: string) {
  if (useStore.getState().selectedId !== serverId) selectServer(serverId)
  await openChannel(serverId, channelId)
  if (rootId) void openThread(serverId, channelId, rootId)
}

// --- Jump to a post (spec «Поиск», Секция 1/1б) ---------------------------

// jumpToPost shows postId of channelId: the channel is opened (unless it is
// the one open), Go loads the history around the post, and the feed
// centers and highlights it (store focus). A reply of a collapsed thread
// (CRT: in_feed false) opens its thread focused on it instead. Each step
// goes on only while this is still the latest jump (jumpSeq). Resolves
// quietly when overtaken or cancelled; any other failure (post_gone,
// forbidden, offline…) is thrown for the caller to show where it asked.
export async function jumpToPost(serverId: number, channelId: string, postId: string, rootId?: string): Promise<void> {
  // Another server first (its reset drops jumps in flight too); its
  // sidebar then finds this channel wanted and leaves it be.
  if (useStore.getState().selectedId !== serverId) selectServer(serverId)
  dropJump()
  const my = jumpSeq
  const ctl = new AbortController()
  jumpAbort = ctl
  const live = () => my === jumpSeq
  try {
    if (wanted?.serverId !== serverId || wanted.channelId !== channelId || useStore.getState().channel?.id !== channelId) {
      await enterChannel(serverId, channelId)
      if (!live()) return
    }
    const r = await client.jumpToPost(serverId, channelId, postId, ctl.signal)
    if (!live()) return
    if (!r.in_feed) {
      await openThreadFocused(serverId, channelId, r.root_id || rootId || '', r.post_id, ctl.signal)
      return
    }
    useStore.getState().setFocus(r.post_id)
    await refreshChannel(serverId, channelId)
  } catch (e) {
    if (!live() || cancelled(e)) return
    throw e
  } finally {
    if (jumpAbort === ctl) jumpAbort = null
  }
}

// openThreadFocused opens rootId's thread in the panel around replyId (Go's
// thread focus: a segment around it when it is beyond the replies held)
// and asks the panel to center and highlight it. threadSeq drops it once
// another thread is opened or the panel closed meanwhile.
async function openThreadFocused(serverId: number, channelId: string, rootId: string, replyId: string, signal?: AbortSignal) {
  threadWanted = { serverId, rootId }
  const my = ++threadSeq
  const th = await client.openThreadAt(serverId, channelId, rootId, replyId, signal)
  if (my !== threadSeq) return
  const s = useStore.getState()
  if (s.selectedId !== serverId) return
  s.setThread(th)
  s.setThreadFocus(replyId)
  void refreshThreadAttachments(serverId, channelId, rootId)
}

// loadNewer loads one page of the gap between the held history and the
// window, then re-reads the channel. A cancelled one is quiet; a failure
// (no_progress, offline…) is thrown: the gap row shows it with "Retry".
export async function loadNewer(serverId: number, channelId: string): Promise<void> {
  try {
    await client.loadNewer(serverId, channelId)
  } catch (e) {
    if (cancelled(e)) return
    throw e
  }
  await refreshChannel(serverId, channelId)
}

// retryRevalidation: "Retry" of a stale history's reread (Go retries on
// its own a few times first).
export async function retryRevalidation(serverId: number, channelId: string): Promise<void> {
  try {
    await client.retryRevalidation(serverId, channelId)
    await refreshChannel(serverId, channelId)
  } catch (e) {
    if (!cancelled(e)) report(e)
  }
}

// loadThreadFocus loads one page of the thread focus: older (the panel's
// onLoadOlder — resolves true when the thread was re-read, a failure is
// reported) or newer (the gap to the latest replies — a failure is thrown
// for the gap row). A cancelled one resolves false, quietly.
export async function loadThreadFocus(serverId: number, rootId: string, newer: boolean): Promise<boolean> {
  try {
    await client.loadThreadFocus(serverId, rootId, newer)
  } catch (e) {
    if (cancelled(e)) return false
    if (newer) throw e
    report(e)
    return false
  }
  await refreshThread(serverId, rootId)
  return true
}

export async function retryThreadRevalidation(serverId: number, rootId: string): Promise<void> {
  try {
    await client.retryThreadRevalidation(serverId, rootId)
    await refreshThread(serverId, rootId)
  } catch (e) {
    if (!cancelled(e)) report(e)
  }
}

// --- Thread panel (Task 6) -----------------------------------------------

// openThread opens rootId's thread in serverId's panel (replacing whatever
// was open there — Go's OpenThread does the LRU swap). A stale reply (the
// user already opened a different thread, closed the panel, or navigated
// away) is dropped by threadSeq — chat.ts's own guard, same pattern as
// channelSeq/attachmentsSeq above, on top of Go's own cache eviction.
export async function openThread(serverId: number, channelId: string, rootId: string) {
  dropJump() // the user opened a thread: a jump on its way must not replace it
  threadWanted = { serverId, rootId }
  const my = ++threadSeq
  try {
    const th = await client.openThread(serverId, channelId, rootId)
    if (my !== threadSeq) return
    const s = useStore.getState()
    if (s.selectedId === serverId) s.setThread(th)
    void refreshThreadAttachments(serverId, channelId, rootId)
  } catch (e) {
    if (my === threadSeq) report(e)
  }
}

// closeThread closes serverId's panel (Go keeps the thread cached, trimmed
// to its last page) — the "×", Esc, "back to channel", and any channel/
// server switch that must take the panel down with it (openChannel/
// selectServer above). A no-op if nothing is open for that server.
export function closeThread(serverId: number) {
  if (threadWanted?.serverId !== serverId) return
  threadWanted = null
  threadSeq++
  threadAttachmentsSeq++
  client.closeThread(serverId).catch(() => {})
  const s = useStore.getState()
  if (s.selectedId === serverId) {
    s.setThread(null)
    s.setThreadAttachments([])
    s.setThreadAttachError(null)
  }
}

// refreshThread re-reads serverId's open thread (a thread_changed event, or
// after loadOlderReplies) — ignored once a different thread (or none) is
// open by the time it resolves.
export async function refreshThread(serverId: number, rootId: string) {
  if (threadWanted?.serverId !== serverId || threadWanted.rootId !== rootId) return
  try {
    const th = await client.getThread(serverId, rootId)
    if (threadWanted?.serverId !== serverId || threadWanted.rootId !== rootId) return
    const s = useStore.getState()
    if (s.selectedId === serverId) s.setThread(th)
  } catch (e) {
    report(e)
  }
}

// loadOlderReplies fetches one page of thread history above the window;
// resolves true when the thread was re-read with it (Feed's onLoadOlder contract).
export async function loadOlderReplies(serverId: number, rootId: string): Promise<boolean> {
  try {
    await client.loadOlderReplies(serverId, rootId)
    await refreshThread(serverId, rootId)
    return true
  } catch (e) {
    report(e)
    return false
  }
}

export const sendReply = (serverId: number, channelId: string, rootId: string, message: string, attachmentIds: string[] = []) =>
  client.sendReply(serverId, channelId, rootId, message, attachmentIds)

export const saveThreadDraft = (serverId: number, rootId: string, text: string) => {
  client.saveThreadDraft(serverId, rootId, text).catch(() => {}) // a lost draft is not worth an error banner
}

// refreshThreadAttachments: the reply composer's tray, on open and as a
// fallback for onAttachmentsChanged — mirrors refreshAttachments below.
export async function refreshThreadAttachments(serverId: number, channelId: string, rootId: string) {
  const my = ++threadAttachmentsSeq
  try {
    const list = await client.attachments(serverId, channelId, rootId)
    if (my !== threadAttachmentsSeq) return
    const s = useStore.getState()
    if (s.selectedId === serverId && s.thread?.root_id === rootId) s.setThreadAttachments(list)
  } catch {
    /* best-effort */
  }
}

// --- Attachments (the composer's tray) ----------------------------------

// refreshAttachments re-reads a (channel, root) composer's tray — on open,
// and as a fallback for onAttachmentsChanged (e.g. the very first load,
// before any attachments_changed event exists to react to). A failure is
// silent, like a lost draft: the tray simply stays empty until the next
// event or open. rootId: '' the channel's own composer.
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
// whole list, coalesced ~4/s during an upload): root_id '' is the channel's
// own composer, on screen; a non-empty root_id is a thread's — applied only
// if that thread is the one open in the panel. Another channel's or
// thread's tray is re-read fresh when it is opened.
export function onAttachmentsChanged(payload: Record<string, unknown> | undefined) {
  const p = payload ?? {}
  const rootId = String(p.root_id ?? '')
  const s = useStore.getState()
  if (Number(p.server_id) !== s.selectedId) return
  const items = (p.items as AttachmentView[] | undefined) ?? []
  if (rootId === '') {
    if (p.channel_id !== s.channel?.id) return
    // Bump the sequence: a refreshAttachments request made before this event
    // can still reply after it (it's a separate round trip); without this,
    // that older, now-stale reply would win the race and overwrite the list
    // this event just applied.
    attachmentsSeq++
    s.setAttachments(items)
    return
  }
  if (s.thread?.root_id !== rootId) return
  threadAttachmentsSeq++
  s.setThreadAttachments(items)
}

// onAttachmentRefused applies an EventAttachmentRefused payload: a drop had
// no caller to report its refusal to (too_many, not_dropped, …) — shown the
// same way a send error is, in the relevant composer (channel or thread).
export function onAttachmentRefused(payload: Record<string, unknown> | undefined) {
  const p = payload ?? {}
  const rootId = String(p.root_id ?? '')
  const s = useStore.getState()
  if (Number(p.server_id) !== s.selectedId) return
  const err = errorMessage(new ApiError(String(p.code ?? 'internal'), ''))
  if (rootId === '') {
    if (p.channel_id === s.channel?.id) s.setAttachError(err)
    return
  }
  if (s.thread?.root_id === rootId) s.setThreadAttachError(err)
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

// executeCommand runs a slash command sent from a composer (rootId '' for
// the channel's own); its answer shows as posts (an ephemeral_message one
// only I see).
export const executeCommand = (serverId: number, channelId: string, rootId: string, command: string) =>
  client.executeCommand(serverId, channelId, rootId, command)

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

// fileKey names a file across servers for fileSaves (store.ts): the same
// file id on two servers is two files.
export const fileKey = (serverId: number, fileId: string) => `${serverId}/${fileId}`

// Download feedback never goes to a banner above the feed (it shifted the
// layout — user report 2026-09-29): the file's own card shows it
// (DownloadButton.tsx), and a failure or a note is a floating toast.
const toastError = (e: unknown) => useStore.getState().showToast(errorMessage(e))

// revealSavedFile backs a saved file's "Show in folder" button: it resolves
// the download's list entry by its (unique) saved path and asks Go to show
// it — RevealDownload takes a downloads-list id, which DownloadFile/
// OpenFile's result does not carry.
export async function revealSavedFile(path: string) {
  try {
    const list = await client.downloads()
    const entry = list.find((d) => d.path === path)
    // No matching list entry (e.g. the download's SQLite insert failed
    // even though the file itself was saved) — say so instead of silently
    // doing nothing; there is no id to reveal by otherwise.
    if (!entry) {
      useStore.getState().showToast(t('downloads.notListed', { path }))
      return
    }
    await client.revealDownload(entry.id)
  } catch (e) {
    toastError(e)
  }
}

// saving marks the file "saving" at once — a big file takes a while — and
// "saved" with its path when Go is done (Go joins clicks on a file already
// downloading to that download). A failure puts the file back as it was (a
// copy saved earlier keeps its "Show in folder", without a ✓) and shows the
// error in a toast.
async function saving(serverId: number, file: { id: string; name: string }, save: () => Promise<{ path: string; opened: boolean }>) {
  const key = fileKey(serverId, file.id)
  // The copy saved earlier, if any — also while another click's download of
  // it is still running (its "saving" entry carries the earlier path).
  const path = useStore.getState().fileSaves[key]?.path ?? ''
  useStore.getState().setFileSave(key, { state: 'saving', path, savedAt: 0 })
  try {
    const r = await save()
    useStore.getState().setFileSave(key, { state: 'saved', path: r.path, savedAt: Date.now() })
    useStore.getState().announce(t('file.savedAnnounce', { name: file.name }))
    return r
  } catch (e) {
    const cur = useStore.getState().fileSaves[key]
    // a joined click may have succeeded meanwhile: keep its result
    if (cur?.state !== 'saved') useStore.getState().setFileSave(key, path ? { state: 'saved', path, savedAt: 0 } : null) // the copy, but no fresh ✓
    useStore.getState().showToast(t('file.downloadFailed', { name: file.name, detail: errorMessage(e) }))
    return null
  }
}

export const downloadFile = async (serverId: number, file: { id: string; name: string }) => {
  await saving(serverId, file, () => client.downloadFile(serverId, file.id))
}

// openFile saves and opens with the system app; Go refuses to open
// launchers (.desktop, scripts…) — then the user is told where the file is.
export const openFile = async (serverId: number, file: { id: string; name: string }) => {
  const r = await saving(serverId, file, () => client.openFile(serverId, file.id))
  if (r && !r.opened) useStore.getState().showToast(t('file.savedNotOpened', { path: r.path }), 'info')
}

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
    if (my === downloadsSeq) toastError(e)
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

export const openDownload = (id: number) => client.openDownload(id).catch(toastError)
export const revealDownload = (id: number) => client.revealDownload(id).catch(toastError)
export const removeDownload = (id: number) => client.removeDownload(id).catch(toastError)
export const clearDownloads = () => client.clearDownloads().catch(toastError)

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

// reactionUsers: no report() on failure — the tooltip/modal that calls this
// shows its own "…"/unknown state and must never surface a global error
// banner for a hover.
export const reactionUsers = (serverId: number, postId: string, emoji: string) => client.reactionUsers(serverId, postId, emoji)

// editLastOwn: ArrowUp on an empty composer edits the last own post — of a
// channel's feed or (thread.posts/thread.me_id) a thread panel's.
export function editLastOwn(view: Pick<ChannelDTO, 'posts' | 'me_id'>) {
  for (let i = view.posts.length - 1; i >= 0; i--) {
    const p = view.posts[i]
    if (p.user_id === view.me_id && !p.pending && !p.failed && !p.system) {
      useStore.getState().setEditing(p.id)
      return
    }
  }
}
