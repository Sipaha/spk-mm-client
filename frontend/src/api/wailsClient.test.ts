import { Call, Events } from '@wailsio/runtime'
import { afterEach, expect, test, vi } from 'vitest'
import { ApiError, wailsClient } from './client'

const FQN = 'github.com/spk/spk-mm-client/internal/api/transport.API.'

// The module-level @wailsio/runtime mock (frontend/vitest.setup.ts) provides
// bare vi.fn()s for Call.ByName / Events.On; these tests drive them directly
// to exercise the desktop transport (wailsClient), which the http/browser
// tests in client.test.ts don't touch at all.
afterEach(() => {
  vi.mocked(Call.ByName).mockReset()
  vi.mocked(Events.On).mockReset()
})

test('calls are addressed by FQN with positional args', async () => {
  vi.mocked(Call.ByName).mockResolvedValue({ id: 1 })
  await wailsClient.addServer('u')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'AddServer', 'u')

  vi.mocked(Call.ByName).mockResolvedValue({ id: 1, signed_in: true })
  await wailsClient.loginWithPassword(1, 'alice', 'secret')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'LoginWithPassword', 1, 'alice', 'secret')
})

test('a rejection is parsed into ApiError', async () => {
  vi.mocked(Call.ByName).mockRejectedValue(new Error('bad_credentials: HTTP 401'))
  await expect(wailsClient.listServers()).rejects.toEqual(new ApiError('bad_credentials', 'HTTP 401'))
})

test('downloads calls are addressed by FQN', async () => {
  vi.mocked(Call.ByName).mockResolvedValue([])
  await wailsClient.downloads()
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'Downloads')

  vi.mocked(Call.ByName).mockResolvedValue(true)
  await wailsClient.openDownload(7)
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'OpenDownload', 7)

  vi.mocked(Call.ByName).mockResolvedValue(undefined)
  await wailsClient.revealDownload(7)
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'RevealDownload', 7)
  await wailsClient.removeDownload(7)
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'RemoveDownload', 7)
  await wailsClient.clearDownloads()
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'ClearDownloads')
})

test('subscribeEvents registers all event types and unwraps both payload shapes', () => {
  const offs = Array.from({ length: 10 }, () => vi.fn())
  let call = 0
  vi.mocked(Events.On).mockImplementation(() => offs[call++])

  const onEvent = vi.fn()
  const unsubscribe = wailsClient.subscribeEvents(onEvent)

  expect(Events.On).toHaveBeenCalledTimes(10)
  const registeredNames = vi.mocked(Events.On).mock.calls.map((c) => c[0])
  expect(registeredNames).toEqual([
    'servers_changed',
    'login_failed',
    'open_external',
    'sidebar_changed',
    'channel_changed',
    'open_channel',
    'downloads_changed',
    'attachments_changed',
    'attachment_refused',
    'thread_changed',
  ])

  const [serversChangedCb, loginFailedCb, openExternalCb] = vi
    .mocked(Events.On)
    .mock.calls.map((c) => c[1] as (ev: { data: unknown }) => void)

  // data = payload directly
  serversChangedCb({ data: { code: 'bad_credentials' } })
  expect(onEvent).toHaveBeenCalledWith({ type: 'servers_changed', payload: { code: 'bad_credentials' } })

  // data = [payload] (1-element array)
  loginFailedCb({ data: [{ code: 'bad_credentials' }] })
  expect(onEvent).toHaveBeenCalledWith({ type: 'login_failed', payload: { code: 'bad_credentials' } })

  // undefined payload
  openExternalCb({ data: undefined })
  expect(onEvent).toHaveBeenCalledWith({ type: 'open_external', payload: undefined })

  unsubscribe()
  for (const off of offs) expect(off).toHaveBeenCalledTimes(1)
})

test('attachment calls are addressed by FQN with positional args', async () => {
  vi.mocked(Call.ByName).mockResolvedValue([])
  await wailsClient.attachments(1, 'c1', '')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'Attachments', 1, 'c1', '')
  await wailsClient.attachments(1, 'c1', 'r1')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'Attachments', 1, 'c1', 'r1')

  vi.mocked(Call.ByName).mockResolvedValue(undefined)
  await wailsClient.removeAttachment(1, 'a1')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'RemoveAttachment', 1, 'a1')
  await wailsClient.retryAttachment(1, 'a1')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'RetryAttachment', 1, 'a1')

  vi.mocked(Call.ByName).mockResolvedValue(2)
  await wailsClient.attachFromClipboard(1, 'c1', '')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'AttachFromClipboard', 1, 'c1', '')
  await wailsClient.pickAttachments(1, 'c1', '')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'PickAttachments', 1, 'c1', '')
})

