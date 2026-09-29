import { create } from 'zustand'
import type { AppInfo, AttachmentView, ChannelDTO, DownloadView, ServerDTO, SidebarDTO, ThreadDTO } from './api/types'
import { forgetRecent } from './emoji/recent'

// FileSave: a file's download as its own card shows it (no banner — it
// shifted the layout, user report 2026-09-29): "saving" while Go fetches it,
// then "saved" with where it went (savedAt: when, for the card's brief ✓).
export interface FileSave {
  state: 'saving' | 'saved'
  path: string // '' while saving a file never saved before
  savedAt: number // Date.now() of the save; 0 for none yet, or after a failed re-download (no ✓)
}

// FILE_SAVES_CAP bounds fileSaves over a long session: the oldest entries go
// (their cards just lose "Show in folder" — the downloads panel keeps it).
export const FILE_SAVES_CAP = 200

// Toast: a short floating message (a download's error, "saved but not
// opened"), outside the layout flow — see Toast.tsx.
export interface ToastMsg {
  id: number
  text: string
  tone: 'error' | 'info'
}
let toastSeq = 0

interface State {
  servers: ServerDTO[]
  selectedId: number | null // null = "add server" screen
  adding: boolean // the user chose "add server": list refreshes keep that screen
  lastError: string | null
  loginFailures: number // bumped on every login_failed event
  info: AppInfo
  // formattingBarHidden: the composer's Aa toggle (composer brief
  // 2026-09-28) — app-wide, not per channel/thread, persisted through
  // internal/api's GetFormattingBarHidden/SetFormattingBarHidden (same
  // ui_prefs mechanism as the layout splitters). formattingBarLoaded guards
  // Composer's one-time fetch on first mount against a refetch on every
  // channel/thread switch (Composer is remounted per channel/root).
  formattingBarHidden: boolean
  formattingBarLoaded: boolean
  signInFor: number | null // "Sign in again" opened the sign-in form over this server's cached chats
  sidebar: SidebarDTO | null // of the selected server
  channel: ChannelDTO | null // open channel of the selected server
  editingId: string | null // post being edited inline
  // fileSaves: per file (fileKey: "<server>/<file id>"), its download's
  // state for the session — see FileSave. Insertion order is age: a set moves
  // the key to the end, and past FILE_SAVES_CAP the first keys are dropped.
  fileSaves: Record<string, FileSave>
  toast: ToastMsg | null
  // liveEpochs: per server, bumped each time it goes live. A picture or
  // snippet that failed to load is remembered only for the epoch it failed
  // in — offline, /media/ answers 404 — and tried again after the next live.
  liveEpochs: Record<number, number>
  // downloads: the browser-like downloads list (newest first). Held only
  // while the panel is open or a download is active — otherwise cleared, so
  // it never grows across a long session (see setDownloads/setDownloadsOpen).
  downloads: DownloadView[]
  downloadsOpen: boolean
  // attachments: the open channel's composer tray (a draft, kept by Go per
  // channel — see AGENTS.md "Вложения"); reset when the open channel
  // changes and refetched by chat.ts's refreshAttachments right after.
  attachments: AttachmentView[]
  // attachError: a message the composer shows like a send error — a limit
  // (too_large, too_many, …) or an attachment_refused event (a drop with
  // no caller to report to). Reset with the channel, same as attachments.
  attachError: string | null
  // thread: the panel's open thread (Task 6) — null when no thread is open.
  // Reset with the channel or server, like sidebar/channel (chat.ts's
  // openChannel/selectServer also close the Go-side panel with it).
  thread: ThreadDTO | null
  threadAttachments: AttachmentView[]
  threadAttachError: string | null
  setServers(list: ServerDTO[]): void
  select(id: number | null): void
  setError(msg: string | null): void
  loginFailed(msg: string): void
  setInfo(info: AppInfo): void
  setFormattingBarHidden(hidden: boolean): void
  showSignIn(id: number | null): void
  setSidebar(serverId: number, sb: SidebarDTO): void
  setChannel(serverId: number, ch: ChannelDTO): void
  setEditing(id: string | null): void
  setFileSave(key: string, save: FileSave | null): void
  showToast(text: string, tone?: ToastMsg['tone']): void
  dismissToast(id?: number): void // without an id: whatever is shown
  setDownloads(list: DownloadView[]): void
  setDownloadsOpen(open: boolean): void
  patchDownloadProgress(id: number, received: number): void
  setAttachments(list: AttachmentView[]): void
  setAttachError(msg: string | null): void
  setThread(thread: ThreadDTO | null): void
  setThreadAttachments(list: AttachmentView[]): void
  setThreadAttachError(msg: string | null): void
}

