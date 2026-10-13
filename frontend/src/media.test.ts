import { useStore } from './store'
import { vi } from 'vitest'
import { client } from './api/client'
import { mediaURL, streamURL } from './media'

test('media URLs are same-origin paths with encoded keys', () => {
  expect(mediaURL(3, 'avatar', 'u-bob', { v: '-42' })).toBe('/media/3/avatar/u-bob?v=-42')
  expect(mediaURL(1, 'emoji', '+1')).toBe('/media/1/emoji/%2B1')
  expect(mediaURL(1, 'feed', 'f1', { src: 'file' })).toBe('/media/1/feed/f1?src=file')
})

// streamURL: Task 7's contract — the base comes from the API exactly once
// (a module-level cache; never localStorage, never logged — the desktop
// base carries a secret token) and every player composes
// `${base}/${serverId}/stream/${fileId}` from it.
test('streamURL asks the API for the stream base once and composes <base>/<srv>/stream/<id>', async () => {
  const spy = vi.spyOn(client, 'mediaStreamBase').mockResolvedValue('/media')
  expect(await streamURL(2, 'f1')).toBe('/media/2/stream/f1')
  expect(await streamURL(3, 'f2')).toBe('/media/3/stream/f2')
  expect(spy).toHaveBeenCalledTimes(1)
  spy.mockRestore()
})

test('all cached URL kinds and streams change with the signed-in account', async () => {
 const server = { id: 1, name: 'test', url: '', signed_in: true, username: 'alice', gitlab: false, state: 'live' as const, unread: false, mentions: 0, media_scope: 'alice-scope' }
 useStore.setState({ servers: [server] })
 const kinds = ['avatar', 'thumb', 'feed', 'full', 'text', 'emoji', 'staged', 'posticon', 'pdf'] as const
 const old = kinds.map(kind => mediaURL(1, kind, 'f1'))
 useStore.setState({ servers: [{ ...server, media_scope: 'bob-scope' }] })
 kinds.forEach((kind, i) => { expect(mediaURL(1, kind, 'f1')).not.toBe(old[i]); expect(mediaURL(1, kind, 'f1')).toContain('account=bob-scope') })
 useStore.setState({ servers: [{ ...server, signed_in: false }] })
 expect(mediaURL(1, 'thumb', 'f1')).not.toContain('account=')
 useStore.setState({ servers: [] })
})
