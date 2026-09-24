import { create } from 'zustand'
import type { ServerDTO } from './api/types'

interface State {
  servers: ServerDTO[]
  selectedId: number | null // null = "add server" screen
  lastError: string | null
  setServers(list: ServerDTO[]): void
  select(id: number | null): void
  setError(msg: string | null): void
}

export const useStore = create<State>((set, get) => ({
  servers: [],
  selectedId: null,
  lastError: null,
  setServers(list) {
    const sel = get().selectedId
    const stillThere = sel !== null && list.some((s) => s.id === sel)
    set({ servers: list, selectedId: stillThere ? sel : (list[0]?.id ?? null) })
  },
  select: (id) => set({ selectedId: id, lastError: null }),
  setError: (msg) => set({ lastError: msg }),
}))
