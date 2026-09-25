import { client } from './api/client'
import type { ChannelDTO } from './api/types'
import { errorMessage } from './errors'
import { t } from './i18n'
import { useStore } from './store'

// Responses can land out of order (a click while a refresh is in flight):
// each request takes a sequence number and only the latest one lands.
let sidebarSeq = 0
let channelSeq = 0
let wanted: { serverId: number; channelId: string } | null = null
let inFlight = false
let again = false

export const report = (e: unknown) => useStore.getState().setError(errorMessage(e))

export function resetChat() {
  sidebarSeq++
  channelSeq++
  wanted = null
  inFlight = false
  again = false
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

export const sendPost = (serverId: number, channelId: string, message: string) => client.sendPost(serverId, channelId, message)

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

// Same permalink form as Mattermost: <server>/<team>/pl/<post id>.
export const copyLink = (serverURL: string, teamName: string, postId: string) => {
  navigator.clipboard?.writeText(`${serverURL}/${teamName}/pl/${postId}`).catch(report)
}

export async function downloadFile(serverId: number, fileId: string) {
  try {
    const r = await client.downloadFile(serverId, fileId)
    useStore.getState().setNotice(t('file.saved', { path: r.path }))
  } catch (e) {
    report(e)
  }
}

// openFile saves and opens with the system app; Go refuses to open
// launchers (.desktop, scripts…) — then the user is told where the file is.
export async function openFile(serverId: number, fileId: string) {
  try {
    const r = await client.openFile(serverId, fileId)
    if (!r.opened) useStore.getState().setNotice(t('file.savedNotOpened', { path: r.path }))
  } catch (e) {
    report(e)
  }
}

export const react = (serverId: number, postId: string, emoji: string, add: boolean) => {
  ;(add ? client.addReaction(serverId, postId, emoji) : client.removeReaction(serverId, postId, emoji)).catch(report)
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
