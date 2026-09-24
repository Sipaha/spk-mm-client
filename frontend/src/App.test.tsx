import { act, render, screen } from '@testing-library/react'
import { vi } from 'vitest'
import type { ApiEvent, ChannelDTO, ServerDTO, SidebarDTO } from './api/types'
import { setLocale } from './i18n'
import { useStore } from './store'

const h = vi.hoisted(() => ({ emit: null as ((e: ApiEvent) => void) | null, list: [] as unknown[] }))

const sb: SidebarDTO = {
  team_id: 't1', selected_channel_id: 'c-town', teams: [],
  categories: [{ id: 'ch', type: 'channels', name: 'Channels', collapsed: false, channels: [{ id: 'c-town', name: 'Town Square', type: 'O', unread: false, mentions: 0, muted: false }] }],
}
const town: ChannelDTO = {
  id: 'c-town', name: 'Town Square', type: 'O', header: 'Everything', purpose: '', team_id: 't1', team_name: 'one', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'u-alice', crt: false, muted: false,
}

vi.mock('./api/client', async (orig) => {
  const real = await orig<typeof import('./api/client')>()
  return {
    ...real,
    client: {
      ...real.httpClient,
      appInfo: vi.fn().mockResolvedValue({ format_locale: '' }),
      listServers: vi.fn(async () => h.list),
      selectServer: vi.fn().mockResolvedValue(undefined),
      setFocused: vi.fn().mockResolvedValue(undefined),
      networkChanged: vi.fn().mockResolvedValue(undefined),
      sidebar: vi.fn(async () => sb),
      openChannel: vi.fn(async () => town),
      getChannel: vi.fn(async () => town),
      subscribeEvents: (fn: (e: ApiEvent) => void) => {
        h.emit = fn
        return () => {}
      },
    },
  }
})

const { App } = await import('./App')
const { resetChat } = await import('./chat')

const srv = (o: Partial<ServerDTO> = {}): ServerDTO => ({
  id: 1, name: 'Acme', url: 'https://mm.acme', signed_in: false, username: '', gitlab: false,
  state: 'off', unread: false, mentions: 0, ...o,
})

beforeEach(() => {
  setLocale('en')
  resetChat()
  useStore.setState({ servers: [], selectedId: null, adding: false, lastError: null, signInFor: null, sidebar: null, channel: null })
})

test('a login error survives status updates and clears when the sign-in state changes', async () => {
  h.list = [srv()]
  render(<App />)
  // Let the initial refreshServers() (auto-selecting the first server, which
  // clears lastError) settle before a login_failed arrives — a real
  // login_failed can never race the mount-time load in practice, since it
  // only fires after the user has selected a server and attempted a sign-in.
  await screen.findByRole('button', { name: 'Acme' })
  await act(async () => h.emit!({ type: 'login_failed', payload: { code: 'no_pending_login' } }))
  expect(screen.getByRole('alert')).toBeInTheDocument()
  await act(async () => h.emit!({ type: 'servers_changed' }))
  expect(screen.getByRole('alert')).toBeInTheDocument()
  h.list = [srv({ signed_in: true, username: 'alice', state: 'connecting' })]
  await act(async () => h.emit!({ type: 'servers_changed' }))
  expect(screen.queryByRole('alert')).toBeNull()
})

test('a signed-in server shows its sidebar and the selected channel', async () => {
  h.list = [srv({ signed_in: true, username: 'alice', state: 'live' })]
  render(<App />)
  expect(await screen.findByRole('heading', { name: /Town Square/ })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /Town Square/ })).toHaveAttribute('aria-current', 'true')
})

test('needs_reauth keeps the chat and offers the sign-in form', async () => {
  h.list = [srv({ signed_in: true, username: 'alice', state: 'needs_reauth' })]
  render(<App />)
  await screen.findByRole('heading', { name: /Town Square/ })
  expect(screen.getByText('Session expired — showing saved messages.')).toBeInTheDocument()
  await act(async () => screen.getAllByRole('button', { name: 'Sign in again' })[0].click())
  expect(screen.getByText('Your session on this server expired — sign in again')).toBeInTheDocument()
})

test('window focus and network changes are reported to Go', async () => {
  const { client } = await import('./api/client')
  h.list = []
  render(<App />)
  vi.mocked(client.setFocused).mockClear()
  await act(async () => window.dispatchEvent(new Event('blur')))
  expect(client.setFocused).toHaveBeenLastCalledWith(false)
  await act(async () => window.dispatchEvent(new Event('focus')))
  expect(client.setFocused).toHaveBeenLastCalledWith(true)
  await act(async () => window.dispatchEvent(new Event('online')))
  expect(client.networkChanged).toHaveBeenCalled()
})
