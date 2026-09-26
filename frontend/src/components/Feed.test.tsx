import { fireEvent, render, screen } from '@testing-library/react'
import { StrictMode } from 'react'
import { vi } from 'vitest'
import type { ChannelDTO, PostView } from '../api/types'
import { setLocale } from '../i18n'
import { anchorNudge, Feed, pickAnchor } from './Feed'

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
  channel: channel(o), serverId: 1, me: { id: 'me', username: 'me' }, locale: 'en-US',
  actions: {
    link: vi.fn(), retry: vi.fn(), discard: vi.fn(), edit: vi.fn(), saveEdit: vi.fn().mockResolvedValue(undefined),
    cancelEdit: vi.fn(), remove: vi.fn(), markUnread: vi.fn(), copyLink: vi.fn(),
    view: vi.fn(), download: vi.fn(), open: vi.fn(), react: vi.fn(),
    emojiInfo: vi.fn().mockResolvedValue({ recent: [], custom: [], custom_enabled: false }),
  },
  editingId: null,
  onLoadOlder,
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

test('anchorNudge: converged within tolerance returns null, otherwise the delta to add to scrollTop', () => {
  expect(anchorNudge(100, 100)).toBeNull()
  expect(anchorNudge(100.4, 100)).toBeNull() // within the default 1px tolerance
  expect(anchorNudge(101, 100)).toBeNull() // exactly at the tolerance boundary
  expect(anchorNudge(124, 100)).toBe(24) // measured lower on screen than target: scroll down (increase scrollTop) by 24
  expect(anchorNudge(76, 100)).toBe(-24) // measured higher than target: scroll up (decrease scrollTop) by 24
  expect(anchorNudge(105, 100, 10)).toBeNull() // a wider tolerance converges sooner
})

test('pickAnchor: the topmost post row still (partly) visible below the viewport top, with its offset', () => {
  const viewTop = 100
  // Overscanned rows above the viewport (bottom <= viewTop) must be skipped,
  // and DOM order must not matter — only on-screen position does.
  const boxes = [
    { key: 'c', top: 150, bottom: 190 },
    { key: 'a', top: 20, bottom: 60 }, // fully above the viewport (overscan)
    { key: 'edge', top: 60, bottom: 100 }, // bottom exactly at the viewport top: not visible
    { key: 'b', top: 84, bottom: 150 }, // partly visible: this is what the user sees at the top
  ]
  expect(pickAnchor(boxes, viewTop)).toEqual({ key: 'b', offset: -16 })
  expect(pickAnchor([{ key: 'a', top: 20, bottom: 60 }], viewTop)).toBeNull() // nothing on screen
  expect(pickAnchor([], viewTop)).toBeNull()
})

test('the history row keeps its box while loading: the label is only hidden, never removed', async () => {
  // The scroll anchor is captured just before the loading label appears; if
  // the row above it grew when the label rendered, everything below would
  // shift after the capture and the restore would land off by that much.
  let finish!: (v: boolean) => void
  const onLoadOlder = vi.fn(() => new Promise<boolean>((r) => (finish = r)))
  render(<Feed {...props({ has_more: true }, onLoadOlder)} />)
  const label = screen.getByText('Loading history…')
  expect(label).toHaveClass('invisible') // present (occupies its line) but not shown
  const log = screen.getByRole('log')
  log.scrollTop = 0
  fireEvent.scroll(log)
  expect(await screen.findByText('Loading history…')).not.toHaveClass('invisible')
  finish(true)
})

// A hidden window (closed to the tray, minimized, screen off) never paints,
// so the engine never runs animation-frame callbacks: every callback still
// scheduled keeps its closure — and through it an unmounted feed's DOM and
// channel — alive until the next paint (WebKitWebProcess grew ~0.6 MB per
// channel switch in the soak). Simulate that page: frames are queued,
// never run.
function neverPaintingPage() {
  const pending = new Map<number, FrameRequestCallback>()
  let next = 1
  const raf = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
    pending.set(next, cb)
    return next++
  })
  const caf = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation((id) => {
    pending.delete(id)
  })
  return { pending, restore: () => (raf.mockRestore(), caf.mockRestore()) }
}

test('a feed that never paints does not pile up frame callbacks as posts arrive', () => {
  const page = neverPaintingPage()
  try {
    const p = props({ has_more: true })
    const { rerender } = render(<Feed {...p} />)
    let posts = p.channel.posts
    for (let i = 0; i < 20; i++) {
      posts = [...posts, P(`n${i}`, 'bob', 0)]
      rerender(<Feed {...p} channel={{ ...p.channel, posts }} />)
    }
    // at most one pending frame per purpose (ours and the virtualizer's), not one per post
    expect(page.pending.size).toBeLessThanOrEqual(3)
  } finally {
    page.restore()
  }
})

test('an unmounted feed leaves no frame callback behind', () => {
  const page = neverPaintingPage()
  try {
    const { unmount } = render(<Feed {...props({ has_more: true })} />)
    expect(page.pending.size).toBeGreaterThan(0) // the viewport-fill check waits for a frame
    unmount()
    expect(page.pending.size).toBe(0)
  } finally {
    page.restore()
  }
})

test('an unmounted feed leaves its scroller empty', async () => {
  // WebKit queues a scroll event for every element scrolled by script and
  // keeps the element until the next paint; a hidden window never paints,
  // so each switched-away feed would stay alive with all its rows.
  const { unmount } = render(<Feed {...props()} />)
  const log = screen.getByRole('log')
  expect(log.querySelector('article')).not.toBeNull()
  unmount()
  await Promise.resolve()
  expect(log.isConnected).toBe(false)
  expect(log.childNodes.length).toBe(0)
})

test('StrictMode keeps the rows of a feed that stays mounted', async () => {
  render(
    <StrictMode>
      <Feed {...props()} />
    </StrictMode>,
  )
  await Promise.resolve()
  expect(screen.getByText('text c')).toBeInTheDocument()
})
