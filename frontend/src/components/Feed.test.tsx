import { act, fireEvent, render, screen } from '@testing-library/react'
import { StrictMode } from 'react'
import { vi } from 'vitest'
import type { ChannelDTO, PostView } from '../api/types'
import { setLocale } from '../i18n'
import { anchorNudge, Feed, pickAnchor } from './Feed'
import { Toast, TOAST_HOST } from './Toast'

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

// ---- history loads that leave the feed at the very top ----
//
// jsdom has no layout, so these tests drive it by hand: animation frames run
// only when flushed (exact, no timed waits), the scroller has a fixed
// geometry, post rows have a box (so the history anchor is really captured),
// and scrollTo is jsdom's no-op — the anchor restore is attempted but cannot
// move the feed, the failure seen in e2e ("history loads up to the first
// message": a page landed, the feed stayed at scrollTop 0 with has_more).

function manualFrames() {
  const pending = new Map<number, FrameRequestCallback>()
  let next = 1
  const raf = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
    pending.set(next, cb)
    return next++
  })
  const caf = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation((id) => void pending.delete(id))
  // flush runs the frames due now and the ones they schedule (bounded: the
  // virtualizer's own reconcile loop reschedules itself), then settles promises.
  const flush = async () => {
    for (let i = 0; i < 10 && pending.size > 0; i++) {
      const due = [...pending.values()]
      pending.clear()
      await act(async () => due.forEach((cb) => cb(performance.now())))
    }
    await act(async () => {})
  }
  return { flush, restore: () => (raf.mockRestore(), caf.mockRestore()) }
}

// A feed the user has scrolled to its top: tall content, scrollTop 0 that
// the restore cannot change, rows with a box below the viewport top.
function atTheTop(log: HTMLElement) {
  let top = 4400
  Object.defineProperties(log, {
    scrollHeight: { configurable: true, get: () => 5000 },
    clientHeight: { configurable: true, get: () => 600 },
    scrollTop: { configurable: true, get: () => top, set: (v: number) => void (top = v) },
  })
  const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const post = this.dataset.kind === 'post'
    return { top: post ? 10 : 0, bottom: post ? 50 : 0, left: 0, right: 0, width: 0, height: post ? 40 : 0, x: 0, y: 0, toJSON: () => ({}) } as DOMRect
  })
  return { setTop: (v: number) => void (top = v), restore: () => rect.mockRestore() }
}

const older = (n: number) => Array.from({ length: n }, (_, i) => P(`o${n - i}`, i % 2 ? 'carol' : 'bob', 100 + n - i))

async function openAtTop(variant: 'channel' | 'thread') {
  const frames = manualFrames()
  const onLoadOlder = vi.fn().mockResolvedValue(true)
  const p = { ...props({ has_more: true }, onLoadOlder), variant }
  const view = render(<Feed {...p} />)
  const log = screen.getByRole('log')
  const geo = atTheTop(log)
  await frames.flush() // the mount's frames
  onLoadOlder.mockClear()
  vi.mocked(HTMLElement.prototype.scrollTo).mockClear()
  geo.setTop(0) // the user reaches the top
  fireEvent.scroll(log)
  await frames.flush()
  expect(onLoadOlder).toHaveBeenCalledTimes(1)
  const land = async (posts: PostView[]) => {
    view.rerender(<Feed {...p} data={{ ...p.data, posts: [...posts, ...p.data.posts] }} />)
    await frames.flush()
  }
  const cleanup = () => (frames.restore(), geo.restore())
  return { log, onLoadOlder, geo, land, flush: frames.flush, cleanup }
}

// The page landed, the anchor restore was attempted and could not move the
// feed off the top; at scrollTop 0 neither scrollTo(0) nor the wheel fires a
// scroll event, so onScroll would never ask again — the fill path does
// (onScroll's own rule: within NEAR_TOP of the top with more history).
test.each(['channel', 'thread'] as const)('%s: a page that lands with the feed still at the top (failed restore) loads the next one without a scroll event', async (variant) => {
  const f = await openAtTop(variant)
  try {
    await f.land(older(2))
    expect(HTMLElement.prototype.scrollTo).toHaveBeenCalled() // the anchor restore was attempted
    expect(f.log.scrollTop).toBe(0) // …and could not move the feed
    expect(f.onLoadOlder).toHaveBeenCalledTimes(2)

    // Away from the top (a restore that worked), a new page asks for nothing more.
    f.geo.setTop(2000)
    await f.land(older(3))
    expect(f.onLoadOlder).toHaveBeenCalledTimes(2)
  } finally {
    f.cleanup()
  }
})

