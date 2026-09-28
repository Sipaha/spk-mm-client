import { fireEvent, render, screen, waitFor } from '@testing-library/react'
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
  data: channel(o), variant: 'channel' as const, serverId: 1, me: { id: 'me', username: 'me' }, locale: 'en-US',
  actions: {
    link: vi.fn(), retry: vi.fn(), discard: vi.fn(), edit: vi.fn(), saveEdit: vi.fn().mockResolvedValue(undefined),
    cancelEdit: vi.fn(), remove: vi.fn(), markUnread: vi.fn(), save: vi.fn(), copyLink: vi.fn(),
    view: vi.fn(), download: vi.fn(), open: vi.fn(), react: vi.fn(), openThread: vi.fn(),
    emojiInfo: vi.fn().mockResolvedValue({ recent: [], custom: [], custom_enabled: false }),
    reactionUsers: vi.fn().mockResolvedValue({ users: [], unknown: 0 }),
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

// Carry from the jump-to-latest review: the empty-state placeholder is
// `absolute inset-0` and must position against the scroller itself, not
// whatever the outer (button-hosting) wrapper happens to be.
test('the scroller is a positioned ancestor for the empty-state placeholder', () => {
  render(<Feed {...props({ posts: [] })} />)
  expect(screen.getByRole('log')).toHaveClass('relative')
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

// A history page that lands while the feed is still at the very top (its
// anchor restore could not move it — e2e "history loads up to the first
// message" caught it stuck at scrollTop 0 with has_more) must not wait for a
// scroll event: at scrollTop 0 another scrollTo(0) or wheel-up fires none,
// so the next page would never load. The rule is onScroll's own (within
// NEAR_TOP of the top with more history → load), applied after the rows
// change too.
test('a page that lands with the feed still at the top loads the next one without a scroll event', async () => {
  const onLoadOlder = vi.fn().mockResolvedValue(true)
  const p = props({ has_more: true }, onLoadOlder)
  // jsdom has no layout: the scroller's geometry is set by hand, as it is
  // after the first rows in a browser — taller than the viewport (not the
  // "page does not fill the viewport" case) and scrolled to its end.
  let top = 4400
  const geometry = {
    scrollHeight: { configurable: true, get: () => 5000 },
    clientHeight: { configurable: true, get: () => 600 },
    scrollTop: { configurable: true, get: () => top, set: (v: number) => void (top = v) },
  }
  const { rerender } = render(<Feed {...p} />)
  const log = screen.getByRole('log')
  Object.defineProperties(log, geometry) // scrollTo is a no-op here: the restore cannot move the feed
  await new Promise((r) => setTimeout(r, 50)) // the mount's frames
  onLoadOlder.mockClear()

  top = 0 // the user reaches the top
  fireEvent.scroll(log)
  await waitFor(() => expect(onLoadOlder).toHaveBeenCalledTimes(1))

  // The page lands; the feed is still at scrollTop 0 and no scroll event follows.
  rerender(<Feed {...p} data={{ ...p.data, posts: [P('o1', 'bob', 90), P('o2', 'carol', 80), ...p.data.posts] }} />)
  await waitFor(() => expect(onLoadOlder).toHaveBeenCalledTimes(2))

  // Away from the top (a restore that worked), a new page asks for nothing more.
  top = 2000
  rerender(<Feed {...p} data={{ ...p.data, posts: [P('o0', 'bob', 95), P('o1', 'bob', 90), P('o2', 'carol', 80), ...p.data.posts] }} />)
  await new Promise((r) => setTimeout(r, 50))
  expect(onLoadOlder).toHaveBeenCalledTimes(2)
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
    let posts = p.data.posts
    for (let i = 0; i < 20; i++) {
      posts = [...posts, P(`n${i}`, 'bob', 0)]
      rerender(<Feed {...p} data={{ ...p.data, posts }} />)
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

// Sets the scroller's scrollHeight/clientHeight (jsdom never computes real
// layout) so distance-from-bottom math in Feed's onScroll is meaningful, then
// fires a scroll event — the same path a real wheel/touch scroll drives.
function scrollAway(log: HTMLElement, distanceFromBottom: number, clientHeight = 600) {
  Object.defineProperty(log, 'scrollHeight', { value: distanceFromBottom + clientHeight, configurable: true })
  Object.defineProperty(log, 'clientHeight', { value: clientHeight, configurable: true })
  log.scrollTop = 0
  fireEvent.scroll(log)
}

// Carry from the jump-to-latest review: a scroll event that lands within
// NEAR_BOTTOM must genuinely decide "hidden", not merely never have fired.
test('jump-to-latest button: hidden while genuinely at the bottom', () => {
  render(<Feed {...props()} />)
  const log = screen.getByRole('log')
  scrollAway(log, 10) // within NEAR_BOTTOM (48px)
  expect(screen.queryByRole('button', { name: 'Jump to latest messages' })).not.toBeInTheDocument()
})

test('jump-to-latest button: shown after scrolling up more than a viewport', () => {
  render(<Feed {...props()} />)
  const log = screen.getByRole('log')
  scrollAway(log, 700) // more than the 600px viewport away from the bottom
  expect(screen.getByRole('button', { name: 'Jump to latest messages' })).toBeInTheDocument()
})

test('jump-to-latest button: clicking it scrolls to the end and hides the button', () => {
  // scrollTo is already a vi.fn() no-op stub (beforeAll, for jsdom's lack of layout).
  const scrollTo = HTMLElement.prototype.scrollTo as unknown as ReturnType<typeof vi.fn>
  render(<Feed {...props()} />)
  const log = screen.getByRole('log')
  scrollAway(log, 700)
  const before = scrollTo.mock.calls.length
  fireEvent.click(screen.getByRole('button', { name: 'Jump to latest messages' }))
  expect(scrollTo.mock.calls.length).toBeGreaterThan(before) // the feed's own scroll path (scrollToIndex → elementScroll)
  expect(screen.queryByRole('button', { name: /Jump to latest messages/ })).not.toBeInTheDocument()
})

test('jump-to-latest button: loading older history while away does not inflate the badge', () => {
  // Regression (e2e-caught): scrolling up in a long channel loads pages of
  // *older* history, which also newly appear in `rows` — those must not
  // count as "arrived while away", only posts newer than anything seen so far.
  const p = props({ has_more: true })
  const { rerender } = render(<Feed {...p} />)
  const log = screen.getByRole('log')
  scrollAway(log, 700)
  expect(screen.queryByRole('button', { name: /new/ })).not.toBeInTheDocument()

  // An older page lands above (smaller minAgo → further in the past, prepended).
  const older = [P('o1', 'bob', 90), P('o2', 'carol', 80), P('o3', 'bob', 70)]
  const posts = [...older, ...p.data.posts]
  rerender(<Feed {...p} data={{ ...p.data, posts }} />)
  expect(screen.queryByRole('button', { name: /new/ })).not.toBeInTheDocument()

  // A genuinely new post at the live end still counts.
  const withNew = [...posts, P('n1', 'bob', 0)]
  rerender(<Feed {...p} data={{ ...p.data, posts: withNew }} />)
  expect(screen.getByRole('button', { name: 'Jump to latest messages — 1 new' })).toBeInTheDocument()
})

test('jump-to-latest button: badge counts only others’ posts that arrived while away, and resets at the bottom', () => {
  const p = props()
  const { rerender } = render(<Feed {...p} />)
  const log = screen.getByRole('log')
  scrollAway(log, 700)
  expect(screen.queryByRole('button', { name: /new/ })).not.toBeInTheDocument()

  // bob's post arrives while away: counted
  let posts = [...p.data.posts, P('n1', 'bob', 0)]
  rerender(<Feed {...p} data={{ ...p.data, posts }} />)
  expect(screen.getByRole('button', { name: 'Jump to latest messages — 1 new' })).toBeInTheDocument()

  // my own post arrives too: not counted
  posts = [...posts, P('n2', 'me', 0)]
  rerender(<Feed {...p} data={{ ...p.data, posts }} />)
  expect(screen.getByRole('button', { name: 'Jump to latest messages — 1 new' })).toBeInTheDocument()

  // carol's post: counted again
  posts = [...posts, P('n3', 'carol', 0)]
  rerender(<Feed {...p} data={{ ...p.data, posts }} />)
  expect(screen.getByRole('button', { name: 'Jump to latest messages — 2 new' })).toBeInTheDocument()

  // reaching the bottom by scrolling (not the button) resets the count and hides the button
  scrollAway(log, 10)
  expect(screen.queryByRole('button', { name: /Jump to latest messages/ })).not.toBeInTheDocument()
  scrollAway(log, 700)
  expect(screen.queryByRole('button', { name: /new/ })).not.toBeInTheDocument()
})

test('jump-to-latest button: badge caps the displayed count at 99+', () => {
  const p = props()
  const { rerender } = render(<Feed {...p} />)
  const log = screen.getByRole('log')
  scrollAway(log, 700)
  let posts = p.data.posts
  for (let i = 0; i < 100; i++) posts = [...posts, P(`x${i}`, 'bob', 0)]
  rerender(<Feed {...p} data={{ ...p.data, posts }} />)
  const button = screen.getByRole('button', { name: 'Jump to latest messages — 100 new' })
  expect(button).toHaveTextContent('99+')
})

test('jump-to-latest button: aria-label and title, with and without a badge', () => {
  const p = props()
  const { rerender } = render(<Feed {...p} />)
  const log = screen.getByRole('log')
  scrollAway(log, 700)
  const plain = screen.getByRole('button', { name: 'Jump to latest messages' })
  expect(plain).toHaveAttribute('title', 'Jump to latest messages')

  const posts = [...p.data.posts, P('n1', 'bob', 0)]
  rerender(<Feed {...p} data={{ ...p.data, posts }} />)
  const withBadge = screen.getByRole('button', { name: 'Jump to latest messages — 1 new' })
  expect(withBadge).toHaveAttribute('title', 'Jump to latest messages — 1 new')
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
