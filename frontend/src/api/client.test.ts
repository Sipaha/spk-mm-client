import { afterEach, vi } from 'vitest'
import { ApiError, httpClient, isDesktopLocation, parseWailsError } from './client'

afterEach(() => {
  vi.restoreAllMocks()
  document.head.innerHTML = ''
})

test('http client sends bearer token from meta tag and JSON body', async () => {
  document.head.innerHTML = '<meta name="spk-mattermost-api-token" content="tok123">'
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