// Review of d1ba0ac: without a bound the fill path re-requested every frame
// (32 calls in 500 ms) when a load left everything as it was — Go dropped the
// page (stale generation or cursor) or the thread had nothing to page from.
test.each(['channel', 'thread'] as const)('%s: a load that changes nothing asks for no more pages until the user scrolls or wheels', async (variant) => {
  const f = await openAtTop(variant)
  try {
    await f.land([]) // the same rows again, the feed still at the top
    await f.land([])
    expect(f.onLoadOlder).toHaveBeenCalledTimes(1)

    fireEvent.wheel(f.log, { deltaY: -100 }) // a real gesture at the top (no scroll event there)
    await f.flush()
    expect(f.onLoadOlder).toHaveBeenCalledTimes(2)
  } finally {
    f.cleanup()
  }
})

// Even loads that do add rows continue on their own at most twice while the
// feed stays stuck at the top: the worst case of a restore that keeps
// failing is two extra pages, not the channel's whole history.
test.each(['channel', 'thread'] as const)('%s: loads continue on their own at most twice, then wait for the user', async (variant) => {
  const f = await openAtTop(variant)
  try {
    await f.land(older(2))
    await f.land(older(4))
    await f.land(older(6))
    await f.land(older(8))
    expect(f.onLoadOlder).toHaveBeenCalledTimes(3) // the user's + 2 automatic

    fireEvent.wheel(f.log, { deltaY: -100 })
    await f.flush()
    expect(f.onLoadOlder).toHaveBeenCalledTimes(4)
    await f.land(older(10)) // a fresh budget after the gesture
    expect(f.onLoadOlder).toHaveBeenCalledTimes(5)
  } finally {
    f.cleanup()
  }
})

// A thread's history lands *below* its root: the root heads the panel before
// and after the page, so it cannot anchor the restore. e2e (threads.spec, "a
// long thread … without gaps"), since the composer grew and the panel's feed
// got shorter: the load started with the root on screen, the restore kept
// the root in place — the feed stayed at the top, the page just loaded was
// skipped and the next one requested (77 of 150 replies ever shown). Real
// layout, by hand: each row sits at its virtualizer offset minus scrollTop,
// and scrollTo moves the feed.
test('thread: history that lands under the root keeps the reply seen at the top in place, not the root', async () => {
  const frames = manualFrames()
  const root: PostView = { ...P('R', 'bob', 60), reply_count: 20 }
  const reply = (n: number): PostView => ({ ...P(`r${n}`, n % 2 ? 'carol' : 'bob', 60 - n), root_id: 'R' })
  const replies = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, i) => reply(from + i))
  const onLoadOlder = vi.fn().mockResolvedValue(true)
  const p = { ...props({ has_more: true, posts: [root, ...replies(11, 20)] }, onLoadOlder), variant: 'thread' as const }
  const view = render(<Feed {...p} />)
  const log = screen.getByRole('log')
  let top = 0
  Object.defineProperties(log, {
    scrollHeight: { configurable: true, get: () => 5000 },
    clientHeight: { configurable: true, get: () => 600 },
    scrollTop: { configurable: true, get: () => top, set: (v: number) => void (top = Math.max(0, v)) },
  })
  log.scrollTo = ((o: ScrollToOptions) => void (top = Math.max(0, o.top ?? top))) as typeof log.scrollTo
  const rowTop = (el: HTMLElement) => Number(/translateY\((-?[\d.]+)px\)/.exec(el.style.transform)?.[1] ?? 0) - top
  const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const row = this.dataset.kind !== undefined
    const y = row ? rowTop(this) : 0
    const h = this === log ? 600 : row ? 40 : 0
    return { top: y, bottom: y + h, left: 0, right: 0, width: 0, height: h, x: 0, y, toJSON: () => ({}) } as DOMRect
  })
  const onScreen = (key: string) => {
    const el = log.querySelector<HTMLElement>(`[data-key="${key}"]`)
    return el ? rowTop(el) : null
  }
  try {
    await frames.flush() // the mount's frames
    top = 0 // the user reached the top: the root is on screen, the first loaded reply under it
    fireEvent.scroll(log)
    await frames.flush()
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
    const before = onScreen('r11')
    expect(before).not.toBeNull()
    expect(onScreen('R')).toBeLessThan(before!) // the root is above it, also on screen

    view.rerender(<Feed {...p} data={{ ...p.data, posts: [root, ...replies(1, 20)] }} />)
    await frames.flush()
    expect(onScreen('r11')).toBe(before) // the reply the user saw stays where it was
    expect(top).toBeGreaterThan(0) // the page just loaded is above, not skipped
  } finally {
    rect.mockRestore()
    frames.restore()
  }
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

