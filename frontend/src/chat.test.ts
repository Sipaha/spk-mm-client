import { vi } from 'vitest'
import type { AttachmentView, ChannelDTO, DownloadView, ServerDTO, SidebarDTO } from './api/types'
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
    attachments: vi.fn(),
    removeAttachment: vi.fn(),
    retryAttachment: vi.fn(),
    pickAttachments: vi.fn(),
    attachFromClipboard: vi.fn(),
  },
  uploadAttachmentBrowser: vi.fn(),
  ApiError: class ApiError extends Error {
    constructor(public code: string, public detail: string) {
      super(detail ? `${code}: ${detail}` : code)
    }
  },
}))

const { client, uploadAttachmentBrowser } = await import('./api/client')
const {
  attachFromClipboard, clearDownloads, closeDownloadsPanel, downloadFile, downloadPrimaryAction, editPost,
  loadSidebar, onAttachmentRefused, onAttachmentsChanged, onDownloadsChanged, openChannel, openDownload,
  openDownloadsPanel, openFile, pickAttachments, refreshChannel, refreshDownloads,
  removeAttachment, removeDownload, resetChat, retryAttachment, revealDownload, selectServer, uploadAttachments,
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
  vi.mocked(client.attachments).mockReset().mockResolvedValue([])
  vi.mocked(client.removeAttachment).mockReset().mockResolvedValue(undefined)
  vi.mocked(client.retryAttachment).mockReset().mockResolvedValue(undefined)
  vi.mocked(client.pickAttachments).mockReset()
  vi.mocked(client.attachFromClipboard).mockReset()
  vi.mocked(uploadAttachmentBrowser).mockReset()
  useStore.setState({
    servers: [srv(1), srv(2)], selectedId: 1, adding: false, sidebar: null, channel: null, lastError: null,
    editingId: null, downloads: [], downloadsOpen: false, attachments: [], attachError: null,
  })
})

