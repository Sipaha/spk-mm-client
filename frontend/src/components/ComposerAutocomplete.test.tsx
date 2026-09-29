import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import type { AutocompleteDTO } from '../api/types'
import { setLocale } from '../i18n'
import { Composer } from './Composer'

vi.mock('../api/client', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/client')>()
  return {
    ...real,
    isDesktop: vi.fn(() => false),
    client: {
      ...real.client,
      getFormattingBarHidden: vi.fn().mockResolvedValue(false),
      setFormattingBarHidden: vi.fn().mockResolvedValue(undefined),
      autocomplete: vi.fn(),
    },
  }
})
vi.mock('../chat', () => ({
  pickAttachments: vi.fn(),
  attachFromClipboard: vi.fn(),
  uploadAttachments: vi.fn(),
  removeAttachment: vi.fn(),
  retryAttachment: vi.fn(),
}))

const { client } = await import('../api/client')
const ac = vi.mocked(client.autocomplete)

const dto = (d: Partial<AutocompleteDTO>): AutocompleteDTO => ({ users: [], others: [], channels: [], emoji: [], commands: [], ...d })
const users = dto({
  users: [
    { id: 'u-bob', username: 'bob', full_name: 'Bob Brown', status: 'online' },
    { id: 'u-bea', username: 'bea' },
  ],
  others: [{ id: 'u-carol', username: 'carol', full_name: 'Carol Clark' }],
})

beforeEach(() => {
  setLocale('en')
  ac.mockReset()
})
afterEach(() => vi.useRealTimers())

function setup(rootId = '') {
  const onSend = vi.fn().mockResolvedValue(undefined)
  render(
    <Composer
      channelId="c1"
      channelName="Town Square"
      draft=""
      rootId={rootId}
      serverId={1}
      attachments={[]}
      emojiInfo={() => Promise.resolve({ recent: [], custom: ['partyparrot'], custom_enabled: true })}
      onSend={onSend}
      onDraft={() => {}}
      onEditLast={() => {}}
    />,
  )
  const box = screen.getByLabelText('Message') as HTMLTextAreaElement
  return { box, onSend }
}

test('@b opens the user list after a debounce; the textarea points at the active option', async () => {
  ac.mockResolvedValue(users)
  const { box } = setup()
  await userEvent.type(box, 'hi @b')
  const list = await screen.findByRole('listbox')
  expect(ac).toHaveBeenCalledTimes(1)
  expect(ac.mock.calls[0].slice(0, 5)).toEqual([1, 'users', 'c1', '', 'b'])
  const opts = within(list).getAllByRole('option')
  expect(opts.map((o) => o.textContent)).toEqual([
    expect.stringContaining('@bob'),
    expect.stringContaining('@bea'),
    expect.stringContaining('@carol'),
  ])
  expect(within(list).getByText('Channel Members')).toBeInTheDocument()
  expect(within(list).getByText('Not in Channel')).toBeInTheDocument()
  expect(box).toHaveAttribute('aria-expanded', 'true')
  expect(box).toHaveAttribute('aria-controls', list.id)
  expect(box).toHaveAttribute('aria-activedescendant', opts[0].id)
  expect(opts[0]).toHaveAttribute('aria-selected', 'true')
})

test('arrows move, Enter inserts and does not send; Tab and a click insert too', async () => {
  ac.mockResolvedValue(users)
  const { box, onSend } = setup()
  await userEvent.type(box, '@b')
  await screen.findByRole('listbox')
  await userEvent.keyboard('{ArrowDown}{ArrowDown}{ArrowUp}')
  expect(box.getAttribute('aria-activedescendant')).toBe(screen.getAllByRole('option')[1].id)
  await userEvent.keyboard('{Enter}')
  expect(box.value).toBe('@bea ')
  expect(onSend).not.toHaveBeenCalled()
  expect(screen.queryByRole('listbox')).toBeNull()
  expect(box).toHaveAttribute('aria-expanded', 'false')

  await userEvent.type(box, '@z') // no special mention starts with z: the server's rows only
  await screen.findByRole('listbox')
  await userEvent.keyboard('{Tab}')
  expect(box.value).toBe('@bea @bob ')
  expect(document.activeElement).toBe(box)

  await userEvent.type(box, '@x')
  await userEvent.click(within(await screen.findByRole('listbox')).getByText('@carol'))
  expect(box.value).toBe('@bea @bob @carol ')
  expect(document.activeElement).toBe(box)
})

test('Esc closes the popup for that word; Enter then sends', async () => {
  ac.mockResolvedValue(users)
  const { box, onSend } = setup()
  await userEvent.type(box, 'hi @b')
  await screen.findByRole('listbox')
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('listbox')).toBeNull()
  await userEvent.type(box, 'o')
  await new Promise((r) => setTimeout(r, 250))
  expect(screen.queryByRole('listbox')).toBeNull()
  await userEvent.keyboard('{Enter}')
  expect(onSend).toHaveBeenCalledWith('hi @bo', [])
})

test('special mentions follow the prefix', async () => {
  ac.mockResolvedValue(dto({}))
  const { box } = setup()
  await userEvent.type(box, '@h')
  const list = await screen.findByRole('listbox')
  expect(within(list).getAllByRole('option').map((o) => o.textContent)).toEqual([expect.stringContaining('@here')])
  expect(within(list).getByText('Special Mentions')).toBeInTheDocument()
})