// ---- bottom-stick on size changes ----
//
// The feed's own height changes without a new row — a banner above it, the
// composer growing with a multi-line draft, the window shrinking. Shrinking
// the scroller keeps scrollTop and fires no scroll event, so before the fix
// the last post slid below the fold (user report 2026-09-29: a download's
// banner pushed the last post under the composer). jsdom has neither layout
// nor ResizeObserver: the scroller's geometry is set by hand and every
// ResizeObserver created (Feed's and the virtualizer's own) is recorded, so
// a test can fire the ones watching a given element.

type Observed = { cb: ResizeObserverCallback; targets: Set<Element>; self: ResizeObserver; disconnected: boolean }
function recordResizeObservers() {
  const all: Observed[] = []
  const saved = (globalThis as { ResizeObserver?: unknown }).ResizeObserver
  class Stub {
    rec: Observed
    constructor(cb: ResizeObserverCallback) {
      this.rec = { cb, targets: new Set(), self: this as unknown as ResizeObserver, disconnected: false }
      all.push(this.rec)
    }
    observe(el: Element) {
      this.rec.targets.add(el)
    }
    unobserve(el: Element) {
      this.rec.targets.delete(el)
    }
    disconnect() {
      this.rec.targets.clear()
      this.rec.disconnected = true
    }
  }
  ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = Stub
  const fire = (el: Element, entry: Partial<ResizeObserverEntry> = {}) =>
    act(() => {
      for (const o of all) if (o.targets.has(el)) o.cb([{ target: el, ...entry } as unknown as ResizeObserverEntry], o.self)
    })
  const watching = (el: Element) => all.filter((o) => o.targets.has(el))
  return { fire, all, watching, restore: () => void ((globalThis as { ResizeObserver?: unknown }).ResizeObserver = saved) }
}

// A scroller with a settable geometry: scrollTop is clamped to the range,
// like a real one.
function geometry(log: HTMLElement, g: { scrollHeight: number; clientHeight: number; scrollTop: number }) {
  Object.defineProperties(log, {
    scrollHeight: { configurable: true, get: () => g.scrollHeight },
    clientHeight: { configurable: true, get: () => g.clientHeight },
    scrollTop: {
      configurable: true,
      get: () => g.scrollTop,
      set: (v: number) => void (g.scrollTop = Math.max(0, Math.min(v, g.scrollHeight - g.clientHeight))),
    },
  })
  return g
}

function stickFeed() {
  const ro = recordResizeObservers()
  const view = render(<Feed {...props({ new_since: 0 })} />)
  const log = screen.getByRole('log')
  const g = geometry(log, { scrollHeight: 2000, clientHeight: 600, scrollTop: 1400 })
  fireEvent.scroll(log) // at the bottom
  const sizer = log.lastElementChild as HTMLElement
  return { ...view, ro, log, g, sizer }
}

