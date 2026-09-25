import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { setLocale } from '../i18n'
import { Reactions } from './Reactions'

beforeEach(() => setLocale('en'))

test('chips toggle our reaction and say whether it is ours', async () => {
  const onToggle = vi.fn()
  render(<Reactions serverId={1} reactions={[{ emoji: '+1', count: 3, mine: true }, { emoji: 'tada', count: 1, mine: false }]} onToggle={onToggle} />)
  const mine = screen.getByRole('button', { name: '👍 3, you reacted' })
  expect(mine).toHaveAttribute('aria-pressed', 'true')
  await userEvent.click(screen.getByRole('button', { name: '🎉 1' }))
  expect(onToggle).toHaveBeenCalledWith({ emoji: 'tada', count: 1, mine: false })
  await userEvent.click(mine)
  expect(onToggle).toHaveBeenLastCalledWith({ emoji: '+1', count: 3, mine: true })
  expect(screen.queryByRole('button', { name: 'Add reaction' })).toBeNull()
})

test('a custom emoji is a picture from the media endpoint once the set says it is not standard', async () => {
  const { container } = render(<Reactions serverId={4} reactions={[{ emoji: 'partyparrot', count: 1, mine: false }]} onToggle={vi.fn()} />)
  expect(screen.getByRole('button', { name: ':partyparrot: 1' })).toBeInTheDocument()
  await waitFor(() => expect(container.querySelector('img')).toHaveAttribute('src', '/media/4/emoji/partyparrot'))
  fireEvent.error(container.querySelector('img')!)
  expect(container).toHaveTextContent(':partyparrot:')
})

test('a standard emoji outside the common table appears once the set loads', async () => {
  render(<Reactions serverId={1} reactions={[{ emoji: 'avocado', count: 2, mine: false }]} onToggle={vi.fn()} />)
  expect(await screen.findByRole('button', { name: '🥑 2' })).toBeInTheDocument()
})