// Task 4: SendReply/SaveThreadDraft are new bindings, and SendPost's
// signature does not change alongside them.
test('reply and thread-draft calls are addressed by FQN with positional args', async () => {
  vi.mocked(Call.ByName).mockResolvedValue(undefined)
  await wailsClient.sendPost(1, 'c1', 'hi')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'SendPost', 1, 'c1', 'hi', [])
  await wailsClient.sendReply(1, 'c1', 'r1', 're')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'SendReply', 1, 'c1', 'r1', 're', [])
  await wailsClient.sendReply(1, 'c1', 'r1', '', ['a1'])
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'SendReply', 1, 'c1', 'r1', '', ['a1'])
  await wailsClient.saveThreadDraft(1, 'r1', 'draft text')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'SaveThreadDraft', 1, 'r1', 'draft text')
})

test('thread calls are addressed by FQN with positional args', async () => {
  vi.mocked(Call.ByName).mockResolvedValue({ root_id: 'r1', posts: [] })
  await wailsClient.openThread(1, 'c1', 'r1')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'OpenThread', 1, 'c1', 'r1')
  await wailsClient.getThread(1, 'r1')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'GetThread', 1, 'r1')

  vi.mocked(Call.ByName).mockResolvedValue(undefined)
  await wailsClient.closeThread(1)
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'CloseThread', 1)
  await wailsClient.loadOlderReplies(1, 'r1')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'LoadOlderReplies', 1, 'r1')
})

test('autocomplete: an abort cancels the Wails call (its Go context)', async () => {
  let resolve!: (v: unknown) => void
  const cancel = vi.fn()
  const p = Object.assign(new Promise((r) => (resolve = r)), { cancel })
  vi.mocked(Call.ByName).mockReturnValue(p as never)
  const ctl = new AbortController()
  const res = wailsClient.autocomplete(3, 'users', 'c1', '', 'bo', ctl.signal)
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'Autocomplete', 3, 'users', 'c1', '', 'bo')
  ctl.abort()
  expect(cancel).toHaveBeenCalledOnce()
  resolve({ users: [], others: [], channels: [], emoji: [], commands: [] })
  await expect(res).resolves.toBeDefined()

  vi.mocked(Call.ByName).mockResolvedValue(undefined)
  await wailsClient.executeCommand(3, 'c1', 'r1', '/away')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'ExecuteCommand', 3, 'c1', 'r1', '/away')
})

test.each([537.9688720703125, 583.1544799804688, 497.22552490234375, 564.2781982421875, 420])('layout widths reach Go int bindings as integers (%s)', async (width) => {
  vi.mocked(Call.ByName).mockResolvedValue(undefined)
  await wailsClient.setSidebarWidth(width)
  expect(Call.ByName).toHaveBeenLastCalledWith(FQN + 'SetSidebarWidth', Math.round(width))
  await wailsClient.setThreadWidth(width)
  expect(Call.ByName).toHaveBeenLastCalledWith(FQN + 'SetThreadWidth', Math.round(width))
})

test('jumpToPost: an abort cancels the Wails call (its Go context); gap calls by FQN', async () => {
  let resolve!: (v: unknown) => void
  const cancel = vi.fn()
  const p = Object.assign(new Promise((r) => (resolve = r)), { cancel })
  vi.mocked(Call.ByName).mockReturnValue(p as never)
  const ctl = new AbortController()
  const res = wailsClient.jumpToPost(3, 'c1', 'p1', ctl.signal)
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'JumpToPost', 3, 'c1', 'p1')
  ctl.abort()
  expect(cancel).toHaveBeenCalledOnce()
  resolve({ post_id: 'p1', root_id: '', in_feed: true })
  await expect(res).resolves.toEqual({ post_id: 'p1', root_id: '', in_feed: true })

  vi.mocked(Call.ByName).mockRejectedValue(new Error('post_gone: 404'))
  await expect(wailsClient.jumpToPost(3, 'c1', 'p2')).rejects.toMatchObject({ code: 'post_gone', detail: '404' })

  vi.mocked(Call.ByName).mockResolvedValue(undefined)
  await wailsClient.loadNewer(3, 'c1')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'LoadNewer', 3, 'c1')
  await wailsClient.retryRevalidation(3, 'c1')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'RetryRevalidation', 3, 'c1')
})
