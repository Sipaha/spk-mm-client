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
