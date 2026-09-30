import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { Profiler, StrictMode } from 'react'
import { vi } from 'vitest'
import { ApiError } from '../api/client'
import type { Attachment, AttachmentField, ChannelDTO, FileView, HistGap, PostView } from '../api/types'
import { setLocale } from '../i18n'
import type { Row } from './feedRows'
import { anchorNudge, CARD_CHROME, estimate, Feed, FILE_CARD_H, pickAnchor } from './Feed'
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
  new_since: now - 10 * 60_000, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'me', crt: false, muted: false, gap: { open: false, gen: 0, before_id: '', stale: false }, hist_rev: 0,
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

// estimate: the virtualizer's pre-mount row-height guess (measureElement
// corrects it after mount) — review of 96cdfc8, Important 1: a post with
// message_attachments must add the new AttachmentView card's height
// (chrome + a rough per-line term), not just the flat head/non-head base,
// or the estimate-vs-real gap for exactly the Jenkins/CI webhook shape the
// density brief targets grows ~8x (was ~4px of unaccounted overhead per
// attachment, is now ~36px of card chrome alone).
const postRow = (post: PostView, head = true): Row => ({ kind: 'post', key: post.id, post, head })
const post = (o: Partial<PostView> = {}): PostView => ({ id: 'p1', user_id: 'u1', author: 'bob', message: 'hi', create_at: 0, ...o })

test('estimate: a plain post (no attachments) is unchanged — flat head/non-head base', () => {
  expect(estimate(postRow(post()))).toBe(64)
  expect(estimate(postRow(post(), false))).toBe(28)
  expect(estimate({ kind: 'day', key: 'd', ms: 0 })).toBe(36)
  expect(estimate({ kind: 'new', key: 'new' })).toBe(36)
  expect(estimate({ kind: 'gapAfter', key: 'gap' })).toBe(36)
  expect(estimate({ kind: 'gap', key: 'gap:1', open: true, stale: false })).toBe(36)
  expect(estimate({ kind: 'more', key: 'more' })).toBe(36)
})

test('estimate: one attachment adds its card chrome plus a per-field/line term', () => {
  const titleOnly: Attachment = { title: 'Build #6' }
  // CARD_CHROME (36) + title (20) = 56
  expect(estimate(postRow(post({ attachments: [titleOnly] })))).toBe(64 + CARD_CHROME + 20)

  const jenkinsStyle: Attachment = {
    color: '#00c100',
    title: 'sample-app - build completed - 006',
    text: 'Branch: **master**\nVersion: **1.1.2**', // 2 lines
    fields: [{ title: 'Changes', value: '- one\n- two', short: false }], // 1 full-width field row
    footer: 'build #006',
  }
  // 36 (chrome) + 20 (title) + 40 (2 text lines * 20) + 40 (1 full-width field row) + 18 (footer) = 154
  expect(estimate(postRow(post({ attachments: [jenkinsStyle] })))).toBe(64 + 36 + 20 + 40 + 40 + 18)
})

test('estimate: fields — a full-width field costs its own row, short fields pack two per row (re-review of fix round 1, Minor 2)', () => {
  const short = (n: string): AttachmentField => ({ title: n, value: n, short: true })
  const fullWidth = (n: string): AttachmentField => ({ title: n, value: n, short: false })

  // 3 short fields → ceil(3/2) = 2 rows of 40px.
  const threeShort: Attachment = { fields: [short('a'), short('b'), short('c')] }
  expect(estimate(postRow(post({ attachments: [threeShort] })))).toBe(64 + CARD_CHROME + 2 * 40)

  // 5 full-width fields → 5 rows of 40px each (the re-review's own worst case
  // for the old formula, which under-counted this by 114px).
  const fiveFullWidth: Attachment = { fields: [1, 2, 3, 4, 5].map((n) => fullWidth(String(n))) }
  expect(estimate(postRow(post({ attachments: [fiveFullWidth] })))).toBe(64 + CARD_CHROME + 5 * 40)

  // A mix: 1 full-width row + 2 short fields packed into 1 row.
  const mixed: Attachment = { fields: [fullWidth('x'), short('a'), short('b')] }
  expect(estimate(postRow(post({ attachments: [mixed] })))).toBe(64 + CARD_CHROME + 40 + 40)
})

