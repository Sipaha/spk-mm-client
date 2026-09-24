import { client } from './api/client'
import { errorMessage } from './errors'
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
