import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { PostView } from '../api/types'
import { setLocale } from '../i18n'
import { PostItem, type PostActions } from './PostItem'

const post = (o: Partial<PostView> = {}): PostView => ({
  id: 'p1', user_id: 'u-bob', author: 'bob', message: 'hello', create_at: new Date(2026, 8, 24, 13, 5).getTime(), ...o,
})
const actions = (): PostActions => ({ link: vi.fn(), retry: vi.fn(), discard: vi.fn() })
const me = { id: 'u-alice', username: 'alice' }

beforeEach(() => setLocale('en'))

test('head shows author, time and bot badge; follow-up hides them', () => {
  const { rerender } = render(<PostItem post={post({ bot: true, edit_at: 1 })} head me={me} locale="ru-RU" crt={false} actions={actions()} />)
  expect(screen.getByText('bob')).toBeInTheDocument()
  expect(screen.getByText('BOT')).toBeInTheDocument()
  expect(screen.getAllByText('13:05')).toHaveLength(1)
  expect(screen.getByText('(edited)')).toBeInTheDocument()
  rerender(<PostItem post={post()} head={false} me={me} locale="ru-RU" crt={false} actions={actions()} />)
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
  const { rerender } = render(<PostItem post={post({ pending: true, user_id: 'u-alice' })} head me={me} locale="en-US" crt={false} actions={a} />)
  expect(screen.getByText('Sending…')).toBeInTheDocument()
  const failed = post({ failed: true, user_id: 'u-alice' })
  rerender(<PostItem post={failed} head me={me} locale="en-US" crt={false} actions={a} />)
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(a.retry).toHaveBeenCalledWith(failed)
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
  expect(a.discard).toHaveBeenCalledWith(failed)
})
