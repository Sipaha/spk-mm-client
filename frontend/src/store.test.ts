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