test('estimate: several attachments on one post sum their individual estimates', () => {
  const a: Attachment = { title: 'one' } // 36 + 20 = 56
  const b: Attachment = { title: 'two', footer: 'f' } // 36 + 20 + 18 = 74
  expect(estimate(postRow(post({ attachments: [a, b] }), false))).toBe(28 + 56 + 74)
})

// estimate: file-cards brief (2026-09-30) review, fix round 1 — estimate()
// never accounted for post.files at all; a file **card** (fileKind 'other'/
// 'pdf', Attachments.tsx's `others` group — the kind FileCard.tsx now
// renders at a fixed 320x64, wrapping) needs its own term, the same way an
// AttachmentView card does above. Images/video/audio/text/markdown are
// deliberately still not estimated (their own fixed-box sizes vary too much
// for a cheap guess, and this gap predates the brief) — only the plain
// "everything else" card, which is what changed height here.
const zip = (id: string): FileView => ({ id, name: `${id}.zip`, ext: 'zip', mime: 'application/zip', size: 100 })
const pdf = (id: string): FileView => ({ id, name: `${id}.pdf`, ext: 'pdf', mime: 'application/pdf', size: 100 })
const png = (id: string): FileView => ({ id, name: `${id}.png`, ext: 'png', mime: 'image/png', size: 100 }) // not a card

test('estimate: file cards add FILE_CARD_H per wrapped row (2 per row at the nominal feed width), not the flat base alone', () => {
  expect(estimate(postRow(post({ files: [zip('a')] })))).toBe(64 + FILE_CARD_H) // 1 card: 1 row
  expect(estimate(postRow(post({ files: [zip('a'), pdf('b')] })))).toBe(64 + FILE_CARD_H) // 2 cards: still 1 row
  // 3 cards: 2 rows (2 + 1), with the row gap (8px) between them.
  expect(estimate(postRow(post({ files: [zip('a'), pdf('b'), zip('c')] })))).toBe(64 + 2 * FILE_CARD_H + 8)
  // 4 cards: 2 full rows, same gap.
  expect(estimate(postRow(post({ files: [zip('a'), zip('b'), zip('c'), zip('d')] })))).toBe(64 + 2 * FILE_CARD_H + 8)
})

test('estimate: files that are not cards (e.g. an image, rendered as a thumbnail) add nothing', () => {
  expect(estimate(postRow(post({ files: [png('a')] })))).toBe(64)
  expect(estimate(postRow(post({ files: [] })))).toBe(64)
  expect(estimate(postRow(post({ files: undefined })))).toBe(64)
})

test('estimate: file cards and message_attachments on the same post both add their own term', () => {
  const a: Attachment = { title: 'one' } // 36 + 20 = 56
  expect(estimate(postRow(post({ attachments: [a], files: [zip('a')] })))).toBe(64 + 56 + FILE_CARD_H)
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
  // A page applied moves hist_rev (the anchor waits for it); a page Go
  // dropped (no posts) does not.
  let rev = p.data.hist_rev
  const land = async (posts: PostView[]) => {
    if (posts.length) rev++
    view.rerender(<Feed {...p} data={{ ...p.data, posts: [...posts, ...p.data.posts], hist_rev: rev }} />)
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

// A thread panel of `height` px with a hand-driven layout: each row sits at
// its range offset plus preceding flow rows minus scrollTop (40 px rows), scrollTo moves the
// feed. The root plus replies r11..r20 are loaded; more history is pending.
async function threadRig(height: number) {
  const frames = manualFrames()
  const root: PostView = { ...P('R', 'bob', 60), reply_count: 20 }
  const reply = (n: number): PostView => ({ ...P(`r${n}`, n % 2 ? 'carol' : 'bob', 60 - n), root_id: 'R' })
  const replies = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, i) => reply(from + i))
  const onLoadOlder = vi.fn().mockResolvedValue(true)
  // A plain thread: no history revision (the restore follows the next rows update).
  const p = { ...props({ has_more: true, posts: [root, ...replies(11, 20)], hist_rev: undefined }, onLoadOlder), variant: 'thread' as const }
  const view = render(<Feed {...p} />)
  const log = screen.getByRole('log')
  const st = { top: 0 }
  Object.defineProperties(log, {
    scrollHeight: { configurable: true, get: () => 5000 },
    clientHeight: { configurable: true, get: () => height },
    scrollTop: { configurable: true, get: () => st.top, set: (v: number) => void (st.top = Math.max(0, v)) },
  })
  log.scrollTo = ((o: ScrollToOptions) => void (st.top = Math.max(0, o.top ?? st.top))) as typeof log.scrollTo
  const rowTop = (el: HTMLElement) => {
    const range = el.parentElement!
    const start = Number(/translateY\((-?[\d.]+)px\)/.exec(range.style.transform)?.[1] ?? 0)
    const index = [...range.children].indexOf(el)
    return start + index * 40 - st.top
  }
  const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const row = this.dataset.kind !== undefined
    const y = row ? rowTop(this) : 0
    const h = this === log ? height : row ? 40 : 0
    return { top: y, bottom: y + h, left: 0, right: 0, width: 0, height: h, x: 0, y, toJSON: () => ({}) } as DOMRect
  })
  const onScreen = (key: string) => {
    const el = log.querySelector<HTMLElement>(`[data-key="${key}"]`)
    return el ? rowTop(el) : null
  }
  const scrollTo = async (top: number) => {
    st.top = top
    fireEvent.scroll(log)
    await frames.flush()
  }
  const loadPage = async () => {
    view.rerender(<Feed {...p} data={{ ...p.data, posts: [root, ...replies(1, 20)] }} />)
    await frames.flush()
  }
  await frames.flush() // the mount's frames
  return {
    st, onLoadOlder, onScreen, scrollTo, loadPage,
    restore: () => (rect.mockRestore(), frames.restore()),
  }
}

