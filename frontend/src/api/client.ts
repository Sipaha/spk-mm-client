import { Call, Events } from '@wailsio/runtime'
import type { ApiEvent, ServerDTO } from './types'

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
  subscribeEvents(onEvent: (e: ApiEvent) => void): () => void
}

const tokenMeta = () =>
  document.querySelector('meta[name="spk-mattermost-api-token"]')?.getAttribute('content') ?? ''

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

export const httpClient: Client = {
  listServers: () => post('ListServers', {}),
  addServer: (url) => post('AddServer', { url }),
  removeServer: (id) => post('RemoveServer', { id }),
  startGitLabLogin: (id) => post('StartGitLabLogin', { id }),
  loginWithPassword: (id, login, password) => post('LoginWithPassword', { id, login, password }),
  logout: (id) => post('Logout', { id }),
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

const FQN = 'github.com/spk/spk-mattermost/internal/api/transport.API.'

async function wcall<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    return (await Call.ByName(FQN + method, ...args)) as T
  } catch (e) {
    throw parseWailsError(e)
  }
}

const EVENT_TYPES = ['servers_changed', 'login_failed', 'open_external'] as const

export const wailsClient: Client = {
  listServers: () => wcall('ListServers'),
  addServer: (url) => wcall('AddServer', url),
  removeServer: (id) => wcall('RemoveServer', id),
  startGitLabLogin: (id) => wcall('StartGitLabLogin', id),
  loginWithPassword: (id, login, password) => wcall('LoginWithPassword', id, login, password),
  logout: (id) => wcall('Logout', id),
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

export const isDesktop = () => window.location.protocol === 'wails:'
export const client: Client = isDesktop() ? wailsClient : httpClient