test('at the bottom, a shorter viewport (banner above, composer growing) keeps the feed at the bottom', () => {
  const { ro, log, g } = stickFeed()
  try {
    g.clientHeight = 500 // scrollTop stays 1400: 100px of the last post now below the fold
    ro.fire(log)
    expect(g.scrollTop).toBe(1500)
    g.clientHeight = 640 // and back: the range shrank, scrollTop clamps, still the bottom
    g.scrollTop = Math.min(g.scrollTop, g.scrollHeight - g.clientHeight)
    ro.fire(log)
    expect(g.scrollTop).toBe(1360)
  } finally {
    ro.restore()
  }
})

test('at the bottom, taller content with no new row (the last row re-measured) keeps the feed at the bottom', () => {
  const { ro, log, g } = stickFeed()
  try {
    const rows = log.querySelectorAll<HTMLElement>('[data-index]')
    const last = rows[rows.length - 1]
    g.scrollHeight = 2120
    // the virtualizer's own row observer reports the last row 120 px taller
    ro.fire(last, { borderBoxSize: [{ blockSize: 160, inlineSize: 800 }] as unknown as ResizeObserverSize[] })
    expect(g.scrollTop).toBe(1520)
  } finally {
    ro.restore()
  }
})

test('away from the bottom, a viewport resize leaves scrollTop alone (the rows on screen stay put)', () => {
  const { ro, log, g } = stickFeed()
  try {
    g.scrollTop = 700
    fireEvent.scroll(log) // the user scrolled up
    g.clientHeight = 500
    ro.fire(log)
    expect(g.scrollTop).toBe(700)
    g.scrollHeight = 2300
    const rows = log.querySelectorAll<HTMLElement>('[data-index]')
    ro.fire(rows[rows.length - 1], { borderBoxSize: [{ blockSize: 340, inlineSize: 800 }] as unknown as ResizeObserverSize[] })
    expect(g.scrollTop).toBe(700)
  } finally {
    ro.restore()
  }
})

test('during a wheel gesture a resize does not pin; once the gesture is idle the feed goes back to the bottom if it is still there', () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const { ro, log, g } = stickFeed()
  try {
    fireEvent.wheel(log, { deltaY: 40 }) // wheeling down at the bottom: nothing moves
    g.clientHeight = 500
    ro.fire(log)
    expect(g.scrollTop).toBe(1400) // no script write mid-gesture (WebKitGTK cancels its wheel animation on one)
    act(() => void vi.advanceTimersByTime(200)) // SCROLL_IDLE_MS
    expect(g.scrollTop).toBe(1500)
  } finally {
    ro.restore()
    vi.useRealTimers()
  }
})

test('a wheel gesture that leaves the bottom is not pulled back when it ends', () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const { ro, log, g } = stickFeed()
  try {
    fireEvent.wheel(log, { deltaY: -200 })
    g.clientHeight = 500
    ro.fire(log)
    g.scrollTop = 1100
    fireEvent.scroll(log) // the wheel scrolled up
    act(() => void vi.advanceTimersByTime(200))
    expect(g.scrollTop).toBe(1100)
  } finally {
    ro.restore()
    vi.useRealTimers()
  }
})

test('the bottom-stick observer is disconnected on unmount', () => {
  const { ro, log, unmount } = stickFeed()
  try {
    // Feed's own observer is the first one on the scroller: its layout
    // effect runs before useVirtualizer's (declared earlier in Feed)
    const mine = ro.watching(log)[0]
    expect(mine).toBeDefined()
    unmount()
    expect(mine.disconnected).toBe(true)
  } finally {
    ro.restore()
  }
})

test('the rows\' container is not observed (resized synchronously from the virtualizer\'s row observer: a loop error)', () => {
  const { ro, sizer } = stickFeed()
  try {
    expect(ro.watching(sizer)).toHaveLength(0)
  } finally {
    ro.restore()
  }
})

test('a feed opened at the "new messages" line is not at its bottom: a resize does not pull it down', () => {
  const ro = recordResizeObservers()
  try {
    render(<Feed {...props()} />) // new_since puts the "new messages" line in the rows
    const log = screen.getByRole('log')
    const g = geometry(log, { scrollHeight: 2000, clientHeight: 600, scrollTop: 900 })
    g.clientHeight = 500
    ro.fire(log)
    expect(g.scrollTop).toBe(900)
  } finally {
    ro.restore()
  }
})

