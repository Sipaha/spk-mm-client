import { vi } from 'vitest'
import type { AttachmentView, ChannelDTO, DownloadView, PostView, ServerDTO, SidebarDTO, ThreadDTO } from './api/types'
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
    setPostSaved: vi.fn(),
    addReaction: vi.fn().mockResolvedValue(undefined),
    removeReaction: vi.fn().mockResolvedValue(undefined),
    openThread: vi.fn(),
    getThread: vi.fn(),
    closeThread: vi.fn().mockResolvedValue(undefined),
    loadOlderReplies: vi.fn().mockResolvedValue(undefined),
    sendReply: vi.fn().mockResolvedValue(undefined),
    saveThreadDraft: vi.fn().mockResolvedValue(undefined),
    jumpToPost: vi.fn(),
    searchPosts: vi.fn().mockResolvedValue({ hits: [], has_next: false, limit_reached: false }),
    searchSuggest: vi.fn().mockResolvedValue({ users: [], others: [], channels: [], emoji: [], commands: [] }),
    loadNewer: vi.fn().mockResolvedValue(undefined),
    retryRevalidation: vi.fn().mockResolvedValue(undefined),
    openThreadAt: vi.fn(),
    loadThreadFocus: vi.fn().mockResolvedValue(undefined),
    retryThreadRevalidation: vi.fn().mockResolvedValue(undefined),
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
  attachFromClipboard, clearDownloads, closeDownloadsPanel, closeThread, downloadFile, downloadPrimaryAction, editPost,
  loadOlderReplies, loadSidebar, onAttachmentRefused, onAttachmentsChanged, onDownloadsChanged, openChannel, openDownload,
  openDownloadsPanel, openFile, openFromNotification, openThread, pickAttachments, refreshChannel, refreshDownloads, refreshThread,
  react, removeAttachment, removeDownload, resetChat, retryAttachment, revealDownload, revealSavedFile, saveThreadDraft, selectServer, sendReply, setPostSaved, uploadAttachments,
} = await import('./chat')

const post = (over: Partial<PostView> = {}): PostView => ({
  id: 'p1', user_id: 'u-bob', author: 'bob', message: 'hi', create_at: 1, ...over,
})

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
  crt: false, muted: false, gap: { open: false, gen: 0, before_id: '', stale: false }, hist_rev: 0, ...over,
})

const sidebar = (over: Partial<SidebarDTO> = {}): SidebarDTO => ({
  team_id: 't1', selected_channel_id: 'a', teams: [], categories: [], ...over,
})

beforeEach(() => {
  resetChat()
  useStore.setState({ fileSaves: {}, toast: null, lastError: null })
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
  vi.mocked(client.setPostSaved).mockReset().mockResolvedValue(undefined)
  vi.mocked(uploadAttachmentBrowser).mockReset()
  vi.mocked(client.openThread).mockReset()
  vi.mocked(client.getThread).mockReset()
  vi.mocked(client.closeThread).mockReset().mockResolvedValue(undefined)
  vi.mocked(client.loadOlderReplies).mockReset().mockResolvedValue(undefined)
  vi.mocked(client.sendReply).mockReset().mockResolvedValue(undefined)
  vi.mocked(client.saveThreadDraft).mockReset().mockResolvedValue(undefined)
  useStore.setState({
    servers: [srv(1), srv(2)], selectedId: 1, adding: false, sidebar: null, channel: null, lastError: null,
    editingId: null, downloads: [], downloadsOpen: false, attachments: [], attachError: null,
    thread: null, threadAttachments: [], threadAttachError: null, heldChannel: null,
  })
})

