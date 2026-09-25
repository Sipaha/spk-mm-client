import { vi } from 'vitest'
import type { ChannelDTO, ServerDTO, SidebarDTO } from './api/types'
import { setLocale } from './i18n'
import { useStore } from './store'

vi.mock('./api/client', () => ({
  client: {
    openChannel: vi.fn(),
    getChannel: vi.fn(),
    sidebar: vi.fn(),
    selectServer: vi.fn().mockResolvedValue(undefined),
    listServers: vi.fn(),
    editPost: vi.fn(),
    downloadFile: vi.fn(),
    openFile: vi.fn(),
  },
}))

const { client } = await import('./api/client')
const { downloadFile, editPost, loadSidebar, openChannel, openFile, refreshChannel, resetChat, selectServer } = await import('./chat')

function deferred<T>() {
  let resolve!: (v: T) => void
  const p = new Promise<T>((r) => (resolve = r))
  return { p, resolve }
}

const srv = (id: number): ServerDTO => ({
  id, name: 'S' + id, url: 'https://s', signed_in: true, username: 'alice', gitlab: false,
  state: 'live', unread: false, mentions: 0,
})

const chan = (id: string, over: Partial<ChannelDTO> = {}): ChannelDTO => ({
  id, name: id.toUpperCase(), type: 'O', header: '', purpose: '', team_id: 't1', team_name: 'team', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'u-alice',
  crt: false, muted: false, ...over,
})

const sidebar = (over: Partial<SidebarDTO> = {}): SidebarDTO => ({
  team_id: 't1', selected_channel_id: 'a', teams: [], categories: [], ...over,
})

beforeEach(() => {
  resetChat()
  vi.mocked(client.openChannel).mockReset()
  vi.mocked(client.getChannel).mockReset()
  vi.mocked(client.sidebar).mockReset()
  vi.mocked(client.editPost).mockReset()
  useStore.setState({ servers: [srv(1), srv(2)], selectedId: 1, adding: false, sidebar: null, channel: null, lastError: null, editingId: null })
})

test('loading the sidebar opens its selected channel', async () => {
  vi.mocked(client.sidebar).mockResolvedValue(sidebar())
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  await loadSidebar(1)
  expect(client.openChannel).toHaveBeenCalledWith(1, 'a')
  expect(useStore.getState().channel?.id).toBe('a')
})

test('a click during a refresh wins', async () => {
  vi.mocked(client.openChannel).mockResolvedValueOnce(chan('a'))
  await openChannel(1, 'a')
  const stale = deferred<ChannelDTO>()
  vi.mocked(client.getChannel).mockReturnValue(stale.p)
  const refresh = refreshChannel(1, 'a')
  vi.mocked(client.openChannel).mockResolvedValueOnce(chan('b'))
  await openChannel(1, 'b')
  stale.resolve(chan('a'))
  await refresh
  expect(useStore.getState().channel?.id).toBe('b')
})

test('a change during an open is fetched right after it', async () => {
  const open = deferred<ChannelDTO>()
  vi.mocked(client.openChannel).mockReturnValue(open.p)
  vi.mocked(client.getChannel).mockResolvedValue(chan('a', { name: 'fresh' }))
  const o = openChannel(1, 'a')
  await refreshChannel(1, 'a')
  expect(client.getChannel).not.toHaveBeenCalled()
  open.resolve(chan('a'))
  await o
  await vi.waitFor(() => expect(useStore.getState().channel?.name).toBe('fresh'))
})

test('responses for a server no longer selected are dropped', async () => {
  const sb = deferred<SidebarDTO>()
  vi.mocked(client.sidebar).mockReturnValueOnce(sb.p).mockResolvedValue(sidebar({ selected_channel_id: '' }))
  const l = loadSidebar(1)
  selectServer(2)
  sb.resolve(sidebar())
  await l
  expect(client.openChannel).not.toHaveBeenCalled()
  expect(useStore.getState().selectedId).toBe(2)
})

test("saving post A while editing B doesn't close B's edit box", async () => {
  const a = deferred<void>()
  vi.mocked(client.editPost).mockReturnValueOnce(a.p)
  useStore.getState().setEditing('postA')
  const saveA = editPost(1, 'postA', 'edited A')
  useStore.getState().setEditing('postB') // the user moved on to editing B before A's save landed
  a.resolve(undefined)
  await saveA
  expect(useStore.getState().editingId).toBe('postB')
})

test('opening a channel of another team switches the sidebar team', async () => {
  vi.mocked(client.sidebar).mockResolvedValueOnce(sidebar()).mockResolvedValueOnce(sidebar({ team_id: 't2' }))
  vi.mocked(client.openChannel).mockResolvedValueOnce(chan('a')).mockResolvedValueOnce(chan('x', { team_id: 't2' }))
  await loadSidebar(1)
  await openChannel(1, 'x')
  await vi.waitFor(() => expect(useStore.getState().sidebar?.team_id).toBe('t2'))
  expect(client.sidebar).toHaveBeenLastCalledWith(1, 't2')
})

test('a download says where the file went; an unopened one says why', async () => {
  setLocale('en')
  vi.mocked(client.downloadFile).mockResolvedValue({ path: '/home/a/Downloads/spec.pdf', opened: false })
  await downloadFile(1, 'f-spec')
  expect(useStore.getState().notice).toBe('Saved to /home/a/Downloads/spec.pdf')
  vi.mocked(client.openFile).mockResolvedValue({ path: '/d/run.desktop', opened: false })
  await openFile(1, 'f-x')
  expect(useStore.getState().notice).toBe('Saved to /d/run.desktop. Files of this type are not opened automatically.')
  useStore.getState().setNotice(null)
  vi.mocked(client.openFile).mockResolvedValue({ path: '/d/a.log', opened: true })
  await openFile(1, 'f-log')
  expect(useStore.getState().notice).toBeNull()
})