test('before the first rows (still loading) a resize writes nothing', () => {
  const ro = recordResizeObservers()
  try {
    render(<Feed {...props({ posts: [], loaded: false })} />)
    const log = screen.getByRole('log')
    const g = geometry(log, { scrollHeight: 1000, clientHeight: 600, scrollTop: 100 })
    g.clientHeight = 500
    ro.fire(log)
    expect(g.scrollTop).toBe(100)
  } finally {
    ro.restore()
  }
})

test('a feed given a toast-host priority hosts the toast in its box, not inside the scrolling content', () => {
  render(
    <>
      <Feed {...props()} toastHost={TOAST_HOST.channel} />
      <Toast />
    </>,
  )
  const region = screen.getByTestId('toast-region')
  expect(screen.getByRole('log')).not.toContainElement(region)
  expect(screen.getByRole('log').parentElement).toContainElement(region)
})

// The scroll event of our own pin arrives a frame later, measured against a
// layout that may have shrunk again meanwhile (the composer growing by more
// than NEAR_BOTTOM at once — a row of attachment chips): it must not read as
// "the user left the bottom" — the feed did not move up, only its size
// changed.
test('the scroll event of our own pin, measured after a further shrink, keeps the feed at the bottom', () => {
  const { ro, log, g } = stickFeed()
  try {
    g.clientHeight = 580
    ro.fire(log)
    expect(g.scrollTop).toBe(1420) // pinned
    g.clientHeight = 520 // the composer grew again before the pin's scroll event
    fireEvent.scroll(log) // scrollTop 1420, distance 60
    ro.fire(log)
    expect(g.scrollTop).toBe(1480)
  } finally {
    ro.restore()
  }
})

test('a real scroll up (scrollTop decreased) still leaves the bottom', () => {
  const { ro, log, g } = stickFeed()
  try {
    g.scrollTop = 1340 // up by 60
    fireEvent.scroll(log)
    g.clientHeight = 500
    ro.fire(log)
    expect(g.scrollTop).toBe(1340)
  } finally {
    ro.restore()
  }
})

// "ResizeObserver loop completed with undelivered notifications" while typing
// in the composer (stick-bottom.spec "away from the bottom, a composer
// growing…", 5–11 of 16 runs in Chromium, composer fix round 1): the
// scroller got shorter, the virtualizer's rect observer recomputed the range
// and — mid-scroll, when its notify is a flushSync — unmounted the row that
// left it right inside the observer delivery. The removed row is still
// observed by the virtualizer's row observer; detached, it sits shallower
// than the scroller, so the browser had to skip its notification (probe: 28
// rows before that callback, 27 after it, the error at the end of the same
// delivery). The rect is taken in the next frame instead, outside delivery.
test('a shorter scroller changes the rendered rows in the next frame, not inside the resize observer delivery', async () => {
  const frames = manualFrames()
  const ro = recordResizeObservers()
  try {
    const posts = Array.from({ length: 60 }, (_, i) => P(`m${i}`, i % 2 ? 'carol' : 'bob', 60 - i))
    render(<Feed {...props({ posts, new_since: 0 })} />)
    await frames.flush()
    const log = screen.getByRole('log')
    geometry(log, { scrollHeight: 2400, clientHeight: 600, scrollTop: 1000 })
    fireEvent.scroll(log) // the virtualizer is "scrolling": its notify is a flushSync
    await frames.flush()
    fireEvent.scroll(log)
    const rendered = () => log.querySelectorAll('[data-index]').length
    const before = rendered()
    const rect = ro.watching(log).filter((o) => o !== ro.watching(log)[0]) // [0] is the bottom-stick's (see above)
    expect(rect).toHaveLength(1)
    // Not inside act(): what the callback itself does to the DOM is the point.
    rect[0].cb([{ target: log, borderBoxSize: [{ blockSize: 200, inlineSize: 800 }] } as unknown as ResizeObserverEntry], rect[0].self)
    expect(rendered()).toBe(before)
    await frames.flush()
    expect(rendered()).toBeLessThan(before) // the next frame takes the new size
  } finally {
    ro.restore()
    frames.restore()
  }
})