const av = (id: string, over: Partial<AttachmentView> = {}): AttachmentView => ({
  id, name: id + '.png', size: 3, mime: 'image/png', state: 'staged', sent: 0, error: '', ...over,
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

test('"show in folder" reports an error instead of doing nothing when no entry matches the saved path (e.g. the SQLite insert failed)', async () => {
  setLocale('en')
  vi.mocked(client.downloadFile).mockResolvedValue({ path: '/d/spec.pdf', opened: false })
  await downloadFile(1, spec)
  const action = useStore.getState().noticeAction

  useStore.getState().setError(null)
  vi.mocked(client.downloads).mockResolvedValue([dl({ id: 10, path: '/d/other.txt' })]) // no entry for spec.pdf
  action?.onClick()
  await vi.waitFor(() => expect(useStore.getState().lastError).not.toBeNull())
  expect(client.revealDownload).not.toHaveBeenCalled()
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

test('downloads panel: every open reloads the list (the store drops it again on close)', async () => {
  vi.mocked(client.downloads).mockResolvedValue([dl()])
  openDownloadsPanel()
  await vi.waitFor(() => expect(useStore.getState().downloads).toHaveLength(1))
  expect(client.downloads).toHaveBeenCalledTimes(1)

  closeDownloadsPanel()
  expect(useStore.getState().downloadsOpen).toBe(false)
  expect(useStore.getState().downloads).toEqual([]) // nothing active: dropped on close

  // Task 6 e2e regression: a fast download can finish (and its
  // downloads_changed event refresh) while the panel was never opened —
  // setDownloads drops that result right back to [] (nothing active, panel
  // closed). The *next* open must still fetch again, not trust a stale
  // "already loaded once" flag — otherwise the panel is stuck empty even
  // though Downloads() has entries.
  openDownloadsPanel()
  await vi.waitFor(() => expect(useStore.getState().downloads).toHaveLength(1))
  expect(client.downloads).toHaveBeenCalledTimes(2)
})

test('a downloads_changed event while the panel is closed does not stop the next open from fetching', async () => {
  vi.mocked(client.downloads).mockResolvedValue([])
  onDownloadsChanged({}) // e.g. a download finished while the panel was never open
  await vi.waitFor(() => expect(client.downloads).toHaveBeenCalledTimes(1))
  expect(useStore.getState().downloads).toEqual([]) // dropped: not open, nothing active

  vi.mocked(client.downloads).mockResolvedValue([dl()])
  openDownloadsPanel()
  await vi.waitFor(() => expect(useStore.getState().downloads).toHaveLength(1))
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

// --- Attachments ---------------------------------------------------------

test('opening a channel fetches its composer tray', async () => {
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  vi.mocked(client.attachments).mockResolvedValue([av('x')])
  await openChannel(1, 'a')
  await vi.waitFor(() => expect(useStore.getState().attachments).toEqual([av('x')]))
  expect(client.attachments).toHaveBeenCalledWith(1, 'a')
})

test('a stale attachments fetch (channel switched away while it was in flight) is dropped', async () => {
  vi.mocked(client.openChannel).mockResolvedValueOnce(chan('a')).mockResolvedValueOnce(chan('b'))
  const stale = deferred<AttachmentView[]>()
  vi.mocked(client.attachments).mockReturnValueOnce(stale.p).mockResolvedValue([av('y')])
  await openChannel(1, 'a')
  await openChannel(1, 'b') // switches away before "a"'s attachments resolve
  await vi.waitFor(() => expect(useStore.getState().attachments).toEqual([av('y')]))
  stale.resolve([av('x')])
  await Promise.resolve()
  expect(useStore.getState().attachments).toEqual([av('y')]) // the stale fetch for "a" must not land
})

test('onAttachmentsChanged applies only to the channel on screen', async () => {
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  await openChannel(1, 'a')
  await vi.waitFor(() => expect(client.attachments).toHaveBeenCalled())

  onAttachmentsChanged({ server_id: 2, channel_id: 'a', items: [av('other-server')] })
  expect(useStore.getState().attachments).toEqual([])
  onAttachmentsChanged({ server_id: 1, channel_id: 'b', items: [av('other-channel')] })
  expect(useStore.getState().attachments).toEqual([])
  onAttachmentsChanged({ server_id: 1, channel_id: 'a', items: [av('mine')] })
  expect(useStore.getState().attachments).toEqual([av('mine')])
})

test('an attachments_changed event beats a slower refreshAttachments reply requested before it', async () => {
  vi.mocked(client.openChannel).mockResolvedValueOnce(chan('a'))
  const stale = deferred<AttachmentView[]>()
  vi.mocked(client.attachments).mockReturnValueOnce(stale.p)
  await openChannel(1, 'a') // fires refreshAttachments; its reply is still in flight
  await vi.waitFor(() => expect(client.attachments).toHaveBeenCalled())
  onAttachmentsChanged({ server_id: 1, channel_id: 'a', items: [av('newer')] })
  expect(useStore.getState().attachments).toEqual([av('newer')])
  stale.resolve([av('older')]) // the request made before the event resolves after it
  await Promise.resolve()
  expect(useStore.getState().attachments).toEqual([av('newer')]) // must not be overwritten by the stale reply
})

test('onAttachmentRefused shows a localized message in the composer, only for the channel on screen', async () => {
  setLocale('en')
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  await openChannel(1, 'a')

  onAttachmentRefused({ server_id: 1, channel_id: 'b', code: 'too_many' })
  expect(useStore.getState().attachError).toBeNull()
  onAttachmentRefused({ server_id: 1, channel_id: 'a', code: 'too_many' })
  expect(useStore.getState().attachError).toBe('Too many attachments (10 at most)')
})

test('removeAttachment/retryAttachment call the API and report a failure globally', async () => {
  removeAttachment(1, 'a1')
  expect(client.removeAttachment).toHaveBeenCalledWith(1, 'a1')
  retryAttachment(1, 'a1')
  expect(client.retryAttachment).toHaveBeenCalledWith(1, 'a1')

  vi.mocked(client.removeAttachment).mockRejectedValueOnce(new Error('boom'))
  await removeAttachment(1, 'a1')
  await vi.waitFor(() => expect(useStore.getState().lastError).not.toBeNull())
})

test('pickAttachments/attachFromClipboard are not caught here — the caller shows the error inline', async () => {
  vi.mocked(client.pickAttachments).mockResolvedValue(2)
  await expect(pickAttachments(1, 'a')).resolves.toBe(2)
  expect(client.pickAttachments).toHaveBeenCalledWith(1, 'a')

  vi.mocked(client.attachFromClipboard).mockRejectedValue(new Error('nope'))
  await expect(attachFromClipboard(1, 'a')).rejects.toThrow('nope')
  expect(useStore.getState().lastError).toBeNull() // not turned into a global error by chat.ts
})

test('uploadAttachments tries every file even if one fails, then throws the first failure', async () => {
  const good1 = new File(['a'], 'a.txt')
  const bad = new File(['b'], 'b.txt')
  const good2 = new File(['c'], 'c.txt')
  vi.mocked(uploadAttachmentBrowser)
    .mockResolvedValueOnce(av('a'))
    .mockRejectedValueOnce(new Error('too big'))
    .mockResolvedValueOnce(av('c'))
  await expect(uploadAttachments(1, 'a', [good1, bad, good2])).rejects.toThrow('too big')
  expect(uploadAttachmentBrowser).toHaveBeenCalledTimes(3)
  expect(uploadAttachmentBrowser).toHaveBeenNthCalledWith(1, 1, 'a', good1)
  expect(uploadAttachmentBrowser).toHaveBeenNthCalledWith(2, 1, 'a', bad)
  expect(uploadAttachmentBrowser).toHaveBeenNthCalledWith(3, 1, 'a', good2)
})
