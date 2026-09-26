import { vi } from 'vitest'
import type { ChannelDTO, DownloadView, ServerDTO, SidebarDTO } from './api/types'
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
    downloads: vi.fn(),
    openDownload: vi.fn(),
    revealDownload: vi.fn(),
    removeDownload: vi.fn(),
    clearDownloads: vi.fn(),
  },
  ApiError: class ApiError extends Error {},
}))

const { client } = await import('./api/client')
const {
  clearDownloads, closeDownloadsPanel, downloadFile, downloadPrimaryAction, editPost, loadSidebar,
  onDownloadsChanged, openChannel, openDownload, openDownloadsPanel, openFile, refreshChannel,
  refreshDownloads, removeDownload, resetChat, revealDownload, selectServer,
} = await import('./chat')

const dl = (over: Partial<DownloadView> = {}): DownloadView => ({
  id: 1, server_id: 1, file_id: 'f1', name: 'a.txt', path: '/d/a.txt', size: 10, mime: 'text/plain',
  started_at: 0, finished_at: 1, state: 'done', error: '', received: 10, exists: true, openable: true, ...over,
})

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
  vi.mocked(client.downloads).mockReset()
  vi.mocked(client.openDownload).mockReset().mockResolvedValue(true)
  vi.mocked(client.revealDownload).mockReset().mockResolvedValue(undefined)
  vi.mocked(client.removeDownload).mockReset().mockResolvedValue(undefined)
  vi.mocked(client.clearDownloads).mockReset().mockResolvedValue(undefined)
  useStore.setState({
    servers: [srv(1), srv(2)], selectedId: 1, adding: false, sidebar: null, channel: null, lastError: null,
    editingId: null, downloads: [], downloadsOpen: false,
  })
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

const spec = { id: 'f-spec', name: 'spec.pdf' }

test('a download says where the file went; an unopened one says why', async () => {
  setLocale('en')
  vi.mocked(client.downloadFile).mockResolvedValue({ path: '/home/a/Downloads/spec.pdf', opened: false })
  await downloadFile(1, spec)
  expect(useStore.getState().notice).toBe('Saved to /home/a/Downloads/spec.pdf')
  vi.mocked(client.openFile).mockResolvedValue({ path: '/d/run.desktop', opened: false })
  await openFile(1, { id: 'f-x', name: 'run.desktop' })
  expect(useStore.getState().notice).toBe('Saved to /d/run.desktop. Files of this type are not opened automatically.')
  useStore.getState().setNotice(null)
  vi.mocked(client.openFile).mockResolvedValue({ path: '/d/a.log', opened: true })
  await openFile(1, { id: 'f-log', name: 'a.log' })
  expect(useStore.getState().notice).toBeNull()
})

test('a download in progress is shown at once and stays until it is replaced', async () => {
  setLocale('en')
  let done!: (r: { path: string; opened: boolean }) => void
  vi.mocked(client.downloadFile).mockReturnValueOnce(new Promise((r) => (done = r)))
  const saving = downloadFile(1, spec)
  expect(useStore.getState().notice).toBe('Downloading spec.pdf…')
  expect(useStore.getState().noticeSticky).toBe(true)
  done({ path: '/d/spec.pdf', opened: false })
  await saving
  expect(useStore.getState().notice).toBe('Saved to /d/spec.pdf')
  expect(useStore.getState().noticeSticky).toBe(false)

  vi.mocked(client.downloadFile).mockRejectedValueOnce(new Error('boom'))
  await downloadFile(1, spec)
  expect(useStore.getState().notice).toBeNull()
  expect(useStore.getState().lastError).not.toBeNull()

  let opened!: (r: { path: string; opened: boolean }) => void
  vi.mocked(client.openFile).mockReturnValueOnce(new Promise((r) => (opened = r)))
  const opening = openFile(1, { id: 'f-log', name: 'a.log' })
  expect(useStore.getState().notice).toBe('Downloading a.log…')
  opened({ path: '/d/a.log', opened: true })
  await opening
  expect(useStore.getState().notice).toBeNull()
  setLocale('ru')
  const ru = downloadFile(1, spec)
  expect(useStore.getState().notice).toBe('Скачивается spec.pdf…')
  await ru
})

