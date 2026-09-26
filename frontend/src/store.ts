import { create } from 'zustand'
import type { AppInfo, ChannelDTO, DownloadView, ServerDTO, SidebarDTO } from './api/types'

export interface NoticeAction {
  label: string
  onClick(): void
}

interface State {
  servers: ServerDTO[]
  selectedId: number | null // null = "add server" screen
  adding: boolean // the user chose "add server": list refreshes keep that screen
  lastError: string | null
  loginFailures: number // bumped on every login_failed event
  info: AppInfo
  signInFor: number | null // "Sign in again" opened the sign-in form over this server's cached chats
  sidebar: SidebarDTO | null // of the selected server
  channel: ChannelDTO | null // open channel of the selected server
  editingId: string | null // post being edited inline
  notice: string | null // info banner (e.g. where a download went)
  noticeSticky: boolean // the notice stays until replaced (a download in progress)
  noticeAction: NoticeAction | null // e.g. the saved notice's "Show in folder"
  // liveEpochs: per server, bumped each time it goes live. A picture or
  // snippet that failed to load is remembered only for the epoch it failed
  // in — offline, /media/ answers 404 — and tried again after the next live.
  liveEpochs: Record<number, number>
  // downloads: the browser-like downloads list (newest first). Held only
  // while the panel is open or a download is active — otherwise cleared, so
  // it never grows across a long session (see setDownloads/setDownloadsOpen).
  downloads: DownloadView[]
  downloadsOpen: boolean
  setServers(list: ServerDTO[]): void
  select(id: number | null): void
  setError(msg: string | null): void
  loginFailed(msg: string): void
  setInfo(info: AppInfo): void
  showSignIn(id: number | null): void
  setSidebar(serverId: number, sb: SidebarDTO): void
  setChannel(serverId: number, ch: ChannelDTO): void
  setEditing(id: string | null): void
  setNotice(msg: string | null, sticky?: boolean, action?: NoticeAction | null): void
  setDownloads(list: DownloadView[]): void
  setDownloadsOpen(open: boolean): void
  patchDownloadProgress(id: number, received: number): void
}

const hasActiveDownload = (list: DownloadView[]) => list.some((d) => d.state === 'downloading')

const cleared = { sidebar: null, channel: null, editingId: null }

export const useStore = create<State>((set, get) => ({
  servers: [],
  selectedId: null,
  adding: false,
  lastError: null,
  loginFailures: 0,
  info: { format_locale: '' },
  signInFor: null,
  sidebar: null,
  channel: null,
  editingId: null,
  notice: null,
  noticeSticky: false,
  noticeAction: null,
  liveEpochs: {},
  downloads: [],
  downloadsOpen: false,
  setServers(list) {
    const { selectedId: sel, adding, servers: prev, signInFor, lastError } = get()
    const stillThere = sel !== null && list.some((s) => s.id === sel)
    const selectedId = stillThere ? sel : adding ? null : (list[0]?.id ?? null)
    // A sign-in or sign-out supersedes an earlier error; badge and
    // connection-state updates (now frequent) do not.
    const signInChanged = list.some((s) => prev.find((p) => p.id === s.id)?.signed_in !== s.signed_in)
    const reauth = list.find((s) => s.id === signInFor)
    let liveEpochs = get().liveEpochs
    for (const s of list) {
      if (s.state === 'live' && prev.find((p) => p.id === s.id)?.state !== 'live') {
        liveEpochs = { ...liveEpochs, [s.id]: (liveEpochs[s.id] ?? 0) + 1 }
      }
    }
    set({
      servers: list,
      liveEpochs,
      selectedId,
      lastError: signInChanged ? null : lastError,
      signInFor: reauth?.state === 'needs_reauth' ? signInFor : null,
      ...(selectedId !== sel ? cleared : {}),
    })
  },
  select: (id) => set({ selectedId: id, adding: id === null, lastError: null, signInFor: null, ...cleared }),
  setError: (msg) => set({ lastError: msg }),
  loginFailed: (msg) => set((s) => ({ lastError: msg, loginFailures: s.loginFailures + 1 })),
  setInfo: (info) => set({ info }),
  showSignIn: (id) => set({ signInFor: id }),
  setSidebar(serverId, sb) {
    if (get().selectedId === serverId) set({ sidebar: sb })
  },
  setChannel(serverId, ch) {
    if (get().selectedId !== serverId) return
    // A different channel: drop any in-progress edit (it belonged to the
    // previous one). A refresh of the *same* open channel (e.g. a
    // channel_changed event) must not interrupt an edit in progress.
    const switchedChannel = get().channel?.id !== ch.id
    set({ channel: ch, ...(switchedChannel ? { editingId: null } : {}) })
  },
  setEditing: (id) => set({ editingId: id }),
  setNotice: (msg, sticky = false, action = null) =>
    set({ notice: msg, noticeSticky: msg !== null && sticky, noticeAction: msg !== null ? action : null }),
  setDownloads(list) {
    set({ downloads: get().downloadsOpen || hasActiveDownload(list) ? list : [] })
  },
  setDownloadsOpen(open) {
    set((s) => ({ downloadsOpen: open, downloads: open || hasActiveDownload(s.downloads) ? s.downloads : [] }))
  },
  patchDownloadProgress(id, received) {
    set((s) => ({ downloads: s.downloads.map((d) => (d.id === id ? { ...d, received } : d)) }))
  },
}))

// useLiveEpoch: the server's live epoch (see State.liveEpochs).
export const useLiveEpoch = (serverId: number) => useStore((s) => s.liveEpochs[serverId] ?? 0)
