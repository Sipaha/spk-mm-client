import { fireEvent, render } from '@testing-library/react'
import type { PostView } from '../api/types'
import { PostAvatar } from './PostAvatar'

const post = (o: Partial<PostView> = {}): PostView => ({
  id: 'w1', user_id: 'u-bob', author: 'GitLab', message: '', create_at: 0, avatar: '5', icon: 'post', icon_version: 'aaaa', ...o,
})

test('the icon URL carries its version', () => {
  const { container } = render(<PostAvatar serverId={1} post={post()} size={36} />)
  expect(container.querySelector('img')).toHaveAttribute('src', '/media/1/posticon/w1?v=aaaa')
})

test('a failed icon falls back to the avatar; a new icon version is tried again', () => {
  const { container, rerender } = render(<PostAvatar serverId={1} post={post()} size={36} />)
  fireEvent.error(container.querySelector('img')!)
  expect(container.querySelector('img')).toHaveAttribute('src', '/media/1/avatar/u-bob?v=5')
  rerender(<PostAvatar serverId={1} post={post({ icon_version: 'bbbb' })} size={36} />)
  expect(container.querySelector('img')).toHaveAttribute('src', '/media/1/posticon/w1?v=bbbb')
})

// A webhook icon Go has not fetched yet (a first fetch of an external host,
// or one that will fail) takes a moment: meanwhile the account's avatar
// stands in — never an empty circle — and the icon replaces it once loaded.
test('the account avatar stands in while the icon loads', () => {
  const { container } = render(<PostAvatar serverId={1} post={post()} size={36} />)
  const [icon, stand] = [...container.querySelectorAll('img')]
  expect(icon).toHaveAttribute('src', '/media/1/posticon/w1?v=aaaa')
  expect(stand).toHaveAttribute('src', '/media/1/avatar/u-bob?v=5')
  fireEvent.load(icon)
  expect([...container.querySelectorAll('img')].map((i) => i.getAttribute('src'))).toEqual(['/media/1/posticon/w1?v=aaaa'])
})

test('an icon already in the webview cache shows at once, without the stand-in', () => {
  const complete = vi.spyOn(HTMLImageElement.prototype, 'complete', 'get').mockReturnValue(true)
  const width = vi.spyOn(HTMLImageElement.prototype, 'naturalWidth', 'get').mockReturnValue(36)
  try {
    const { container } = render(<PostAvatar serverId={1} post={post()} size={36} />)
    expect([...container.querySelectorAll('img')].map((i) => i.getAttribute('src'))).toEqual(['/media/1/posticon/w1?v=aaaa'])
  } finally {
    complete.mockRestore()
    width.mockRestore()
  }
})

test('a new icon version waits for its own load, with the stand-in again', () => {
  const { container, rerender } = render(<PostAvatar serverId={1} post={post()} size={36} />)
  fireEvent.load(container.querySelector('img')!)
  rerender(<PostAvatar serverId={1} post={post({ icon_version: 'bbbb' })} size={36} />)
  expect([...container.querySelectorAll('img')].map((i) => i.getAttribute('src'))).toEqual([
    '/media/1/posticon/w1?v=bbbb',
    '/media/1/avatar/u-bob?v=5',
  ])
})