const hasActiveDownload = (list: DownloadView[]) => list.some((d) => d.state === 'downloading')

const cleared = {
  sidebar: null, channel: null, editingId: null, attachments: [], attachError: null,
  thread: null, threadAttachments: [], threadAttachError: null,
}

export const useStore = create<State>((set, get) => ({
  servers: [],
  selectedId: null,
  adding: false,
  lastError: null,
  loginFailures: 0,
  info: { format_locale: '' },
  formattingBarHidden: false,
  formattingBarLoaded: false,
  signInFor: null,
  sidebar: null,
  channel: null,
  editingId: null,
  fileSaves: {},
  toast: null,
  liveEpochs: {},
  downloads: [],
  downloadsOpen: false,
  attachments: [],
  attachError: null,
  thread: null,
  threadAttachments: [],
  threadAttachError: null,
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
    // A server no longer in the list was removed: drop its quick-reactions
    // cache (emoji/recent.ts) — an id is never reused, so keeping it would
    // just grow the cache forever across add/remove churn.
    for (const p of prev) {
      if (!list.some((s) => s.id === p.id)) forgetRecent(p.id)
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
  setFormattingBarHidden: (hidden) => set({ formattingBarHidden: hidden, formattingBarLoaded: true }),
  showSignIn: (id) => set({ signInFor: id }),
  setSidebar(serverId, sb) {
    if (get().selectedId === serverId) set({ sidebar: sb })
  },
  setChannel(serverId, ch) {
    if (get().selectedId !== serverId) return
    // A different channel: drop any in-progress edit (it belonged to the
    // previous one) and the composer's attachments/error (they belong to
    // it too — chat.ts's refreshAttachments refetches the new channel's
    // right after). A refresh of the *same* open channel (e.g. a
    // channel_changed event) must not interrupt an edit or a draft tray.
    const switchedChannel = get().channel?.id !== ch.id
    set({
      channel: ch,
      ...(switchedChannel ? { editingId: null, attachments: [], attachError: null, thread: null, threadAttachments: [], threadAttachError: null } : {}),
    })
  },
  setEditing: (id) => set({ editingId: id }),
  setFileSave(key, save) {
    const next = { ...get().fileSaves }
    delete next[key]
    if (save) next[key] = save
    const keys = Object.keys(next)
    for (let i = 0; i < keys.length - FILE_SAVES_CAP; i++) delete next[keys[i]]
    set({ fileSaves: next })
  },
  showToast: (text, tone = 'error') => set({ toast: { id: ++toastSeq, text, tone } }),
  dismissToast(id) {
    const cur = get().toast
    if (cur && (id === undefined || cur.id === id)) set({ toast: null })
  },
  setDownloads(list) {
    set({ downloads: get().downloadsOpen || hasActiveDownload(list) ? list : [] })
  },
  setDownloadsOpen(open) {
    set((s) => ({ downloadsOpen: open, downloads: open || hasActiveDownload(s.downloads) ? s.downloads : [] }))
  },
  patchDownloadProgress(id, received) {
    set((s) => ({ downloads: s.downloads.map((d) => (d.id === id ? { ...d, received } : d)) }))
  },
  setAttachments: (list) => set({ attachments: list }),
  setAttachError: (msg) => set({ attachError: msg }),
  setThread: (thread) => set({ thread }),
  setThreadAttachments: (list) => set({ threadAttachments: list }),
  setThreadAttachError: (msg) => set({ threadAttachError: msg }),
}))

// useLiveEpoch: the server's live epoch (see State.liveEpochs).
export const useLiveEpoch = (serverId: number) => useStore((s) => s.liveEpochs[serverId] ?? 0)
