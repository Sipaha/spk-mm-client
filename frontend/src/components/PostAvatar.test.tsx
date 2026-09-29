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

const srcs = (c: HTMLElement) => [...c.querySelectorAll('img')].map((i) => i.getAttribute('src'))
const generic = (c: HTMLElement) => c.querySelector('[data-webhook-icon]')

// A webhook post never shows its owner's account avatar — it would read as
// if the owner had written it (webapp: DEFAULT_WEBHOOK_LOGO). Without an
// icon of its own, while that icon loads, and when it fails: the generic
// webhook icon.
test('a webhook post without an icon of its own shows the generic webhook icon', () => {
  const { container } = render(<PostAvatar serverId={1} post={post({ icon: 'webhook', icon_version: undefined })} size={36} />)
  expect(srcs(container)).toEqual([])
  expect(generic(container)).toBeInTheDocument()
  expect(generic(container)!.querySelector('svg')).toBeInTheDocument()
})

test('a failed icon falls back to the generic webhook icon; a new icon version is tried again', () => {
  const { container, rerender } = render(<PostAvatar serverId={1} post={post()} size={36} />)
  fireEvent.error(container.querySelector('img')!)
  expect(srcs(container)).toEqual([])
  expect(generic(container)).toBeInTheDocument()
  rerender(<PostAvatar serverId={1} post={post({ icon_version: 'bbbb' })} size={36} />)
  expect(srcs(container)).toEqual(['/media/1/posticon/w1?v=bbbb'])
})

// A webhook icon Go has not fetched yet (a first fetch of an external host,
// or one that will fail) takes a moment: meanwhile the generic webhook icon
// stands in — never an empty circle — and the icon replaces it once loaded.
test('the generic webhook icon stands in while the icon loads', () => {
  const { container } = render(<PostAvatar serverId={1} post={post()} size={36} />)
  expect(srcs(container)).toEqual(['/media/1/posticon/w1?v=aaaa'])
  expect(generic(container)).toBeInTheDocument()
  fireEvent.load(container.querySelector('img')!)
  expect(srcs(container)).toEqual(['/media/1/posticon/w1?v=aaaa'])
  expect(generic(container)).toBeNull()
})

test('an icon already in the webview cache shows at once, without the stand-in', () => {
  const complete = vi.spyOn(HTMLImageElement.prototype, 'complete', 'get').mockReturnValue(true)
  const width = vi.spyOn(HTMLImageElement.prototype, 'naturalWidth', 'get').mockReturnValue(36)
  try {
    const { container } = render(<PostAvatar serverId={1} post={post()} size={36} />)
    expect(srcs(container)).toEqual(['/media/1/posticon/w1?v=aaaa'])
    expect(generic(container)).toBeNull()
  } finally {
    complete.mockRestore()
    width.mockRestore()
  }
})

test('a new icon version waits for its own load, with the stand-in again', () => {
  const { container, rerender } = render(<PostAvatar serverId={1} post={post()} size={36} />)
  fireEvent.load(container.querySelector('img')!)
  rerender(<PostAvatar serverId={1} post={post({ icon_version: 'bbbb' })} size={36} />)
  expect(srcs(container)).toEqual(['/media/1/posticon/w1?v=bbbb'])
  expect(generic(container)).toBeInTheDocument()
})

test('a post without an override keeps the account avatar (a bot account too)', () => {
  const { container } = render(<PostAvatar serverId={1} post={post({ icon: undefined, icon_version: undefined })} size={36} />)
  expect(srcs(container)).toEqual(['/media/1/avatar/u-bob?v=5'])
  expect(generic(container)).toBeNull()
})
