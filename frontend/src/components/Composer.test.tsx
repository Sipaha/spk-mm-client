import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { ApiError } from '../api/client'
import type { ChannelDTO } from '../api/types'
import { setLocale } from '../i18n'
import { Composer } from './Composer'

const channel = (o: Partial<ChannelDTO> = {}): ChannelDTO => ({
  id: 'c1', name: 'Off-Topic', type: 'O', header: '', purpose: '', team_id: 't', team_name: 'team', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'me', crt: false, muted: false, ...o,
})

beforeEach(() => setLocale('en'))
afterEach(() => vi.useRealTimers())

test('Enter sends and clears, Shift+Enter makes a new line, blank is not sent', async () => {
  const onSend = vi.fn().mockResolvedValue(undefined)
  render(<Composer channel={channel()} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  expect(box).toHaveAttribute('placeholder', 'Write to Off-Topic')
  await userEvent.type(box, '   {Enter}')
  expect(onSend).not.toHaveBeenCalled()
  await userEvent.clear(box)
  await userEvent.type(box, 'line one{Shift>}{Enter}{/Shift}line two{Enter}')
  expect(onSend).toHaveBeenCalledWith('line one\nline two')
  expect(box).toHaveValue('')
})

test('a refused send puts the text back with the error', async () => {
  const onSend = vi.fn().mockRejectedValue(new ApiError('session_expired', ''))
  render(<Composer channel={channel()} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'hello{Enter}')
  expect(await screen.findByRole('alert')).toHaveTextContent('Session expired — sign in again')
  expect(box).toHaveValue('hello')
})

test('ArrowUp in an empty box edits the last own post', async () => {
  const onEditLast = vi.fn()
  render(<Composer channel={channel()} onSend={vi.fn()} onDraft={() => {}} onEditLast={onEditLast} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'x{ArrowUp}')
  expect(onEditLast).not.toHaveBeenCalled()
  await userEvent.clear(box)
  await userEvent.type(box, '{ArrowUp}')
  expect(onEditLast).toHaveBeenCalledTimes(1)
})

test('draft: restored, saved 500 ms after typing, flushed when leaving the channel', async () => {
  vi.useFakeTimers()
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const onDraft = vi.fn()
  const { unmount } = render(<Composer channel={channel({ draft: 'saved' })} onSend={vi.fn()} onDraft={onDraft} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  expect(box).toHaveValue('saved')
  await user.type(box, '!')
  expect(onDraft).not.toHaveBeenCalled()
  await act(async () => vi.advanceTimersByTime(500))
  expect(onDraft).toHaveBeenLastCalledWith('saved!')
  await user.type(box, '?')
  unmount()
  expect(onDraft).toHaveBeenLastCalledWith('saved!?')
})
