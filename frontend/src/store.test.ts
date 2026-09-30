import { vi } from 'vitest'
import type { DownloadView, ServerDTO } from './api/types'
import { forgetRecent } from './emoji/recent'
import { FILE_SAVES_CAP, useStore } from './store'

vi.mock('./emoji/recent', () => ({ forgetRecent: vi.fn() }))

const srv = (o: Partial<ServerDTO> = {}): ServerDTO => ({
  id: 1, name: 'A', url: 'https://a', signed_in: true, username: 'alice', gitlab: false,
  state: 'live', unread: false, mentions: 0, ...o,
})

beforeEach(() =>
  useStore.setState({
    servers: [], selectedId: null, adding: false, lastError: null, signInFor: null, sidebar: null, channel: null, heldChannel: null,
  }),
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
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'me', crt: false, muted: false, gap: { open: false, gen: 0, before_id: '', stale: false }, hist_rev: 0,
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

const av = (id: string): import('./api/types').AttachmentView => ({ id, name: id + '.png', size: 3, mime: 'image/png', state: 'staged', sent: 0, error: '' })

test('switching to a different channel drops the composer tray and its error; refreshing the same one keeps it (chat.ts refetches right after)', () => {
  useStore.getState().setServers([srv()])
  useStore.getState().setChannel(1, chan('a'))
  useStore.getState().setAttachments([av('x')])
  useStore.getState().setAttachError('boom')
  useStore.getState().setChannel(1, chan('a'))
  expect(useStore.getState().attachments).toEqual([av('x')])
  expect(useStore.getState().attachError).toBe('boom')
  useStore.getState().setChannel(1, chan('b'))
  expect(useStore.getState().attachments).toEqual([])
  expect(useStore.getState().attachError).toBeNull()
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

test('a server dropped from the list has its quick-reactions cache forgotten (emoji/recent.ts)', () => {
  vi.mocked(forgetRecent).mockClear() // the mock is shared across this file's tests
  useStore.getState().setServers([srv({ id: 1 }), srv({ id: 2 })])
  expect(forgetRecent).not.toHaveBeenCalled()
  useStore.getState().setServers([srv({ id: 1 })]) // server 2 removed
  expect(forgetRecent).toHaveBeenCalledTimes(1)
  expect(forgetRecent).toHaveBeenCalledWith(2)
  useStore.getState().setServers([srv({ id: 1 })]) // no change: not called again
  expect(forgetRecent).toHaveBeenCalledTimes(1)
})

const dl = (over: Partial<DownloadView> = {}): DownloadView => ({
  id: 1, server_id: 1, file_id: 'f1', name: 'a.txt', path: '/d/a.txt', size: 10, mime: 'text/plain',
  started_at: 0, finished_at: 1, state: 'done', error: '', received: 10, exists: true, openable: true, ...over,
})

test('fileSaves: set and cleared per file key; the oldest entries go past the session cap', () => {
  const st = () => useStore.getState()
  st().setFileSave('1/a', { state: 'saving', path: '', savedAt: 0 })
  st().setFileSave('1/a', { state: 'saved', path: '/d/a', savedAt: 5 })
  expect(st().fileSaves['1/a']).toEqual({ state: 'saved', path: '/d/a', savedAt: 5 })
  st().setFileSave('1/a', null)
  expect(st().fileSaves['1/a']).toBeUndefined()
  for (let i = 0; i < FILE_SAVES_CAP + 5; i++) st().setFileSave(`1/f${i}`, { state: 'saved', path: `/d/${i}`, savedAt: i })
  expect(Object.keys(st().fileSaves)).toHaveLength(FILE_SAVES_CAP)
  expect(st().fileSaves['1/f0']).toBeUndefined()
  st().setFileSave('1/f5', { state: 'saved', path: '/d/5', savedAt: 99 }) // touched: now the newest
  st().setFileSave('1/g', { state: 'saved', path: '/d/g', savedAt: 100 })
  expect(st().fileSaves['1/f5']).toBeDefined()
  expect(st().fileSaves['1/f6']).toBeUndefined()
})

test('toast: showing replaces the current one; dismissing an older id leaves a newer toast alone', () => {
  const st = () => useStore.getState()
  st().showToast('first')
  const first = st().toast!
  st().showToast('second', 'info')
  expect(st().toast).toMatchObject({ text: 'second', tone: 'info' })
  st().dismissToast(first.id)
  expect(st().toast?.text).toBe('second')
  st().dismissToast()
  expect(st().toast).toBeNull()
})

test('the downloads list is dropped once the panel closes and nothing is active', () => {
  useStore.setState({ downloads: [], downloadsOpen: false })
  useStore.getState().setDownloads([dl()])
  expect(useStore.getState().downloads).toEqual([]) // panel closed, nothing active: not held

  useStore.getState().setDownloadsOpen(true)
  useStore.getState().setDownloads([dl()])
  expect(useStore.getState().downloads).toHaveLength(1) // panel open: held

  useStore.getState().setDownloadsOpen(false)
  expect(useStore.getState().downloads).toEqual([]) // closing with nothing active drops it

  useStore.getState().setDownloads([dl({ state: 'downloading', received: 3 })])
  useStore.getState().setDownloadsOpen(false) // an active download keeps the list even when the panel is closed
  expect(useStore.getState().downloads).toHaveLength(1)

  useStore.getState().patchDownloadProgress(1, 7)
  expect(useStore.getState().downloads[0].received).toBe(7)

  useStore.getState().setDownloads([dl({ state: 'done' })]) // it finished: no longer active, panel still closed
  expect(useStore.getState().downloads).toEqual([])
})

test('patchDownloadProgress for an id not in the store is inert: no crash, no phantom row', () => {
  useStore.setState({ downloads: [dl({ id: 1 })], downloadsOpen: true })
  useStore.getState().patchDownloadProgress(999, 42)
  expect(useStore.getState().downloads).toEqual([dl({ id: 1 })]) // unchanged
  expect(useStore.getState().downloads).toHaveLength(1)
})

const thread = (rootId: string): import('./api/types').ThreadDTO => ({
  root_id: rootId, channel_id: 'a', channel_name: 'a', team_name: 'team', posts: [], has_more: false, capped: false,
  loaded: true, syncing: false, root_deleted: false, error: '', draft: '', me_id: 'me', crt: false, new_since: 0, gap_after: '',
})

// Task 6: switching the channel closes the thread panel (chat.ts also
// closes it on the Go side) — same rule as attachments above.
test('switching to a different channel drops the open thread and its tray; refreshing the same one keeps it', () => {
  useStore.getState().setServers([srv()])
  useStore.getState().setChannel(1, chan('a'))
  useStore.getState().setThread(thread('r1'))
  useStore.getState().setThreadAttachments([av('x')])
  useStore.getState().setThreadAttachError('boom')
  useStore.getState().setChannel(1, chan('a')) // a content refresh of the still-open channel
  expect(useStore.getState().thread).toEqual(thread('r1'))
  expect(useStore.getState().threadAttachments).toEqual([av('x')])
  expect(useStore.getState().threadAttachError).toBe('boom')
  useStore.getState().setChannel(1, chan('b')) // the user actually switched channels
  expect(useStore.getState().thread).toBeNull()
  expect(useStore.getState().threadAttachments).toEqual([])
  expect(useStore.getState().threadAttachError).toBeNull()
})

test('switching servers (select) clears the thread too', () => {
  useStore.getState().setServers([srv(), srv({ id: 2 })])
  useStore.getState().setChannel(1, chan('a'))
  useStore.getState().setThread(thread('r1'))
  useStore.getState().select(2)
  expect(useStore.getState().thread).toBeNull()
})

// heldChannel (sidebar-sections-brief.md fix round 1): captured centrally in
// chat.ts's openChannel, not owned by Sidebar.tsx, so it must reset the same
// way sidebar/channel/thread already do — via `cleared`, on every server
// switch (select) and sign-out (setServers dropping a signed-out selection).
test('switching servers (select) clears the held channel too', () => {
  useStore.getState().setServers([srv(), srv({ id: 2 })])
  useStore.getState().setHeldChannel({ id: 'a', hadMentions: true })
  useStore.getState().select(2)
  expect(useStore.getState().heldChannel).toBeNull()
})