const thread = (rootId: string, over: Partial<ThreadDTO> = {}): ThreadDTO => ({
  root_id: rootId, channel_id: 'a', channel_name: 'A', team_name: 'team', posts: [], has_more: false, capped: false,
  loaded: true, syncing: false, root_deleted: false, error: '', draft: '', me_id: 'u-alice', crt: false, new_since: 0, gap_after: '', focus: null, ...over,
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
const save = (key = '1/f-spec') => useStore.getState().fileSaves[key]

test('a download is "saving" on its file at once and "saved" with its path when done — no banner', async () => {
  setLocale('en')
  let done!: (r: { path: string; opened: boolean }) => void
  vi.mocked(client.downloadFile).mockReturnValueOnce(new Promise((r) => (done = r)))
  const saving = downloadFile(1, spec)
  expect(save()).toMatchObject({ state: 'saving' })
  done({ path: '/home/a/Downloads/spec.pdf', opened: false })
  await saving
  expect(save()).toMatchObject({ state: 'saved', path: '/home/a/Downloads/spec.pdf' })
  expect(save().savedAt).toBeGreaterThan(0)
  expect(useStore.getState().toast).toBeNull()
  expect(useStore.getState().lastError).toBeNull()
})

test('a failed download: the file goes back to what it was and the error is a toast, not the layout banner', async () => {
  setLocale('en')
  useStore.getState().dismissToast()
  vi.mocked(client.downloadFile).mockRejectedValueOnce(new Error('boom'))
  await downloadFile(1, { id: 'f-new', name: 'new.bin' })
  expect(save('1/f-new')).toBeUndefined()
  expect(useStore.getState().toast).toMatchObject({ tone: 'error' })
  expect(useStore.getState().toast!.text).toContain('new.bin')
  expect(useStore.getState().lastError).toBeNull()

  vi.mocked(client.downloadFile).mockResolvedValueOnce({ path: '/d/spec.pdf', opened: false })
  await downloadFile(1, spec)
  vi.mocked(client.downloadFile).mockRejectedValueOnce(new Error('boom'))
  await downloadFile(1, spec)
  expect(save()).toMatchObject({ state: 'saved', path: '/d/spec.pdf', savedAt: 0 }) // the earlier copy is still there; no fresh ✓
})

test('a second click while the file downloads keeps the earlier copy\'s "Show in folder"; failing twice keeps it saved', async () => {
  setLocale('en')
  vi.mocked(client.downloadFile).mockResolvedValueOnce({ path: '/d/spec.pdf', opened: false })
  await downloadFile(1, spec)
  let failFirst!: (e: unknown) => void
  let failSecond!: (e: unknown) => void
  vi.mocked(client.downloadFile)
    .mockReturnValueOnce(new Promise((_, r) => (failFirst = r)))
    .mockReturnValueOnce(new Promise((_, r) => (failSecond = r)))
  const a = downloadFile(1, spec)
  const b = downloadFile(1, spec) // joins the running download (Go's shared)
  expect(save()).toMatchObject({ state: 'saving', path: '/d/spec.pdf' })
  failFirst(new Error('boom'))
  failSecond(new Error('boom'))
  await a
  await b
  expect(save()).toMatchObject({ state: 'saved', path: '/d/spec.pdf', savedAt: 0 })
})

test('a finished download is announced for screen readers', async () => {
  setLocale('en')
  vi.mocked(client.downloadFile).mockResolvedValueOnce({ path: '/d/spec.pdf', opened: false })
  await downloadFile(1, spec)
  expect(useStore.getState().announcement?.text).toBe('File saved: spec.pdf')
})

test('a failed downloads-list refresh is a toast, not the layout banner', async () => {
  vi.mocked(client.downloads).mockRejectedValueOnce(new Error('db'))
  await refreshDownloads()
  expect(useStore.getState().toast).toMatchObject({ tone: 'error' })
  expect(useStore.getState().lastError).toBeNull()
})

test('open: an opened file is saved quietly; one not opened says why in a toast', async () => {
  setLocale('en')
  useStore.getState().dismissToast()
  vi.mocked(client.openFile).mockResolvedValueOnce({ path: '/d/a.log', opened: true })
  await openFile(1, { id: 'f-log', name: 'a.log' })
  expect(save('1/f-log')).toMatchObject({ state: 'saved', path: '/d/a.log' })
  expect(useStore.getState().toast).toBeNull()
  vi.mocked(client.openFile).mockResolvedValueOnce({ path: '/d/run.desktop', opened: false })
  await openFile(1, { id: 'f-x', name: 'run.desktop' })
  expect(useStore.getState().toast).toMatchObject({ tone: 'info', text: 'Saved to /d/run.desktop. Files of this type are not opened automatically.' })
})

test('"show in folder" of a saved file resolves its downloads-list entry by path', async () => {
  vi.mocked(client.downloads).mockResolvedValue([dl({ id: 9, path: '/d/spec.pdf' }), dl({ id: 10, path: '/d/other.txt' })])
  await revealSavedFile('/d/spec.pdf')
  expect(client.revealDownload).toHaveBeenCalledWith(9)
})

test('"show in folder" with no entry for the saved path (e.g. the SQLite insert failed) says so in a toast', async () => {
  setLocale('en')
  useStore.getState().dismissToast()
  vi.mocked(client.revealDownload).mockClear()
  vi.mocked(client.downloads).mockResolvedValue([dl({ id: 10, path: '/d/other.txt' })])
  await revealSavedFile('/d/spec.pdf')
  expect(useStore.getState().toast).toMatchObject({ tone: 'error', text: '/d/spec.pdf is not in the downloads list — it cannot be shown in its folder' })
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
  expect(client.attachments).toHaveBeenCalledWith(1, 'a', '')
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

// Task 4/6: attachments_changed/attachment_refused carry root_id; a
// thread's (root_id set) is ignored while no matching thread is open (it
// cannot clobber the channel composer's own state), and applied to the
// thread* store fields once that thread is the one in the panel.
test('a thread-scoped attachments_changed/attachment_refused (root_id set) is ignored without a matching open thread', async () => {
  setLocale('en')
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  vi.mocked(client.attachments).mockResolvedValue([av('x')])
  await openChannel(1, 'a')
  await vi.waitFor(() => expect(useStore.getState().attachments).toEqual([av('x')]))

  onAttachmentsChanged({ server_id: 1, channel_id: 'a', root_id: 'r1', items: [av('thread-only')] })
  expect(useStore.getState().attachments).toEqual([av('x')])
  expect(useStore.getState().threadAttachments).toEqual([])
  onAttachmentRefused({ server_id: 1, channel_id: 'a', root_id: 'r1', code: 'too_many' })
  expect(useStore.getState().attachError).toBeNull()
  expect(useStore.getState().threadAttachError).toBeNull()

  // root_id '' (the channel's own) still applies, as before.
  onAttachmentsChanged({ server_id: 1, channel_id: 'a', root_id: '', items: [av('channel-only')] })
  expect(useStore.getState().attachments).toEqual([av('channel-only')])
})

test('a thread-scoped attachments_changed/attachment_refused applies once that thread is open', async () => {
  setLocale('en')
  useStore.getState().setChannel(1, chan('a'))
  useStore.getState().setThread(thread('r1'))

  onAttachmentsChanged({ server_id: 1, channel_id: 'a', root_id: 'r1', items: [av('thread-item')] })
  expect(useStore.getState().threadAttachments).toEqual([av('thread-item')])
  expect(useStore.getState().attachments).toEqual([]) // the channel's own tray is untouched

  onAttachmentRefused({ server_id: 1, channel_id: 'a', root_id: 'r1', code: 'too_many' })
  expect(useStore.getState().threadAttachError).toBe('Too many attachments (10 at most)')
  expect(useStore.getState().attachError).toBeNull()

  // A different thread's event (not the one open) is ignored.
  onAttachmentsChanged({ server_id: 1, channel_id: 'a', root_id: 'other', items: [av('nope')] })
  expect(useStore.getState().threadAttachments).toEqual([av('thread-item')])
})

// --- Thread panel (Task 6) ------------------------------------------------

test('openThread opens a thread in the panel and fetches its tray', async () => {
  useStore.getState().setChannel(1, chan('a'))
  vi.mocked(client.openThread).mockResolvedValue(thread('r1'))
  vi.mocked(client.attachments).mockResolvedValue([av('x')])
  await openThread(1, 'a', 'r1')
  expect(client.openThread).toHaveBeenCalledWith(1, 'a', 'r1')
  expect(useStore.getState().thread).toEqual(thread('r1'))
  await vi.waitFor(() => expect(useStore.getState().threadAttachments).toEqual([av('x')]))
  expect(client.attachments).toHaveBeenCalledWith(1, 'a', 'r1')
})

test('opening a different thread (or navigating away) drops a stale openThread reply', async () => {
  useStore.getState().setChannel(1, chan('a'))
  const stale = deferred<ThreadDTO>()
  vi.mocked(client.openThread).mockReturnValueOnce(stale.p).mockResolvedValueOnce(thread('r2'))
  const first = openThread(1, 'a', 'r1')
  await openThread(1, 'a', 'r2') // the user opened a different thread before "r1" resolved
  stale.resolve(thread('r1'))
  await first
  expect(useStore.getState().thread).toEqual(thread('r2')) // "r1"'s late reply must not win
})

test('closeThread closes the panel and clears its store fields, but only for the server it was open on', async () => {
  useStore.getState().setChannel(1, chan('a'))
  vi.mocked(client.openThread).mockResolvedValue(thread('r1'))
  await openThread(1, 'a', 'r1')
  useStore.getState().setThreadAttachments([av('x')])
  useStore.getState().setThreadAttachError('boom')

  closeThread(2) // a no-op: nothing is open for server 2
  expect(client.closeThread).not.toHaveBeenCalled()
  expect(useStore.getState().thread).not.toBeNull()

  closeThread(1)
  expect(client.closeThread).toHaveBeenCalledWith(1)
  expect(useStore.getState().thread).toBeNull()
  expect(useStore.getState().threadAttachments).toEqual([])
  expect(useStore.getState().threadAttachError).toBeNull()
})

test('refreshThread re-reads the open thread; a reply for a thread no longer open is dropped', async () => {
  useStore.getState().setChannel(1, chan('a'))
  vi.mocked(client.openThread).mockResolvedValue(thread('r1'))
  await openThread(1, 'a', 'r1')

  vi.mocked(client.getThread).mockResolvedValue(thread('r1', { syncing: false, posts: [post({ id: 'p2' })] }))
  await refreshThread(1, 'r1')
  expect(useStore.getState().thread?.posts).toEqual([post({ id: 'p2' })])

  const stale = deferred<ThreadDTO>()
  vi.mocked(client.getThread).mockReturnValueOnce(stale.p)
  const r = refreshThread(1, 'r1')
  closeThread(1) // the panel closed before the refresh landed
  stale.resolve(thread('r1', { posts: [post({ id: 'late' })] }))
  await r
  expect(useStore.getState().thread).toBeNull() // must not resurrect the closed panel
})

test('loadOlderReplies fetches a page then re-reads the thread, like loadOlder', async () => {
  useStore.getState().setChannel(1, chan('a'))
  vi.mocked(client.openThread).mockResolvedValue(thread('r1'))
  await openThread(1, 'a', 'r1')
  vi.mocked(client.getThread).mockResolvedValue(thread('r1', { has_more: false }))
  const ok = await loadOlderReplies(1, 'r1')
  expect(client.loadOlderReplies).toHaveBeenCalledWith(1, 'r1')
  expect(client.getThread).toHaveBeenCalledWith(1, 'r1')
  expect(ok).toBe(true)

  vi.mocked(client.loadOlderReplies).mockRejectedValueOnce(new Error('boom'))
  expect(await loadOlderReplies(1, 'r1')).toBe(false)
})

test('sendReply/saveThreadDraft call the matching API methods', () => {
  void sendReply(1, 'a', 'r1', 'hello', ['att1'])
  expect(client.sendReply).toHaveBeenCalledWith(1, 'a', 'r1', 'hello', ['att1'])
  saveThreadDraft(1, 'r1', 'draft text')
  expect(client.saveThreadDraft).toHaveBeenCalledWith(1, 'r1', 'draft text')
})

test('openFromNotification opens the channel, then the thread, when a root_id is given', async () => {
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  vi.mocked(client.openThread).mockResolvedValue(thread('r1'))
  await openFromNotification(1, 'a', 'r1')
  expect(useStore.getState().channel?.id).toBe('a')
  expect(client.openThread).toHaveBeenCalledWith(1, 'a', 'r1')
  await vi.waitFor(() => expect(useStore.getState().thread).toEqual(thread('r1')))
})

// heldChannel (sidebar-sections-brief.md, fix round 1): captured centrally
// in openChannel — the single gateway every switch funnels through — so
// every origin (a Sidebar row click, a notification, a ~channel link) is
// just a call to openChannel/openFromNotification under the hood. The
// "~channel link" origin (Markdown.tsx's ChannelMention, which also calls
// openChannel directly) is covered at the component level in
// Markdown.test.tsx, not duplicated here.
const withUnreadA = (over: Partial<{ mentions: number }> = {}) =>
  sidebar({
    categories: [{
      id: 'c1', type: 'channels', name: 'Channels', collapsed: false,
      channels: [{ id: 'a', name: 'A', type: 'O', unread: true, mentions: 0, muted: false, ...over }],
    }],
  })

test('opening an unread channel (a Sidebar row click) holds it, remembering whether it had a mention', async () => {
  useStore.setState({ sidebar: withUnreadA({ mentions: 2 }) })
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  await openChannel(1, 'a')
  expect(useStore.getState().heldChannel).toEqual({ id: 'a', hadMentions: true })
})

test('opening an already-read channel clears any previously held one', async () => {
  useStore.setState({ heldChannel: { id: 'x', hadMentions: true }, sidebar: withUnreadA() })
  vi.mocked(client.openChannel).mockResolvedValue(chan('b'))
  await openChannel(1, 'b') // 'b' isn't in the sidebar fixture at all → not unread
  expect(useStore.getState().heldChannel).toBeNull()
})

test('openFromNotification captures the held channel exactly like a direct openChannel call', async () => {
  useStore.setState({ sidebar: withUnreadA() })
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  await openFromNotification(1, 'a')
  expect(useStore.getState().heldChannel).toEqual({ id: 'a', hadMentions: false })
})

test('opening a channel on a server that is not yet selected holds nothing — its sidebar is not loaded yet', async () => {
  // selectedId is 1 (beforeEach); `sidebar` here would belong to server 1,
  // so a switch to server 2 must not read it as if it were server 2's.
  useStore.setState({ sidebar: withUnreadA({ mentions: 1 }) })
  vi.mocked(client.openChannel).mockResolvedValue(chan('a'))
  await openChannel(2, 'a')
  expect(useStore.getState().heldChannel).toBeNull()
})

test('opening a channel closes a thread open for that server; opening a different server closes its thread too', async () => {
  useStore.getState().setChannel(1, chan('a'))
  vi.mocked(client.openThread).mockResolvedValue(thread('r1'))
  await openThread(1, 'a', 'r1')

  vi.mocked(client.openChannel).mockResolvedValue(chan('b'))
  await openChannel(1, 'b') // navigating to another channel takes the panel down with it
  expect(client.closeThread).toHaveBeenCalledWith(1)
  expect(useStore.getState().thread).toBeNull()

  vi.mocked(client.closeThread).mockClear()
  useStore.getState().setChannel(1, chan('a'))
  await openThread(1, 'a', 'r1')
  selectServer(2) // switching servers closes server 1's thread too
  expect(client.closeThread).toHaveBeenCalledWith(1)
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

// UI (Task 3 brief): the save button's state comes only from post.saved (no
// optimistic local flip to roll back), so a failure just needs the same
// global error report every other one-off post action (markUnread,
// deletePost, removeAttachment) already gets — never a "stuck" button.
test('setPostSaved calls the API and reports a failure globally, like markUnread', async () => {
  setPostSaved(1, post({ id: 'p1' }), true)
  expect(client.setPostSaved).toHaveBeenCalledWith(1, 'p1', true)

  vi.mocked(client.setPostSaved).mockRejectedValueOnce(new Error('boom'))
  setPostSaved(1, post({ id: 'p1' }), false)
  expect(client.setPostSaved).toHaveBeenCalledWith(1, 'p1', false)
  await vi.waitFor(() => expect(useStore.getState().lastError).not.toBeNull())
})

// react's returned promise always resolves — never rejects — once the call
// has settled either way: PostItem's quick-reactions cache invalidation
// (emoji/recent.ts) waits for it to settle before marking the cache stale,
// so it must never be left hanging by a rejection (final-review finding,
// UI pass 2026-09-28).
test('react calls addReaction/removeReaction, reports a failure globally, and its promise always resolves', async () => {
  await react(1, 'p1', 'tada', true)
  expect(client.addReaction).toHaveBeenCalledWith(1, 'p1', 'tada')
  await react(1, 'p1', 'tada', false)
  expect(client.removeReaction).toHaveBeenCalledWith(1, 'p1', 'tada')
  expect(useStore.getState().lastError).toBeNull()

  vi.mocked(client.addReaction).mockRejectedValueOnce(new Error('boom'))
  await expect(react(1, 'p1', 'fire', true)).resolves.toBeUndefined() // settles, does not reject
  await vi.waitFor(() => expect(useStore.getState().lastError).not.toBeNull())
})

test('pickAttachments/attachFromClipboard are not caught here — the caller shows the error inline', async () => {
  vi.mocked(client.pickAttachments).mockResolvedValue(2)
  await expect(pickAttachments(1, 'a', '')).resolves.toBe(2)
  expect(client.pickAttachments).toHaveBeenCalledWith(1, 'a', '')

  vi.mocked(client.attachFromClipboard).mockRejectedValue(new Error('nope'))
  await expect(attachFromClipboard(1, 'a', '')).rejects.toThrow('nope')
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
  await expect(uploadAttachments(1, 'a', [good1, bad, good2], '')).rejects.toThrow('too big')
  expect(uploadAttachmentBrowser).toHaveBeenCalledTimes(3)
  expect(uploadAttachmentBrowser).toHaveBeenNthCalledWith(1, 1, 'a', good1, '')
  expect(uploadAttachmentBrowser).toHaveBeenNthCalledWith(2, 1, 'a', bad, '')
  expect(uploadAttachmentBrowser).toHaveBeenNthCalledWith(3, 1, 'a', good2, '')
})