test('an out-of-order answer is ignored and the stale request is aborted', async () => {
  const resolvers: ((d: AutocompleteDTO) => void)[] = []
  const signals: AbortSignal[] = []
  ac.mockImplementation((_id, _k, _c, _r, _p, signal) => {
    signals.push(signal!)
    return new Promise((r) => resolvers.push(r))
  })
  const { box } = setup()
  await userEvent.type(box, '@b')
  await waitFor(() => expect(ac).toHaveBeenCalledTimes(1))
  await userEvent.type(box, 'o')
  await waitFor(() => expect(ac).toHaveBeenCalledTimes(2))
  expect(signals[0].aborted).toBe(true)
  await act(async () => resolvers[1](dto({ users: [{ id: 'u-bob', username: 'bob' }] })))
  await act(async () => resolvers[0](dto({ users: [{ id: 'u-bea', username: 'bea' }] })))
  const opts = within(screen.getByRole('listbox')).getAllByRole('option')
  expect(opts.map((o) => o.textContent)).toEqual([expect.stringContaining('@bob')])
})

test('an email address asks for nothing', async () => {
  ac.mockResolvedValue(users)
  const { box } = setup()
  await userEvent.type(box, 'mail a@b')
  await new Promise((r) => setTimeout(r, 250))
  expect(ac).not.toHaveBeenCalled()
  expect(screen.queryByRole('listbox')).toBeNull()
})

test('a failed request shows nothing and never blocks typing or sending', async () => {
  ac.mockRejectedValue(new Error('unreachable'))
  const { box, onSend } = setup()
  await userEvent.type(box, '@bo')
  await waitFor(() => expect(ac).toHaveBeenCalled())
  await new Promise((r) => setTimeout(r, 50))
  expect(screen.queryByRole('listbox')).toBeNull()
  await userEvent.type(box, 'b{Enter}')
  expect(onSend).toHaveBeenCalledWith('@bob', [])
})

test(':thu lists 👍 first (the local set) and inserts :thumbsup:', async () => {
  ac.mockResolvedValue(dto({ emoji: [] }))
  const { box } = setup()
  await userEvent.type(box, ':thu')
  const list = await screen.findByRole('listbox')
  await waitFor(() => expect(within(list).getAllByRole('option')[0].textContent).toContain(':thumbsup:'))
  expect(within(list).getByText('Emoji')).toBeInTheDocument()
  await userEvent.keyboard('{Enter}')
  expect(box.value).toBe(':thumbsup: ')
})

test('custom emoji from the server join the list', async () => {
  ac.mockResolvedValue(dto({ emoji: ['parrotwave'] }))
  const { box } = setup()
  await userEvent.type(box, ':parr')
  const list = await screen.findByRole('listbox')
  await waitFor(() => expect(within(list).getAllByRole('option').map((o) => o.textContent)).toEqual([
    expect.stringContaining(':parrot:'), // the standard 🦜 first
    expect.stringContaining(':parrotwave:'),
    expect.stringContaining(':partyparrot:'),
  ]))
  expect(ac.mock.calls.at(-1)!.slice(0, 5)).toEqual([1, 'emoji', 'c1', '', 'parr'])
})

test('~ lists my channels first and inserts the channel name', async () => {
  ac.mockResolvedValue(dto({ channels: [
    { id: 'c2', name: 'off-topic', display_name: 'Off-Topic', type: 'O', joined: true },
    { id: 'c3', name: 'offices', display_name: 'Offices', type: 'O' },
  ] }))
  const { box } = setup()
  await userEvent.type(box, 'see ~off')
  const list = await screen.findByRole('listbox')
  expect(within(list).getByText('My Channels')).toBeInTheDocument()
  expect(within(list).getByText('Other Channels')).toBeInTheDocument()
  await userEvent.keyboard('{Enter}')
  expect(box.value).toBe('see ~off-topic ')
})

test('/ at the start lists commands with their hint and description; the thread composer passes its root', async () => {
  ac.mockResolvedValue(dto({ commands: [{ trigger: 'echo', hint: '"message"', description: 'Echo back text' }] }))
  const { box, onSend } = setup('r1')
  await userEvent.type(box, '/ec')
  const list = await screen.findByRole('listbox')
  expect(ac.mock.calls.at(-1)!.slice(0, 5)).toEqual([1, 'commands', 'c1', 'r1', 'ec'])
  expect(within(list).getByText('Echo back text')).toBeInTheDocument()
  await userEvent.keyboard('{Enter}')
  expect(box.value).toBe('/echo ')
  expect(onSend).not.toHaveBeenCalled()
  expect(screen.queryByRole('listbox')).toBeNull()
})

test('Escape in the popup does not reach the thread panel', async () => {
  ac.mockResolvedValue(users)
  const { box } = setup('r1')
  const seen = vi.fn()
  document.addEventListener('keydown', (e) => seen(e.defaultPrevented))
  await userEvent.type(box, '@b')
  await screen.findByRole('listbox')
  fireEvent.keyDown(box, { key: 'Escape' })
  expect(seen).toHaveBeenLastCalledWith(true)
})
