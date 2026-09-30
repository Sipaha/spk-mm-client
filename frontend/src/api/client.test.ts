import { afterEach, vi } from 'vitest'
import { ApiError, httpClient, isDesktopLocation, parseWailsError, uploadAttachmentBrowser } from './client'

afterEach(() => {
  vi.restoreAllMocks()
  document.head.innerHTML = ''
})

test('http client sends bearer token from meta tag and JSON body', async () => {
  document.head.innerHTML = '<meta name="spk-mm-client-api-token" content="tok123">'
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ id: 1, name: 'A', url: 'u', signed_in: false, username: '', gitlab: true }), {
      headers: { 'content-type': 'application/json' },
    }),
  )
  const s = await httpClient.addServer('https://mm')
  expect(s.name).toBe('A')
  const [path, init] = fetchMock.mock.calls[0]
  expect(path).toBe('/api/AddServer')
  expect((init!.headers as Record<string, string>).Authorization).toBe('Bearer tok123')
  expect(JSON.parse(init!.body as string)).toEqual({ url: 'https://mm' })
})

test('http client turns 400 {code} into ApiError', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ code: 'invalid_url', detail: 'x' }), {
      status: 400,
      headers: { 'content-type': 'application/json' },
    }),
  )
  await expect(httpClient.addServer('bad')).rejects.toEqual(new ApiError('invalid_url', 'x'))
})

test('wails error text "<code>: <detail>" parses into ApiError', () => {
  expect(parseWailsError(new Error('bad_credentials: mattermost: HTTP 401'))).toEqual(
    new ApiError('bad_credentials', 'mattermost: HTTP 401'),
  )
  expect(parseWailsError({ message: 'not_found' })).toEqual(new ApiError('not_found', ''))
})

test.each([
  ['wails://localhost/', true], // Linux, macOS
  ['http://wails.localhost/', true], // Windows
  ['http://wails.localhost:34115/', true], // Windows with port
  ['https://wails.localhost/', true],
  ['http://127.0.0.1:5180/', false], // browser mode
  ['http://localhost:5180/', false],
  ['https://wails.localhost.evil.com/', false],
])('isDesktopLocation(%s) === %s', (href, want) => {
  expect(isDesktopLocation(new URL(href))).toBe(want)
})

