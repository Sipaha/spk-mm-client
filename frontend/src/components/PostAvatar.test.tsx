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
