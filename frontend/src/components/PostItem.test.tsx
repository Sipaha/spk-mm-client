import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { ApiError } from '../api/client'
import type { PostView } from '../api/types'
import { setLocale } from '../i18n'
import { PostItem, type PostActions } from './PostItem'

const post = (o: Partial<PostView> = {}): PostView => ({
  id: 'p1', user_id: 'u-bob', author: 'bob', message: 'hello', create_at: new Date(2026, 8, 24, 13, 5).getTime(), ...o,
})
const actions = (): PostActions => ({
  link: vi.fn(), retry: vi.fn(), discard: vi.fn(), edit: vi.fn(), saveEdit: vi.fn().mockResolvedValue(undefined),
  cancelEdit: vi.fn(), remove: vi.fn(), markUnread: vi.fn(), copyLink: vi.fn(),
})
const me = { id: 'u-alice', username: 'alice' }

beforeEach(() => setLocale('en'))

test('head shows author, time and bot badge; follow-up hides them', () => {
  const { rerender } = render(<PostItem post={post({ bot: true, edit_at: 1 })} head me={me} locale="ru-RU" crt={false} actions={actions()} editing={false} />)
  expect(screen.getByText('bob')).toBeInTheDocument()
  expect(screen.getByText('BOT')).toBeInTheDocument()
  expect(screen.getAllByText('13:05')).toHaveLength(1)
  expect(screen.getByText('(edited)')).toBeInTheDocument()
  rerender(<PostItem post={post()} head={false} me={me} locale="ru-RU" crt={false} actions={actions()} editing={false} />)
  expect(screen.queryByText('bob')).toBeNull()
})

test('attachments, files, reactions and reply count', async () => {
  const a = actions()
  render(
    <PostItem
      post={post({
        message: '',
        attachments: [{ color: 'danger', pretext: 'Build', title: 'Pipeline #7', title_link: 'https://ci/7', text: 'failed on **test**', fields: [{ title: 'Branch', value: 'main', short: true }] }],
        files: [{ name: 'report.pdf', size: 2048, mime: 'application/pdf' }],
        reactions: [{ emoji: '+1', count: 2, mine: true }, { emoji: 'custom_party', count: 1, mine: false }],
        reply_count: 3,
      })}
      head
      me={me}
      locale="en-US"
      crt
      actions={a}
      editing={false}
    />,
  )
  expect(screen.getByText('test').tagName).toBe('STRONG')
  expect(screen.getByText('Branch')).toBeInTheDocument()
  expect(screen.getByText('report.pdf')).toBeInTheDocument()
  expect(screen.getByText('2.0 KB')).toBeInTheDocument()
  expect(screen.getByTitle(':+1:')).toHaveTextContent('👍 2')
  expect(screen.getByTitle(':custom_party:')).toHaveTextContent(':custom_party: 1')
  expect(screen.getByText('Replies: 3')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('link', { name: 'Pipeline #7' }))
  expect(a.link).toHaveBeenCalledWith('https://ci/7')
})

test('pending and failed posts', async () => {
  const a = actions()
  const { rerender } = render(<PostItem post={post({ pending: true, user_id: 'u-alice' })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  expect(screen.getByText('Sending…')).toBeInTheDocument()
  const failed = post({ failed: true, user_id: 'u-alice' })
  rerender(<PostItem post={failed} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(a.retry).toHaveBeenCalledWith(failed)
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
  expect(a.discard).toHaveBeenCalledWith(failed)
})

test('own post offers edit and delete; others only mark unread and copy link', async () => {
  const a = actions()
  const own = post({ user_id: 'u-alice' })
  const { rerender } = render(<PostItem post={own} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
  expect(a.edit).toHaveBeenCalledWith(own)
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }))
  expect(a.remove).toHaveBeenCalledWith(own)
  const other = post()
  rerender(<PostItem post={other} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
  await userEvent.click(screen.getByRole('button', { name: 'Mark as unread' }))
  expect(a.markUnread).toHaveBeenCalledWith(other)
  await userEvent.click(screen.getByRole('button', { name: 'Copy link' }))
  expect(a.copyLink).toHaveBeenCalledWith(other)
})

test('inline edit: Enter saves, Escape cancels, errors stay visible', async () => {
  const a = actions()
  const own = post({ user_id: 'u-alice', message: 'v1' })
  const { rerender } = render(<PostItem post={own} head me={me} locale="en-US" crt={false} actions={a} editing />)
  const box = screen.getByRole('textbox', { name: 'Edit message' })
  expect(box).toHaveValue('v1')
  expect(screen.queryByRole('toolbar')).toBeNull()
  await userEvent.type(box, ' v2{Enter}')
  expect(a.saveEdit).toHaveBeenCalledWith(own, 'v1 v2')
  await userEvent.type(box, '{Escape}')
  expect(a.cancelEdit).toHaveBeenCalled()
  a.saveEdit = vi.fn().mockRejectedValue(new ApiError('forbidden', ''))
  rerender(<PostItem post={own} head me={me} locale="en-US" crt={false} actions={{ ...a }} editing />)
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('You are not allowed to do that')
})
