import { fireEvent, render, screen } from '@testing-library/react'
import { vi } from 'vitest'
import type { ChannelDTO, PostView } from '../api/types'
import { setLocale } from '../i18n'
import { Feed } from './Feed'

// jsdom has no layout: give the scroller and rows sizes so the virtualizer renders.
const saved = {
  h: Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight'),
  w: Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth'),
  scrollTo: HTMLElement.prototype.scrollTo,
}
beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get() {
      return (this as HTMLElement).getAttribute('role') === 'log' ? 600 : 40
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get: () => 800 })
  HTMLElement.prototype.scrollTo = vi.fn() as unknown as typeof HTMLElement.prototype.scrollTo
})
afterAll(() => {
  if (saved.h) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', saved.h)
  if (saved.w) Object.defineProperty(HTMLElement.prototype, 'offsetWidth', saved.w)
  HTMLElement.prototype.scrollTo = saved.scrollTo
})
beforeEach(() => setLocale('en'))

const now = Date.now()
const P = (id: string, user: string, minAgo: number): PostView => ({ id, user_id: user, author: user, message: `text ${id}`, create_at: now - minAgo * 60_000 })
const channel = (o: Partial<ChannelDTO> = {}): ChannelDTO => ({
  id: 'c1', name: 'C', type: 'O', header: '', purpose: '', team_id: 't', team_name: 'team',
  posts: [P('a', 'bob', 30), P('b', 'bob', 29), P('c', 'carol', 5)],
  new_since: now - 10 * 60_000, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'me', crt: false, muted: false,
  ...o,
})
const props = (o: Partial<ChannelDTO> = {}, onLoadOlder = vi.fn().mockResolvedValue(true)) => ({
  channel: channel(o), me: { id: 'me', username: 'me' }, locale: 'en-US',
  actions: { link: vi.fn(), retry: vi.fn(), discard: vi.fn() }, onLoadOlder,
})

test('renders posts, the day separator and the new-messages line', () => {
  render(<Feed {...props()} />)
  expect(screen.getByRole('log', { name: 'Messages' })).toBeInTheDocument()
  expect(screen.getByText('text a')).toBeInTheDocument()
  expect(screen.getByText('text c')).toBeInTheDocument()
  expect(screen.getByText('New messages')).toBeInTheDocument()
})

test('scrolling to the top loads history once at a time', async () => {
  let finish!: (v: boolean) => void
  const onLoadOlder = vi.fn(() => new Promise<boolean>((r) => (finish = r)))
  render(<Feed {...props({ has_more: true }, onLoadOlder)} />)
  const log = screen.getByRole('log')
  log.scrollTop = 0
  fireEvent.scroll(log)
  fireEvent.scroll(log)
  expect(onLoadOlder).toHaveBeenCalledTimes(1)
  finish(true)
})

test('empty channel says so', () => {
  render(<Feed {...props({ posts: [] })} />)
  expect(screen.getByText('No messages yet')).toBeInTheDocument()
})
