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
  await httpClient.attachments(3, 'c1')
  await httpClient.removeAttachment(3, 'a1')
  await httpClient.retryAttachment(3, 'a1')
  await httpClient.attachFromClipboard(3, 'c1')
  await httpClient.pickAttachments(3, 'c1')
  await httpClient.openThread(3, 'c1', 'r1')
  await httpClient.getThread(3, 'r1')
  await httpClient.closeThread(3)
  await httpClient.loadOlderReplies(3, 'r1')
  expect(fetchMock.mock.calls.map(([p, i]) => [p, JSON.parse(i!.body as string)])).toEqual([
    ['/api/SendPost', { id: 3, channel_id: 'c1', message: 'hi', attachment_ids: [] }],
    ['/api/SendPost', { id: 3, channel_id: 'c1', message: '', attachment_ids: ['a1', 'a2'] }],
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
    ['/api/Attachments', { id: 3, channel_id: 'c1' }],
    ['/api/RemoveAttachment', { id: 3, attachment_id: 'a1' }],
    ['/api/RetryAttachment', { id: 3, attachment_id: 'a1' }],
    ['/api/AttachFromClipboard', { id: 3, channel_id: 'c1' }],
    ['/api/PickAttachments', { id: 3, channel_id: 'c1' }],
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
  const a = await uploadAttachmentBrowser(3, 'c1', file)
  expect(a.id).toBe('a1')
  const [url, init] = fetchMock.mock.calls[0]
  expect(url).toBe('/api/attachments/3/c1?name=x.png&mime=image%2Fpng')
  expect((init!.headers as Record<string, string>).Authorization).toBe('Bearer tok123')
  expect(init!.body).toBe(file)
})

test('uploadAttachmentBrowser turns a non-JSON error response into a generic ApiError', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('too large', { status: 413 }))
  await expect(uploadAttachmentBrowser(3, 'c1', new File(['x'], 'x.bin'))).rejects.toEqual(new ApiError('internal', 'HTTP 413'))
})

test('uploadAttachmentBrowser turns a JSON {code} error response into ApiError', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ code: 'too_large' }), { status: 413, headers: { 'content-type': 'application/json' } }),
  )
  await expect(uploadAttachmentBrowser(3, 'c1', new File(['x'], 'x.bin'))).rejects.toEqual(new ApiError('too_large', ''))
})