test('the saved notice carries a "show in folder" action resolving the entry by path', async () => {
  setLocale('en')
  vi.mocked(client.downloadFile).mockResolvedValue({ path: '/d/spec.pdf', opened: false })
  await downloadFile(1, spec)
  const action = useStore.getState().noticeAction
  expect(action?.label).toBe('Show in folder')

  vi.mocked(client.downloads).mockResolvedValue([dl({ id: 9, path: '/d/spec.pdf' }), dl({ id: 10, path: '/d/other.txt' })])
  action?.onClick()
  await vi.waitFor(() => expect(client.revealDownload).toHaveBeenCalledWith(9))
})

test('two overlapping refreshDownloads reloads resolving out of order: the newer one wins', async () => {
  useStore.getState().setDownloadsOpen(true)
  const first = deferred<DownloadView[]>()
  const second = deferred<DownloadView[]>()
  vi.mocked(client.downloads).mockReturnValueOnce(first.p).mockReturnValueOnce(second.p)
  const r1 = refreshDownloads() // started first
  const r2 = refreshDownloads() // started right after, while r1 is still in flight
  second.resolve([dl({ id: 2, name: 'second' })]) // the newer request resolves first
  await r2
  first.resolve([dl({ id: 1, name: 'first' })]) // the older one lands late
  await r1
  expect(useStore.getState().downloads.map((d) => d.name)).toEqual(['second'])
})

test('downloads panel: first open loads the list once, later opens rely on events', async () => {
  vi.mocked(client.downloads).mockResolvedValue([dl()])
  openDownloadsPanel()
  await vi.waitFor(() => expect(useStore.getState().downloads).toHaveLength(1))
  expect(client.downloads).toHaveBeenCalledTimes(1)

  closeDownloadsPanel()
  expect(useStore.getState().downloadsOpen).toBe(false)
  expect(useStore.getState().downloads).toEqual([]) // nothing active: dropped on close

  openDownloadsPanel()
  expect(client.downloads).toHaveBeenCalledTimes(1) // not re-fetched: only downloads_changed refreshes it now
})

test('onDownloadsChanged patches progress in place, and reloads on state/new/clear', async () => {
  useStore.getState().setDownloadsOpen(true)
  useStore.getState().setDownloads([dl({ id: 5, state: 'downloading', received: 1 })])

  onDownloadsChanged({ id: 5, received: 42 })
  expect(useStore.getState().downloads[0].received).toBe(42)
  expect(client.downloads).not.toHaveBeenCalled()

  vi.mocked(client.downloads).mockResolvedValue([dl({ id: 5, state: 'done', received: 100 })])
  onDownloadsChanged({ id: 5, state: 'done' })
  await vi.waitFor(() => expect(useStore.getState().downloads[0].state).toBe('done'))

  vi.mocked(client.downloads).mockResolvedValue([])
  onDownloadsChanged(undefined) // a clear/remove: neither field
  await vi.waitFor(() => expect(useStore.getState().downloads).toEqual([]))
})

test('downloadPrimaryAction: open when possible, else reveal, none while downloading or deleted', () => {
  expect(downloadPrimaryAction(dl({ state: 'downloading' }))).toBeNull()
  expect(downloadPrimaryAction(dl({ state: 'done', exists: false }))).toBeNull()

  downloadPrimaryAction(dl({ id: 3, state: 'done', exists: true, openable: true }))?.()
  expect(client.openDownload).toHaveBeenCalledWith(3)

  downloadPrimaryAction(dl({ id: 4, state: 'done', exists: true, openable: false }))?.()
  expect(client.revealDownload).toHaveBeenCalledWith(4)
})

test('the panel actions call the matching API methods', () => {
  openDownload(1)
  expect(client.openDownload).toHaveBeenCalledWith(1)
  revealDownload(2)
  expect(client.revealDownload).toHaveBeenCalledWith(2)
  removeDownload(3)
  expect(client.removeDownload).toHaveBeenCalledWith(3)
  clearDownloads()
  expect(client.clearDownloads).toHaveBeenCalledTimes(1)
})