test('http client chat methods post snake_case bodies', async () => {
  // A fresh Response per call: real fetch() never hands back the same
  // (already-consumed) body twice, but a single mockResolvedValue() would.
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
    async () => new Response('{}', { headers: { 'content-type': 'application/json' } }),
  )
  await httpClient.sendPost(3, 'c1', 'hi')
  await httpClient.sendPost(3, 'c1', '', ['a1', 'a2'])
  await httpClient.sendReply(3, 'c1', 'r1', 're')
  await httpClient.sendReply(3, 'c1', 'r1', '', ['a1'])
  await httpClient.editPost(3, 'p1', 'v2')
  await httpClient.sidebar(3, '')
  await httpClient.downloadFile(3, 'f1')
  await httpClient.addReaction(3, 'p1', '+1')
  await httpClient.emojiInfo(3)
  await httpClient.mediaStreamBase()
  await httpClient.downloads()
  await httpClient.openDownload(7)
  await httpClient.revealDownload(7)
  await httpClient.removeDownload(7)
  await httpClient.clearDownloads()
  await httpClient.attachments(3, 'c1', '')
  await httpClient.attachments(3, 'c1', 'r1')
  await httpClient.removeAttachment(3, 'a1')
  await httpClient.retryAttachment(3, 'a1')
  await httpClient.attachFromClipboard(3, 'c1', '')
  await httpClient.pickAttachments(3, 'c1', '')
  await httpClient.saveThreadDraft(3, 'r1', 'draft text')
  await httpClient.openThread(3, 'c1', 'r1')
  await httpClient.getThread(3, 'r1')
  await httpClient.closeThread(3)
  await httpClient.loadOlderReplies(3, 'r1')
  expect(fetchMock.mock.calls.map(([p, i]) => [p, JSON.parse(i!.body as string)])).toEqual([
    ['/api/SendPost', { id: 3, channel_id: 'c1', message: 'hi', attachment_ids: [] }],
    ['/api/SendPost', { id: 3, channel_id: 'c1', message: '', attachment_ids: ['a1', 'a2'] }],
    ['/api/SendReply', { id: 3, channel_id: 'c1', root_id: 'r1', message: 're', attachment_ids: [] }],
    ['/api/SendReply', { id: 3, channel_id: 'c1', root_id: 'r1', message: '', attachment_ids: ['a1'] }],
    ['/api/EditPost', { id: 3, post_id: 'p1', message: 'v2' }],
    ['/api/Sidebar', { id: 3, team_id: '' }],
    ['/api/DownloadFile', { id: 3, file_id: 'f1' }],
    ['/api/AddReaction', { id: 3, post_id: 'p1', emoji: '+1' }],
    ['/api/EmojiInfo', { id: 3 }],
    ['/api/MediaStreamBase', {}],
    ['/api/Downloads', {}],
    ['/api/OpenDownload', { id: 7 }],
    ['/api/RevealDownload', { id: 7 }],
    ['/api/RemoveDownload', { id: 7 }],
    ['/api/ClearDownloads', {}],
    ['/api/Attachments', { id: 3, channel_id: 'c1', root_id: '' }],
    ['/api/Attachments', { id: 3, channel_id: 'c1', root_id: 'r1' }],
    ['/api/RemoveAttachment', { id: 3, attachment_id: 'a1' }],
    ['/api/RetryAttachment', { id: 3, attachment_id: 'a1' }],
    ['/api/AttachFromClipboard', { id: 3, channel_id: 'c1', root_id: '' }],
    ['/api/PickAttachments', { id: 3, channel_id: 'c1', root_id: '' }],
    ['/api/SaveThreadDraft', { id: 3, root_id: 'r1', text: 'draft text' }],
    ['/api/OpenThread', { id: 3, channel_id: 'c1', root_id: 'r1' }],
    ['/api/GetThread', { id: 3, root_id: 'r1' }],
    ['/api/CloseThread', { id: 3 }],
    ['/api/LoadOlderReplies', { id: 3, root_id: 'r1' }],
  ])
})

test('uploadAttachmentBrowser posts the raw File as the body to /api/attachments/{srv}/{channel}', async () => {
  document.head.innerHTML = '<meta name="spk-mm-client-api-token" content="tok123">'
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ id: 'a1', name: 'x.png', size: 3, mime: 'image/png', state: 'staged', sent: 0, error: '' }), {
      headers: { 'content-type': 'application/json' },
    }),
  )
  const file = new File(['abc'], 'x.png', { type: 'image/png' })
  const a = await uploadAttachmentBrowser(3, 'c1', file, '')
  expect(a.id).toBe('a1')
  const [url, init] = fetchMock.mock.calls[0]
  expect(url).toBe('/api/attachments/3/c1?root=&name=x.png&mime=image%2Fpng')
  expect((init!.headers as Record<string, string>).Authorization).toBe('Bearer tok123')
  expect(init!.body).toBe(file)
})

// Task 4: a non-empty rootId routes the upload to that thread's reply
// composer instead of the channel's (?root=).
test('uploadAttachmentBrowser with a rootId sends ?root= for the thread composer', async () => {
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ id: 'a1', name: 'x.png', size: 3, mime: 'image/png', state: 'staged', sent: 0, error: '' }), {
      headers: { 'content-type': 'application/json' },
    }),
  )
  await uploadAttachmentBrowser(3, 'c1', new File(['abc'], 'x.png', { type: 'image/png' }), 'r1')
  const [url] = fetchMock.mock.calls[0]
  expect(url).toBe('/api/attachments/3/c1?root=r1&name=x.png&mime=image%2Fpng')
})

test('uploadAttachmentBrowser turns a non-JSON error response into a generic ApiError', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('too large', { status: 413 }))
  await expect(uploadAttachmentBrowser(3, 'c1', new File(['x'], 'x.bin'), '')).rejects.toEqual(new ApiError('internal', 'HTTP 413'))
})

test('uploadAttachmentBrowser turns a JSON {code} error response into ApiError', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ code: 'too_large' }), { status: 413, headers: { 'content-type': 'application/json' } }),
  )
  await expect(uploadAttachmentBrowser(3, 'c1', new File(['x'], 'x.bin'), '')).rejects.toEqual(new ApiError('too_large', ''))
})

