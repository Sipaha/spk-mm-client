import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { setLocale } from '../i18n'
import EmojiPicker, { placePicker } from './EmojiPicker'

const anchor = { top: 100, bottom: 120, left: 500, right: 520, width: 20, height: 20, x: 500, y: 100, toJSON() {} } as DOMRect

beforeEach(() => setLocale('en'))

test('recent, standard and custom sections; custom emoji are server pictures', async () => {
  render(<EmojiPicker serverId={2} anchor={anchor} info={{ recent: ['tada', 'partyparrot', 'gone_custom'], custom: ['partyparrot'], custom_enabled: true }} onPick={vi.fn()} onClose={vi.fn()} />)
  const recent = await screen.findByRole('region', { name: 'Recently used' })
  expect(within(recent).getAllByRole('button').map((b) => b.getAttribute('aria-label'))).toEqual([':tada:', ':partyparrot:'])
  expect(within(recent).getByRole('button', { name: ':partyparrot:' }).querySelector('img')).toHaveAttribute('src', '/media/2/emoji/partyparrot')
  expect(screen.getByRole('region', { name: 'Smileys & Emotion' })).toBeInTheDocument()
  expect(within(screen.getByRole('region', { name: 'Custom' })).getAllByRole('button')).toHaveLength(1)
  expect(screen.getByRole('textbox', { name: 'Search emoji' })).toHaveFocus()
})

test('search and keyboard: arrows through results, Enter picks, Escape closes', async () => {
  const onPick = vi.fn()
  const onClose = vi.fn()
  render(<EmojiPicker serverId={1} anchor={anchor} info={{ recent: [], custom: [], custom_enabled: false }} onPick={onPick} onClose={onClose} />)
  await screen.findByRole('region', { name: 'Smileys & Emotion' })
  await userEvent.type(screen.getByRole('textbox', { name: 'Search emoji' }), 'thumbs')
  await userEvent.keyboard('{ArrowDown}')
  expect(screen.getByRole('button', { name: ':+1:' })).toHaveFocus()
  await userEvent.keyboard('{ArrowRight}')
  expect(screen.getByRole('button', { name: ':-1:' })).toHaveFocus()
  await userEvent.keyboard('{ArrowUp}')
  expect(screen.getByRole('textbox', { name: 'Search emoji' })).toHaveFocus()
  await userEvent.keyboard('{ArrowDown}{ArrowRight}{Enter}')
  expect(onPick).toHaveBeenCalledWith('-1')
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalled()
})

test('Enter in the search box picks the first result; nothing found says so', async () => {
  const onPick = vi.fn()
  render(<EmojiPicker serverId={1} anchor={anchor} info={null} onPick={onPick} onClose={vi.fn()} />)
  await screen.findByRole('region', { name: 'Smileys & Emotion' })
  const box = screen.getByRole('textbox', { name: 'Search emoji' })
  await userEvent.type(box, 'zzzqqq')
  expect(screen.getByText('No emoji found')).toBeInTheDocument()
  await userEvent.clear(box)
  await userEvent.type(box, 'rocket{Enter}')
  expect(onPick).toHaveBeenCalledWith('rocket')
})

test('the picker opens below its button, or above when there is no room', () => {
  expect(placePicker({ top: 100, bottom: 120, right: 520 }, 1400, 900)).toEqual({ left: 160, top: 124 })
  expect(placePicker({ top: 800, bottom: 820, right: 50 }, 1400, 900)).toEqual({ left: 8, top: 416 })
})