// threads.spec "a long thread … without gaps" failed once the composer grew
// and the panel got shorter: the load started with the root on screen, the
// restore kept the root in place — the feed stayed at the top, the page just
// loaded was skipped and the next one requested (77 of 150 replies shown).
test('thread: history that lands under the root keeps the reply seen at the top in place, not the root', async () => {
  const t = await threadRig(600)
  try {
    await t.scrollTo(0) // the user reached the top: the root is on screen, the first loaded reply under it
    expect(t.onLoadOlder).toHaveBeenCalledTimes(1)
    const before = t.onScreen('r11')
    expect(before).not.toBeNull()
    expect(t.onScreen('R')).toBeLessThan(before!) // the root is above it, also on screen
    await t.loadPage()
    expect(t.onScreen('r11')).toBe(before) // the reply the user saw stays where it was
    expect(t.st.top).toBeGreaterThan(0) // the page just loaded is above, not skipped
  } finally {
    t.restore()
  }
})

// Re-review I-1: with only the root on screen (a tall root, a short panel),
// skipping the root anchored the first reply below the fold (overscan), and
// keeping that one in place pushed the root the user was reading off screen.
test('thread: history loaded while only the root is on screen keeps the root in place', async () => {
  const t = await threadRig(100)
  try {
    await t.scrollTo(60) // the root is on screen; the first loaded reply starts at the bottom edge
    expect(t.onLoadOlder).toHaveBeenCalledTimes(1)
    const before = t.onScreen('R')
    expect(before).not.toBeNull()
    expect(before!).toBeLessThan(100)
    expect(t.onScreen('r11')).toBeGreaterThanOrEqual(100) // no reply is on screen
    await t.loadPage()
    expect(t.onScreen('R')).toBe(before) // the root the user was reading does not move
  } finally {
    t.restore()
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

type Observed = { cb: ResizeObserverCallback; targets: Set<Element>; boxes: Map<Element, string | undefined>; self: ResizeObserver; disconnected: boolean }
function recordResizeObservers() {
  const all: Observed[] = []
  const saved = (globalThis as { ResizeObserver?: unknown }).ResizeObserver
  class Stub {
    rec: Observed
    constructor(cb: ResizeObserverCallback) {
      this.rec = { cb, targets: new Set(), boxes: new Map(), self: this as unknown as ResizeObserver, disconnected: false }
      all.push(this.rec)
    }
    observe(el: Element, opts?: ResizeObserverOptions) {
      this.rec.targets.add(el)
      this.rec.boxes.set(el, opts?.box)
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
  // Who is who on the scroller, whatever the effects' order: the
  // virtualizer's rect observer asks for the border box
  // (virtual-core's observeElementRect), Feed's bottom-stick passes no options.
  const virtualizerRect = (el: Element) => watching(el).filter((o) => o.boxes.get(el) === 'border-box')
  const bottomStick = (el: Element) => watching(el).filter((o) => o.boxes.get(el) === undefined)
  return { fire, all, watching, virtualizerRect, bottomStick, restore: () => void ((globalThis as { ResizeObserver?: unknown }).ResizeObserver = saved) }
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
    const [mine, ...more] = ro.bottomStick(log)
    expect(mine).toBeDefined()
    expect(more).toHaveLength(0)
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
    const rect = ro.virtualizerRect(log)
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

test('a batch of row resizes commits together instead of rendering once per measured row', async () => {
  const ro = recordResizeObservers()
  let commits = 0
  const posts = Array.from({ length: 20 }, (_, i) => P(`batch-${i}`, 'bob', 30 - i))
  const view = render(<Profiler id="feed" onRender={() => { commits++ }}><Feed {...props({ posts, new_since: 0 })} /></Profiler>)
  try {
    const log = screen.getByRole('log')
    geometry(log, { scrollHeight: 3000, clientHeight: 600, scrollTop: 400 })
    log.scrollTo = ((o: ScrollToOptions) => { log.scrollTop = o.top ?? log.scrollTop }) as typeof log.scrollTo
    fireEvent.scroll(log)
    await act(async () => { await Promise.resolve() })
    const targets = [...log.querySelectorAll('[data-index]')].slice(0, 8)
    expect(targets).toHaveLength(8)
    const observer = ro.watching(targets[0])[0]
    commits = 0
    await act(async () => {
      observer.cb(targets.map(target => ({ target, borderBoxSize: [{ inlineSize: 800, blockSize: 60 }] }) as unknown as ResizeObserverEntry), observer.self)
      await Promise.resolve()
    })
    expect(commits).toBeGreaterThan(0)
    expect(commits).toBeLessThanOrEqual(2)
  } finally {
    view.unmount()
    ro.restore()
  }
})

// ---- the history gap and jumps (spec «Поиск», «Лента (Feed)») ----
//
// A channel whose held history (s1..s10, a segment jumped to) does not join
// the window (w1..w10): the gap row sits between them. The layout is driven
// by hand like threadRig's: 40 px rows at their range offset minus
// scrollTop; scrollTo moves the feed within the content's height.

const seg = (from: number, to: number, prefix = 's', minAgo = 300) =>
  Array.from({ length: to - from + 1 }, (_, i) => P(`${prefix}${from + i}`, 'bob', minAgo - from - i))
const win = (from: number, to: number) => seg(from, to, 'w', 100)
const openGap = (o: Partial<HistGap> = {}): HistGap => ({ open: true, gen: 1, before_id: 'w1', stale: false, ...o })

type FeedProps = Parameters<typeof Feed>[0]

async function channelRig(height: number, o: Partial<ChannelDTO> = {}, extra: Partial<FeedProps> = {}) {
  const frames = manualFrames()
  const onLoadNewer = vi.fn((): Promise<unknown> => Promise.resolve())
  const onRetryStale = vi.fn()
  let p: FeedProps = {
    ...props({ posts: [...seg(1, 10), ...win(1, 10)], gap: openGap(), hist_rev: 1, new_since: 0, ...o }),
    onLoadNewer, onRetryStale, ...extra,
  }
  const view = render(<Feed {...p} />)
  const log = screen.getByRole('log')
  const st = { top: 0 }
  const content = () => Math.max(height, parseFloat((log.lastElementChild as HTMLElement).style.height) || 0)
  const clamp = (v: number) => Math.max(0, Math.min(v, content() - height))
  Object.defineProperties(log, {
    scrollHeight: { configurable: true, get: content },
    clientHeight: { configurable: true, get: () => height },
    scrollTop: { configurable: true, get: () => st.top, set: (v: number) => void (st.top = clamp(v)) },
  })
  const scrollToSpy = vi.fn((o: ScrollToOptions) => void (st.top = clamp(o.top ?? st.top)))
  log.scrollTo = scrollToSpy as unknown as typeof log.scrollTo
  const rowTop = (el: HTMLElement) => {
    const range = el.parentElement!
    const start = Number(/translateY\((-?[\d.]+)px\)/.exec(range.style.transform)?.[1] ?? 0)
    return start + [...range.children].indexOf(el) * 40 - st.top
  }
  const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const row = this.closest<HTMLElement>('[data-kind]')
    const inLog = row !== null && log.contains(row)
    const y = this === log ? 0 : inLog ? rowTop(row) : 0
    const h = this === log ? height : inLog ? 40 : 0
    return { top: y, bottom: y + h, left: 0, right: 0, width: 0, height: h, x: 0, y, toJSON: () => ({}) } as DOMRect
  })
  const onScreen = (key: string) => {
    const el = log.querySelector<HTMLElement>(`[data-key="${key}"]`)
    return el ? rowTop(el) : null
  }
  const row = (key: string) => log.querySelector<HTMLElement>(`[data-key="${key}"]`)
  const scrollTo = async (top: number) => {
    st.top = clamp(top)
    fireEvent.scroll(log)
    await frames.flush()
  }
  const update = async (d: Partial<ChannelDTO>, x: Partial<FeedProps> = {}) => {
    p = { ...p, ...x, data: { ...p.data, ...d } }
    view.rerender(<Feed {...p} />)
    await frames.flush()
  }
  await frames.flush() // the mount's frames
  fireEvent.scroll(log) // the scroll event a browser sends for the mount's own scroll
  await frames.flush()
  return {
    log, st, onLoadNewer, onRetryStale, onScreen, row, scrollTo, update, scrollToSpy, flush: frames.flush,
    data: () => p.data,
    restore: () => (rect.mockRestore(), frames.restore()),
  }
}

// A promise the test settles by hand.
function pending<T = void>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((a, b) => ((resolve = a), (reject = b)))
  return { promise, resolve, reject }
}

test('renders a gap row between segment and window', () => {
  render(<Feed {...props({ posts: [...seg(1, 2), ...win(1, 2)], gap: openGap(), hist_rev: 1 })} onLoadNewer={vi.fn().mockResolvedValue(undefined)} />)
  const log = screen.getByRole('log')
  const order = [...log.querySelectorAll('[data-kind]')].map((r) => r.getAttribute('data-key'))
  expect(order.indexOf('gap:1')).toBe(order.indexOf('s2') + 1)
  expect(order.indexOf('w1')).toBeGreaterThan(order.indexOf('gap:1'))
  const gapRow = log.querySelector<HTMLElement>('[data-key="gap:1"]')!
  expect(within(gapRow).getByRole('button', { name: 'Load newer messages' })).toBeInTheDocument()
})

test('gap row key is stable while the gap generation holds (the same row element across pages)', async () => {
  const r = await channelRig(600)
  try {
    const before = r.row('gap:1')
    expect(before).not.toBeNull()
    await r.update({ posts: [...seg(1, 12), ...win(1, 10)], hist_rev: 2 })
    expect(r.row('gap:1')).toBe(before)
  } finally {
    r.restore()
  }
})

test('the thread variant labels the gap row with replies', () => {
  render(<Feed {...props({ posts: [...seg(1, 2), ...win(1, 2)], gap: openGap(), hist_rev: 1 })} variant="thread" onLoadNewer={vi.fn().mockResolvedValue(undefined)} />)
  expect(screen.getByRole('button', { name: 'Load newer replies' })).toBeInTheDocument()
})

test('auto-loads the gap within a budget', async () => {
  // Short channel: the gap row stays on screen whatever loads.
  const r = await channelRig(600, { posts: [...seg(1, 2), ...win(1, 2)] })
  try {
    expect(r.onLoadNewer).toHaveBeenCalledTimes(5) // on its own: 5 pages in a row, then it waits
    await r.update({ posts: [...seg(1, 3), ...win(1, 2)], hist_rev: 2 })
    expect(r.onLoadNewer).toHaveBeenCalledTimes(5)

    fireEvent.click(within(r.row('gap:1')!).getByRole('button', { name: 'Load newer messages' }))
    await r.flush()
    expect(r.onLoadNewer.mock.calls.length).toBeGreaterThan(5) // the user asked: a fresh budget

    const n = r.onLoadNewer.mock.calls.length
    fireEvent.wheel(r.log, { deltaY: 100 })
    await r.flush()
    expect(r.onLoadNewer.mock.calls.length).toBeGreaterThan(n) // a wheel gesture too
  } finally {
    r.restore()
  }
})

test('pages are loaded one at a time (the gap and history share one flag)', async () => {
  const page = pending()
  const onLoadNewer = vi.fn(() => page.promise)
  const onLoadOlder = vi.fn().mockResolvedValue(true)
  const r = await channelRig(600, { has_more: true }, { onLoadNewer, onLoadOlder })
  try {
    expect(onLoadNewer).toHaveBeenCalledTimes(1)
    fireEvent.wheel(r.log, { deltaY: 100 })
    await r.scrollTo(r.st.top - 10)
    const button = within(r.row('gap:1')!).getByRole('button', { name: 'Loading…' })
    expect(button).toBeDisabled()
    fireEvent.click(button)
    await r.scrollTo(0) // at the top: history would load — not while a page is in flight
    expect(onLoadNewer).toHaveBeenCalledTimes(1)
    expect(onLoadOlder).not.toHaveBeenCalled()
  } finally {
    r.restore()
  }
})

test('anchor applies on hist_rev, not on an earlier channel_changed', async () => {
  const page = pending()
  const onLoadNewer = vi.fn(() => page.promise)
  const r = await channelRig(600, {}, { onLoadNewer })
  try {
    expect(onLoadNewer).toHaveBeenCalledTimes(1) // the gap row is on screen at the bottom of the feed
    // A WS post arrives first (channel_changed before the page): no hist_rev change.
    await r.update({ posts: [...seg(1, 10), ...win(1, 11)] })
    await r.scrollTo(r.st.top + 20) // the user keeps scrolling while the page is in flight
    const seen = r.onScreen('w1')!
    page.resolve()
    // The page: three posts land in the gap, above w1.
    await r.update({ posts: [...seg(1, 10), ...seg(1, 3, 'n', 200), ...win(1, 11)], hist_rev: 2 })
    expect(r.onScreen('w1')).toBe(seen)
  } finally {
    r.restore()
  }
})

test('a dropped page (no hist_rev change) with a WS row does not consume the anchor', async () => {
  const later = pending()
  const onLoadNewer = vi.fn((): Promise<unknown> => later.promise).mockImplementationOnce(() => Promise.resolve()) // Go dropped the first page
  const r = await channelRig(600, {}, { onLoadNewer })
  try {
    expect(onLoadNewer).toHaveBeenCalledTimes(2) // …the next one is in flight
    await r.scrollTo(r.st.top - 100) // off the bottom: a new post is not followed there
    const seen = r.onScreen('w1')!
    r.scrollToSpy.mockClear()
    await r.update({ posts: [...seg(1, 10), ...win(1, 11)] }) // a WS post, same hist_rev
    expect(r.scrollToSpy).not.toHaveBeenCalled() // no restore on it
    expect(r.onScreen('w1')).toBe(seen)
    later.resolve()
    await r.update({ posts: [...seg(1, 10), ...seg(1, 3, 'n', 200), ...win(1, 11)], hist_rev: 2 })
    expect(r.onScreen('w1')).toBe(seen)
  } finally {
    r.restore()
  }
})

test('keeps the visible post when rows are inserted above it (from below)', async () => {
  const page = pending()
  const onLoadNewer = vi.fn(() => page.promise)
  const r = await channelRig(600, {}, { onLoadNewer })
  try {
    // The user came up from the window: the gap row is in the upper part of
    // the screen, segment posts above it, window posts below.
    await r.scrollTo(240)
    expect(r.onScreen('gap:1')).toBe(200)
    const seen = r.onScreen('w1')!
    expect(r.onScreen('s6')).toBeGreaterThanOrEqual(0) // a segment post is on screen too, at the top
    page.resolve()
    await r.update({ posts: [...seg(1, 10), ...seg(1, 3, 'n', 200), ...win(1, 10)], hist_rev: 2 })
    expect(r.onScreen('w1')).toBe(seen) // the side on screen below the gap stays put
  } finally {
    r.restore()
  }
})

test('jump centers and highlights the target, beats the initial scroll', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  // A "new messages" line in the window would be the initial scroll's target.
  const posts = [...seg(1, 10), ...win(1, 9), P('w10', 'carol', 1)]
  const r = await channelRig(600, { posts, gap: openGap({ open: false, before_id: '' }), new_since: now - 5 * 60_000 }, { focus: { postId: 's8', nonce: 1 } })
  try {
    expect(r.onScreen('s8')).toBe(280) // (600 - 40) / 2
    expect(r.row('s8')).toHaveClass('post--focus')
    act(() => void vi.advanceTimersByTime(3000))
    expect(r.row('s8')).not.toHaveClass('post--focus') // the highlight fades out
  } finally {
    vi.useRealTimers()
    r.restore()
  }
})

test('a jump after the feed opened centers the target and a new post does not pull it to the bottom', async () => {
  const r = await channelRig(600, { gap: openGap({ open: false, before_id: '' }) })
  try {
    expect(r.st.top).toBe(r.log.scrollHeight - 600) // opened at the bottom
    await r.update({}, { focus: { postId: 's8', nonce: 1 } })
    expect(r.onScreen('s8')).toBe(280)
    await r.update({ posts: [...seg(1, 10), ...win(1, 11)] })
    expect(r.onScreen('s8')).toBe(280)
  } finally {
    r.restore()
  }
})

test('a jump whose target is not loaded yet centers it once it arrives', async () => {
  const r = await channelRig(600, { posts: win(1, 10), gap: openGap({ open: false, before_id: '' }) })
  try {
    await r.update({}, { focus: { postId: 's8', nonce: 1 } }) // Go has not re-read the channel yet
    await r.update({ posts: [...seg(1, 10), ...win(1, 10)], gap: openGap(), hist_rev: 2 })
    expect(r.onScreen('s8')).toBe(280)
    expect(r.row('s8')).toHaveClass('post--focus')
  } finally {
    r.restore()
  }
})

test('same target again re-centers (nonce)', async () => {
  const r = await channelRig(600, { gap: openGap({ open: false, before_id: '' }) }, { focus: { postId: 's8', nonce: 1 } })
  try {
    expect(r.onScreen('s8')).toBe(280)
    await r.scrollTo(0)
    await r.update({ hist_rev: 2 }) // a data revision: never re-centers
    expect(r.onScreen('s8')).toBe(320)
    await r.update({}, { focus: { postId: 's8', nonce: 2 } })
    expect(r.onScreen('s8')).toBe(280)
  } finally {
    r.restore()
  }
})

test('stale indicator with retry', async () => {
  const onRetryStale = vi.fn()
  const view = render(<Feed {...props({ posts: [...seg(1, 2), ...win(1, 2)], gap: openGap({ stale: true }), hist_rev: 1 })} onLoadNewer={vi.fn().mockResolvedValue(undefined)} onRetryStale={onRetryStale} />)
  const gapRow = screen.getByRole('log').querySelector<HTMLElement>('[data-key="gap:1"]')!
  expect(within(gapRow).getByText('Checking messages…')).toBeInTheDocument()
  fireEvent.click(within(gapRow).getByRole('button', { name: 'Retry checking messages' }))
  expect(onRetryStale).toHaveBeenCalledTimes(1)

  // A closed gap whose history is still stale: a thin indicator row of its own.
  view.rerender(<Feed {...props({ posts: [...seg(1, 2), ...win(1, 2)], gap: openGap({ open: false, before_id: '', stale: true }), hist_rev: 2 })} onRetryStale={onRetryStale} />)
  expect(screen.queryByRole('button', { name: 'Load newer messages' })).not.toBeInTheDocument()
  const thin = screen.getByRole('log').querySelector<HTMLElement>('[data-key="gap-stale:1"]')!
  expect(within(thin).getByText('Checking messages…')).toBeInTheDocument()
  fireEvent.click(within(thin).getByRole('button', { name: 'Retry checking messages' }))
  expect(onRetryStale).toHaveBeenCalledTimes(2)
})

test('a failed gap page shows the error with Retry and stops loading on its own', async () => {
  const onLoadNewer = vi.fn((): Promise<unknown> => Promise.reject(new ApiError('no_progress', '')))
  const r = await channelRig(600, {}, { onLoadNewer })
  try {
    expect(onLoadNewer).toHaveBeenCalledTimes(1)
    const gapRow = r.row('gap:1')!
    expect(within(gapRow).getByText('Could not load newer messages')).toBeInTheDocument()
    await r.scrollTo(r.st.top - 10)
    expect(onLoadNewer).toHaveBeenCalledTimes(1)
    onLoadNewer.mockImplementation(() => new Promise(() => {}))
    fireEvent.click(within(gapRow).getByRole('button', { name: 'Retry loading newer messages' }))
    await r.flush()
    expect(onLoadNewer).toHaveBeenCalledTimes(2)
    expect(within(r.row('gap:1')!).queryByText('Could not load newer messages')).not.toBeInTheDocument()
  } finally {
    r.restore()
  }
})

// --- Codex review (Task 6 fix round 1) repros ---

test('Codex: an existing pending send cannot undo a jump on a later refresh', async ()=>{
 const pendingPost={...P('pending','me',0),pending:true};
 const posts=[...seg(1,10),...win(1,10),pendingPost];
 const r=await channelRig(600,{posts,gap:openGap({open:false,before_id:''})});
 try {
  await r.update({}, {focus:{postId:'s8',nonce:1}});
  expect(r.onScreen('s8')).toBe(280);
  await r.update({posts:posts.map(p=>({...p}))});
  expect(r.onScreen('s8')).toBe(280);
 } finally {r.restore()}
});

test('fix: a new own send after a jump still goes to the bottom', async () => {
  const posts = [...seg(1, 10), ...win(1, 10)]
  const r = await channelRig(600, { posts, gap: openGap({ open: false, before_id: '' }) })
  try {
    await r.update({}, { focus: { postId: 's8', nonce: 1 } })
    expect(r.onScreen('s8')).toBe(280)
    await r.update({ posts: [...posts, { ...P('mine', 'me', 0), pending: true }] })
    expect(r.st.top).toBe(r.log.scrollHeight - 600)
  } finally {
    r.restore()
  }
})

test('fix: a jump clears a history anchor pending since before its target arrived', async () => {
  const page = pending()
  const onLoadNewer = vi.fn(() => page.promise)
  const r = await channelRig(600, { posts: [...seg(1, 3), ...win(1, 10)] }, { focus: { postId: 's8', nonce: 1 }, onLoadNewer })
  try {
    expect(onLoadNewer).toHaveBeenCalledTimes(1) // a gap page (and its anchor) began after the jump, before its target arrived
    await r.scrollTo(r.st.top - 30) // the user moves: the pending anchor follows
    page.resolve()
    const closed = openGap({ open: false, before_id: '' }) // the page closed the gap: nothing loads after it
    await r.update({ posts: [...seg(1, 10), ...win(1, 10)], hist_rev: 2, gap: closed })
    expect(r.onScreen('s8')).toBe(280)
    await r.update({ posts: [...seg(1, 10), ...win(1, 11)], hist_rev: 2, gap: closed }) // a WS post
    expect(r.onScreen('s8')).toBe(280)
  } finally {
    r.restore()
  }
})

test('fix: a click on the gap button while history loads is kept and runs after it', async () => {
  const older = pending<boolean>()
  const onLoadOlder = vi.fn(() => older.promise)
  const onLoadNewer = vi.fn((): Promise<unknown> => Promise.resolve()) // pages Go dropped: the budget runs out
  const r = await channelRig(600, { has_more: true }, { onLoadOlder, onLoadNewer })
  try {
    expect(onLoadNewer).toHaveBeenCalledTimes(5)
    await r.scrollTo(0) // history starts loading; the gap row is still on screen
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
    fireEvent.click(within(r.row('gap:1')!).getByRole('button', { name: 'Load newer messages' }))
    await r.flush()
    expect(onLoadNewer).toHaveBeenCalledTimes(5) // one history operation at a time…
    older.resolve(true)
    await r.flush()
    expect(onLoadNewer.mock.calls.length).toBeGreaterThan(5) // …but the click was kept, not dropped
  } finally {
    r.restore()
  }
})

test('fix: open + stale + error — the two Retry buttons have distinct names', async () => {
  const r = await channelRig(600, { gap: openGap({ stale: true }) }, { onLoadNewer: vi.fn((): Promise<unknown> => Promise.reject(new ApiError('no_progress', ''))) })
  try {
    const row = r.row('gap:1')!
    expect(within(row).getByRole('button', { name: 'Retry loading newer messages' })).toBeInTheDocument()
    expect(within(row).getByRole('button', { name: 'Retry checking messages' })).toBeInTheDocument()
  } finally {
    r.restore()
  }
})