test('autocomplete posts its query and an abort cancels the fetch', async () => {
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ users: [{ id: 'u1', username: 'bob' }], others: [], channels: [], emoji: [], commands: [] }), {
      headers: { 'content-type': 'application/json' },
    }),
  )
  const ctl = new AbortController()
  const r = await httpClient.autocomplete(3, 'users', 'c1', 'r1', 'bo', ctl.signal)
  expect(r.users?.[0].username).toBe('bob')
  const [path, init] = fetchMock.mock.calls[0]
  expect(path).toBe('/api/Autocomplete')
  expect(JSON.parse(init!.body as string)).toEqual({ id: 3, kind: 'users', channel_id: 'c1', root_id: 'r1', prefix: 'bo' })
  expect(init!.signal).toBe(ctl.signal)
})

test('executeCommand posts the command', async () => {
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { headers: { 'content-type': 'application/json' } }))
  await httpClient.executeCommand(3, 'c1', '', '/echo hi')
  const [path, init] = fetchMock.mock.calls[0]
  expect(path).toBe('/api/ExecuteCommand')
  expect(JSON.parse(init!.body as string)).toEqual({ id: 3, channel_id: 'c1', root_id: '', command: '/echo hi' })
})

test('layout JSON uses integer pixels for both Go handlers', async () => {
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('null'))
  await httpClient.setSidebarWidth(313.43)
  expect(JSON.parse(fetchMock.mock.calls[0][1]!.body as string)).toEqual({ width: 313 })
  await httpClient.setThreadWidth(537.9688720703125)
  expect(JSON.parse(fetchMock.mock.calls[1][1]!.body as string)).toEqual({ width: 538 })
})

test('jump and gap calls post their bodies; an abort cancels the jump', async () => {
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
    async () => new Response(JSON.stringify({ post_id: 'p1', root_id: '', in_feed: true }), { headers: { 'content-type': 'application/json' } }),
  )
  const ctl = new AbortController()
  const r = await httpClient.jumpToPost(3, 'c1', 'p1', ctl.signal)
  expect(r).toEqual({ post_id: 'p1', root_id: '', in_feed: true })
  expect(fetchMock.mock.calls[0][1]!.signal).toBe(ctl.signal)
  await httpClient.loadNewer(3, 'c1')
  await httpClient.retryRevalidation(3, 'c1')
  expect(fetchMock.mock.calls.map(([p, i]) => [p, JSON.parse(i!.body as string)])).toEqual([
    ['/api/JumpToPost', { id: 3, channel_id: 'c1', post_id: 'p1' }],
    ['/api/LoadNewer', { id: 3, channel_id: 'c1' }],
    ['/api/RetryRevalidation', { id: 3, channel_id: 'c1' }],
  ])
})

test('thread focus calls post their bodies; an abort cancels openThreadAt', async () => {
  const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
    async () => new Response(JSON.stringify({ root_id: 'r1' }), { headers: { 'content-type': 'application/json' } }),
  )
  const ctl = new AbortController()
  expect(await httpClient.openThreadAt(3, 'c1', 'r1', 'p1', ctl.signal)).toEqual({ root_id: 'r1' })
  expect(fetchMock.mock.calls[0][1]!.signal).toBe(ctl.signal)
  await httpClient.loadThreadFocus(3, 'r1', false)
  await httpClient.retryThreadRevalidation(3, 'r1')
  expect(fetchMock.mock.calls.map(([p, i]) => [p, JSON.parse(i!.body as string)])).toEqual([
    ['/api/OpenThreadAt', { id: 3, channel_id: 'c1', root_id: 'r1', reply_id: 'p1' }],
    ['/api/LoadThreadFocus', { id: 3, root_id: 'r1', newer: false }],
    ['/api/RetryThreadRevalidation', { id: 3, root_id: 'r1' }],
  ])
})

test('a jump error keeps its code', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ code: 'post_gone', detail: '' }), { status: 400, headers: { 'content-type': 'application/json' } }),
  )
  await expect(httpClient.jumpToPost(3, 'c1', 'p1')).rejects.toEqual(new ApiError('post_gone', ''))
})
