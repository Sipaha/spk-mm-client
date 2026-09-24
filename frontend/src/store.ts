import { create } from 'zustand'
import type { AppInfo, ChannelDTO, ServerDTO, SidebarDTO } from './api/types'

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
  setServers(list: ServerDTO[]): void
  select(id: number | null): void
  setError(msg: string | null): void
  loginFailed(msg: string): void
  setInfo(info: AppInfo): void
  showSignIn(id: number | null): void
  setSidebar(serverId: number, sb: SidebarDTO): void
  setChannel(serverId: number, ch: ChannelDTO): void
  setEditing(id: string | null): void
}

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
  setServers(list) {
    const { selectedId: sel, adding, servers: prev, signInFor, lastError } = get()
    const stillThere = sel !== null && list.some((s) => s.id === sel)
    const selectedId = stillThere ? sel : adding ? null : (list[0]?.id ?? null)
    // A sign-in or sign-out supersedes an earlier error; badge and
    // connection-state updates (now frequent) do not.
    const signInChanged = list.some((s) => prev.find((p) => p.id === s.id)?.signed_in !== s.signed_in)
    const reauth = list.find((s) => s.id === signInFor)
    set({
      servers: list,
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
}))
