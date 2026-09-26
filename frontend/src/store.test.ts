import type { ServerDTO } from './api/types'
import { useStore } from './store'

const srv = (o: Partial<ServerDTO> = {}): ServerDTO => ({
  id: 1, name: 'A', url: 'https://a', signed_in: true, username: 'alice', gitlab: false,
  state: 'live', unread: false, mentions: 0, ...o,
})

beforeEach(() =>
  useStore.setState({ servers: [], selectedId: null, adding: false, lastError: null, signInFor: null, sidebar: null, channel: null }),
)

test('first server is selected on load, the add-server screen survives refreshes', () => {
  const s = useStore.getState()
  s.setServers([srv()])
  expect(useStore.getState().selectedId).toBe(1)
  useStore.getState().select(null)
  useStore.getState().setServers([srv({ mentions: 2 })])
  expect(useStore.getState().selectedId).toBeNull()
})

test('removed selected server falls back to the first one', () => {
  useStore.getState().setServers([srv(), srv({ id: 2 })])
  useStore.getState().select(2)
  useStore.getState().setServers([srv()])
  expect(useStore.getState().selectedId).toBe(1)
})

test('a login error survives badge updates and clears when sign-in state changes', () => {
  useStore.getState().setServers([srv({ signed_in: false, state: 'off' })])
  useStore.getState().loginFailed('boom')
  useStore.getState().setServers([srv({ signed_in: false, state: 'off', name: 'A2' })])
  expect(useStore.getState().lastError).toBe('boom')
  useStore.getState().setServers([srv({ state: 'connecting' })])
  expect(useStore.getState().lastError).toBeNull()
})

test('re-auth form closes once the server is no longer needs_reauth', () => {
  useStore.getState().setServers([srv({ state: 'needs_reauth' })])
  useStore.getState().showSignIn(1)
  useStore.getState().setServers([srv({ state: 'needs_reauth', mentions: 1 })])
  expect(useStore.getState().signInFor).toBe(1)
  useStore.getState().setServers([srv({ state: 'connecting' })])
  expect(useStore.getState().signInFor).toBeNull()
})

test('views of another server are ignored', () => {
  useStore.getState().setServers([srv(), srv({ id: 2 })])
  useStore.getState().setSidebar(2, { team_id: 't', selected_channel_id: '', teams: null, categories: null })
  expect(useStore.getState().sidebar).toBeNull()
})

const chan = (id: string) => ({
  id, name: id, type: 'O', header: '', purpose: '', team_id: 't', team_name: 'team', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'me', crt: false, muted: false,
})

test('switching to a different channel drops an in-progress edit; refreshing the same one does not', () => {
  useStore.getState().setServers([srv()])
  useStore.getState().setChannel(1, chan('a'))
  useStore.getState().setEditing('p1')
  useStore.getState().setChannel(1, chan('a')) // a content refresh of the still-open channel (e.g. channel_changed)
  expect(useStore.getState().editingId).toBe('p1')
  useStore.getState().setChannel(1, chan('b')) // the user actually switched channels
  expect(useStore.getState().editingId).toBeNull()
})

test('a server going live bumps its live epoch; other updates do not', () => {
  const epoch = (id: number) => useStore.getState().liveEpochs[id] ?? 0
  useStore.getState().setServers([srv({ state: 'connecting' }), srv({ id: 2, state: 'live' })])
  const e1 = epoch(1)
  const e2 = epoch(2)
  useStore.getState().setServers([srv({ state: 'live' }), srv({ id: 2, state: 'live', mentions: 3 })])
  expect(epoch(1)).toBe(e1 + 1)
  expect(epoch(2)).toBe(e2)
  useStore.getState().setServers([srv({ state: 'reconnecting' }), srv({ id: 2, state: 'live' })])
  expect(epoch(1)).toBe(e1 + 1)
  useStore.getState().setServers([srv({ state: 'live' }), srv({ id: 2, state: 'live' })])
  expect(epoch(1)).toBe(e1 + 2)
})
