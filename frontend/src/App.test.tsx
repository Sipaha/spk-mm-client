import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'u-alice', crt: false, muted: false, gap: { open: false, gen: 0, before_id: '', stale: false }, hist_rev: 0,
}
const aThread = {
  root_id: 'r1', channel_id: 'c-town', channel_name: 'Town Square', team_name: 'one', posts: [], has_more: false,
  capped: false, loaded: true, syncing: false, root_deleted: false, error: '', draft: '', me_id: 'u-alice', crt: false,
  new_since: 0, gap_after: '', focus: null,
}

vi.mock('./api/client', async (orig) => {
  const real = await orig<typeof import('./api/client')>()
  return {
    ...real,
    client: {
      ...real.httpClient,
      appInfo: vi.fn().mockResolvedValue({ format_locale: '' }),
      getLayout: vi.fn().mockResolvedValue({ sidebar_width: 0, thread_width: 0 }),
      setSidebarWidth: vi.fn().mockResolvedValue(undefined),
      setThreadWidth: vi.fn().mockResolvedValue(undefined),
      listServers: vi.fn(async () => h.list),
      selectServer: vi.fn().mockResolvedValue(undefined),
      setFocused: vi.fn().mockResolvedValue(undefined),
      networkChanged: vi.fn().mockResolvedValue(undefined),
      sidebar: vi.fn(async () => sb),
      openChannel: vi.fn(async () => town),
      getChannel: vi.fn(async () => town),
      downloads: vi.fn(async () => []),
      openThread: vi.fn(async () => aThread),
      getThread: vi.fn(async () => aThread),
      closeThread: vi.fn().mockResolvedValue(undefined),
      attachments: vi.fn(async () => []),
      jumpToPost: vi.fn(),
      loadNewer: vi.fn().mockResolvedValue(undefined),
      retryRevalidation: vi.fn().mockResolvedValue(undefined),
      openThreadAt: vi.fn(),
      loadThreadFocus: vi.fn().mockResolvedValue(undefined),
      retryThreadRevalidation: vi.fn().mockResolvedValue(undefined),
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
  useStore.setState({
    servers: [], selectedId: null, adding: false, lastError: null, signInFor: null, sidebar: null, channel: null,
    thread: null, threadAttachments: [], threadAttachError: null,
  })
})

test('a login error survives status updates and clears when the sign-in state changes', async () => {
  h.list = [srv()]
  render(<App />)
  // Let the initial refreshServers() (auto-selecting the first server, which
  // clears lastError) settle before a login_failed arrives — a real
  // login_failed can never race the mount-time load in practice, since it
  // only fires after the user has selected a server and attempted a sign-in.
  // The rail itself is hidden here (sidebar-menu brief addendum: exactly one
  // server), so wait on ServerPanel's own heading instead of the rail tile.
  await screen.findByRole('heading', { name: 'Acme' })
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

// Fix round 1 (coordinator review, theme-report.md): double-clicking the
// sidebar splitter must reset it to the built-in constant (256px), not to
// whatever width it currently has — a previous version passed the current
// width itself as the "default", making the reset a no-op.
test('double-clicking the sidebar splitter resets it to the built-in default, not its current width', async () => {
  const { client } = await import('./api/client')
  // 300: inside the sidebar's bounds on jsdom's default window width (768px)
  // with no thread panel open, so it lands unclamped — the point is that
  // it's not 256, not the exact number.
  vi.mocked(client.getLayout).mockResolvedValueOnce({ sidebar_width: 300, thread_width: 0 })
  h.list = [srv({ signed_in: true, username: 'alice', state: 'live' })]
  render(<App />)
  await screen.findByRole('heading', { name: /Town Square/ })
  const sep = await screen.findByRole('separator', { name: 'Resize sidebar' })
  await waitFor(() => expect(sep).toHaveAttribute('aria-valuenow', '300'))

  fireEvent.dblClick(sep)

  expect(sep).toHaveAttribute('aria-valuenow', '256')
  expect(client.setSidebarWidth).toHaveBeenCalledWith(256)
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

// Task 6: a reply notification's open_channel carries root_id — the channel
// opens, then its thread panel; thread_changed refreshes the open one.
test('open_channel with a root_id opens the channel then its thread panel; thread_changed refreshes it', async () => {
  const { client } = await import('./api/client')
  h.list = [srv({ signed_in: true, username: 'alice', state: 'live' })]
  render(<App />)
  await screen.findByRole('heading', { name: /Town Square/ })
  await act(async () => h.emit!({ type: 'open_channel', payload: { server_id: 1, channel_id: 'c-town', root_id: 'r1' } }))
  expect(client.openThread).toHaveBeenCalledWith(1, 'c-town', 'r1')
  expect(await screen.findByRole('complementary', { name: 'Thread' })).toBeInTheDocument()

  vi.mocked(client.getThread).mockResolvedValueOnce({ ...aThread, syncing: false, error: 'internal' })
  await act(async () => h.emit!({ type: 'thread_changed', payload: { server_id: 1, root_id: 'r1' } }))
  expect(client.getThread).toHaveBeenCalledWith(1, 'r1')
})

// Sidebar-menu-brief addendum (2026-09-29): the server rail is redundant
// clutter with only one server (its per-server picker has nothing to pick
// between) — hidden then, shown again as soon as a second server exists.
// ServerPanel (the sign-in form) has no menu of its own to reach "Add
// server"/other servers from — unlike the chat view — so the rail stays
// visible here even with a single, not-yet-signed-in server.
test('a single not-yet-signed-in server keeps the rail visible (ServerPanel has no menu of its own)', async () => {
  h.list = [srv()]
  render(<App />)
  expect(await screen.findByRole('heading', { name: 'Acme' })).toBeInTheDocument() // ServerPanel's own heading
  expect(screen.getByRole('button', { name: 'Acme' })).toBeInTheDocument() // the rail tile
})

test('the server rail shows with two or more servers and hides with exactly one, without disturbing the sidebar', async () => {
  h.list = [srv({ signed_in: true, username: 'alice', state: 'live' }), srv({ id: 2, name: 'Beta', signed_in: false })]
  render(<App />)
  expect(await screen.findByRole('button', { name: 'Acme' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Beta' })).toBeInTheDocument()

  h.list = [srv({ signed_in: true, username: 'alice', state: 'live' })]
  await act(async () => h.emit!({ type: 'servers_changed' }))
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Acme' })).toBeNull())
  expect(screen.getByRole('heading', { name: /Town Square/ })).toBeInTheDocument() // the sidebar itself is unaffected

  h.list = [srv({ signed_in: true, username: 'alice', state: 'live' }), srv({ id: 2, name: 'Beta', signed_in: false })]
  await act(async () => h.emit!({ type: 'servers_changed' }))
  expect(await screen.findByRole('button', { name: 'Acme' })).toBeInTheDocument() // rail comes back
})

// With the rail hidden, its "+" tile is unreachable — "Add server" moves
// into the sidebar's "⋯" menu (same flow: selecting no server shows the
// add-server form, exactly like the rail's "+" does today).
// The rail is hidden only *while actually chatting* with exactly one server
// (see the `showRail` comment in App.tsx): with no menu of its own,
// ServerPanel and AddServerForm would otherwise be a dead end (no way back
// to the one existing server) if the rail vanished there too. Picking "Add
// server" deselects the current one, chat turns false, and the rail — with
// that one server's own tile — comes right back, so the user isn't stuck.
test('with a single server (rail hidden), the sidebar "⋯" menu\'s Add server opens the add-server form, and the rail reappears there (no dead end)', async () => {
  h.list = [srv({ signed_in: true, username: 'alice', state: 'live' })]
  render(<App />)
  await screen.findByRole('heading', { name: /Town Square/ })
  expect(screen.queryByRole('button', { name: 'Acme' })).toBeNull() // rail hidden while chatting
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  await userEvent.click(screen.getByRole('menuitem', { name: 'Add server' }))
  expect(await screen.findByRole('heading', { name: 'Add a Mattermost server' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Acme' })).toBeInTheDocument() // rail back: a way to return to it
  await userEvent.click(screen.getByRole('button', { name: 'Acme' }))
  expect(await screen.findByRole('heading', { name: /Town Square/ })).toBeInTheDocument() // back in chat
})

test('a downloads_changed event refreshes the list and the header badge shows the active count', async () => {
  const { client } = await import('./api/client')
  h.list = [srv({ signed_in: true, username: 'alice', state: 'live' })]
  render(<App />)
  await screen.findByRole('heading', { name: /Town Square/ })
  vi.mocked(client.downloads).mockResolvedValue([
    { id: 1, server_id: 1, file_id: 'f1', name: 'a.txt', path: '', size: 5, mime: 'text/plain', started_at: 0,
      finished_at: 0, state: 'downloading', error: '', received: 2, exists: false, openable: true },
  ])
  await act(async () => h.emit!({ type: 'downloads_changed', payload: { id: 1 } }))
  expect(await screen.findByRole('button', { name: 'Downloads — active: 1' })).toBeInTheDocument()
})
