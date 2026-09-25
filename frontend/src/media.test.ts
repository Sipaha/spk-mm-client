import { mediaURL } from './media'

test('media URLs are same-origin paths with encoded keys', () => {
  expect(mediaURL(3, 'avatar', 'u-bob', { v: '-42' })).toBe('/media/3/avatar/u-bob?v=-42')
  expect(mediaURL(1, 'emoji', '+1')).toBe('/media/1/emoji/%2B1')
  expect(mediaURL(1, 'feed', 'f1', { src: 'file' })).toBe('/media/1/feed/f1?src=file')
})
