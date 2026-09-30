import { observeElementRect, useVirtualizer } from '@tanstack/react-virtual'
import { useCallback, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import type { Attachment, ChannelDTO, FileView } from '../api/types'
import { formatDay } from '../format'
import { t } from '../i18n'
import { buildRows, type FeedVariant, type Row } from './feedRows'
import { fileKind } from './files'
import { IconArrowDown } from './icons'
import { PostItem, type PostActions } from './PostItem'
import { ScrollShift } from './scrollShift'
import { useToastHost } from './Toast'

// FeedData: what Feed needs from a channel or a thread (ChannelDTO and
// ThreadDTO both have this shape — see AGENTS.md/Task 6 brief). new_since/
// gap_after are always empty on a ThreadDTO, so the "new messages" line and
// the gap row never appear there.
export type FeedData = Pick<ChannelDTO, 'id' | 'posts' | 'new_since' | 'me_id' | 'gap_after' | 'has_more' | 'loaded' | 'crt'>

interface Props {
  data: FeedData
  variant: FeedVariant
  serverId: number
  me: { id: string; username: string }
  locale: string
  actions: PostActions
  editingId: string | null
  onLoadOlder(): Promise<boolean>
  // toastHost: this feed's priority as a toast host (Toast.tsx's
  // TOAST_HOST) — the toast then floats over the feed's visible box, like the
  // jump-to-latest button. Without it the feed hosts no toast.
  toastHost?: number
}

const NEAR_TOP = 300
const NEAR_BOTTOM = 48
const NEW_BADGE_CAP = 99
const MAX_AUTO_LOADS = 2 // history loads the feed starts on its own (fillViewportIfShort) before it waits for the user
const MAX_CORRECTIONS = 10 // a few frames may pass before the anchor row is even (re-)mounted after scrollToOffset

// attachmentEstimate: a message_attachment's rendered height before mount
// (density-brief 2026-09-29's bordered card — PostItem.tsx's AttachmentView).
// Deliberately as coarse as the rest of estimate() (no text-wrapping math —
// measureElement corrects the real height after mount), but accounts for
// the two things that dominate the gap the review flagged: the card's own
// chrome (border + margin + padding, ~36px, independent of content) and a
// rough per-line add-on so a title/text/fields/footer-heavy card (the
// Jenkins/CI shape the brief itself uses) isn't estimated as if it were an
// empty box.
export const CARD_CHROME = 36
function attachmentEstimate(a: Attachment): number {
  let h = CARD_CHROME
  if (a.pretext) h += 20
  if (a.author_name) h += 16
  if (a.title) h += 20
  if (a.text) h += 20 * a.text.split('\n').length
  if (a.fields && a.fields.length > 0) {
    // Each field renders a title line + a value line (~40px together,
    // review's own measurement) as one grid row; a full-width field
    // (short: false) takes a row to itself, short fields pack two per row.
    const fullWidth = a.fields.filter((f) => !f.short).length
    const shortCount = a.fields.length - fullWidth
    h += fullWidth * 40 + Math.ceil(shortCount / 2) * 40
  }
  if (a.footer) h += 18
  return h
}

// fileCardsEstimate: file-cards brief (2026-09-30) review, fix round 1 —
// estimate() never accounted for post.files at all (images/video/audio/
// text/markdown still don't; their own fixed-box sizes vary too much for a
// cheap guess to be worth it, and they predate this brief). File **cards**
// specifically (fileKind 'other'/'pdf' — Attachments.tsx's `others` group)
// went from a cramped single ~40px row to a fixed 320x64 box that wraps,
// so a post with several of them is now under-estimated by a much larger
// margin than before this brief. FileCard.tsx/index.css own the real
// rendering constants; these are only a coarse pre-mount guess
// (measureElement corrects the real height after mount, same as every
// other term in estimate()) — no ResizeObserver on the feed's actual width
// (the brief's own "no new observers" ruling), just a nominal guess at a
// typical feed content width to work out roughly how many cards wrap onto
// one row.
export const FILE_CARD_W = 320
export const FILE_CARD_H = 64
const FILE_CARD_GAP = 8
const FEED_NOMINAL_WIDTH = 800
function fileCardsEstimate(files: FileView[] | undefined): number {
  if (!files || files.length === 0) return 0
  const cards = files.filter((f) => fileKind(f) === 'other' || fileKind(f) === 'pdf').length
  if (cards === 0) return 0
  const perRow = Math.max(1, Math.floor((FEED_NOMINAL_WIDTH + FILE_CARD_GAP) / (FILE_CARD_W + FILE_CARD_GAP)))
  const rows = Math.ceil(cards / perRow)
  return rows * FILE_CARD_H + (rows - 1) * FILE_CARD_GAP
}

export const estimate = (r: Row) =>
  r.kind === 'post'
    ? (r.head ? 64 : 28) +
      (r.post.attachments?.reduce((sum, a) => sum + attachmentEstimate(a), 0) ?? 0) +
      fileCardsEstimate(r.post.files)
    : 36

// anchorNudge computes how far scrollTop must move to bring the anchor
// row's measured on-screen position back to its target — the position it
// had before the history load. Returns null once already within
// `tolerance` px (converged; nothing to do).
export function anchorNudge(measured: number, target: number, tolerance = 1): number | null {
  const delta = measured - target
  return Math.abs(delta) <= tolerance ? null : delta
}

export interface RowBox {
  key: string
  top: number // getBoundingClientRect().top
  bottom: number // getBoundingClientRect().bottom
}

// pickAnchor chooses the history-load scroll anchor: of the rendered post
// rows, the topmost one still (at least partly) visible below the viewport
// top `viewTop` — i.e. the post the user actually sees at the top — and its
// offset from the viewport top (negative when partly scrolled out). Rows
// entirely above the viewport (the virtualizer's overscan) are skipped.
export function pickAnchor(boxes: RowBox[], viewTop: number): { key: string; offset: number } | null {
  let best: RowBox | null = null
  for (const b of boxes) {
    if (b.bottom > viewTop && (!best || b.top < best.top)) best = b
  }
  return best && { key: best.key, offset: best.top - viewTop }
}

// useFrames schedules animation-frame callbacks by purpose: scheduling a
// purpose again replaces its pending frame, and unmounting cancels them all.
// A hidden window (closed to the tray) never paints, so the
// engine never runs frame callbacks; each one left pending would keep its
// closure — the feed's rows, DOM and channel of that render — alive until
// the next paint: one per incoming post, and a whole feed per channel switch.
export function useFrames(): (purpose: string, fn: () => void) => void {
  const ids = useRef<Map<string, number>>(new Map())
  useEffect(() => {
    const pending = ids.current
    return () => {
      for (const id of pending.values()) cancelAnimationFrame(id)
      pending.clear()
    }
  }, [])
  return useCallback((purpose, fn) => {
    const pending = ids.current
    const prev = pending.get(purpose)
    if (prev !== undefined) cancelAnimationFrame(prev)
    pending.set(
      purpose,
      requestAnimationFrame(() => {
        pending.delete(purpose)
        fn()
      }),
    )
  }, [])
}

// Feed must be keyed by channel id: another channel is a fresh mount, so
// the scroll bookkeeping below never leaks between channels.
export function Feed({ data, variant, serverId, me, locale, actions, editingId, onLoadOlder, toastHost }: Props) {
  const rows = useMemo(() => buildRows(data, variant), [data, variant])
  const scroller = useRef<HTMLDivElement>(null)
  const ready = useRef(false)
  const atBottom = useRef(true)
  const bottomTop = useRef<number | null>(null) // scrollTop when the feed was last known to be at its bottom
  const anchor = useRef<string | null>(null)
  const anchorOffset = useRef(0) // anchor row's distance below the viewport top, px
  const userScrolling = useRef(false) // a real wheel/touch gesture since the last restore
  const anchorTried = useRef(false) // the last rows change restored a history anchor (dev diagnostics)
  const loading = useRef(false)
  const autoLoads = useRef(0) // fillViewportIfShort's loads since the last user gesture or leaving the top
  const lastLoad = useRef<{ rows: number; top: number } | null>(null) // rows and scrollTop when the last load started
  const [loadingOlder, setLoadingOlder] = useState(false)
  const frame = useFrames()
  // Jump-to-latest button: visible once the feed is far enough from the
  // bottom, with a badge counting others' posts that arrived since. `far`
  // mirrors `jumpVisible` in a ref so onScroll (fired on every scroll event)
  // only calls setState when the visible/hidden state actually flips, not on
  // every pixel of scroll — same rule as `atBottom`.
  const [jumpVisible, setJumpVisible] = useState(false)
  const far = useRef(false)
  const [newCount, setNewCount] = useState(0)
  const newIds = useRef<Set<string>>(new Set()) // counted arrivals since the user left the bottom
  const newestSeenAt = useRef<number | null>(null) // create_at of the newest post accounted for; null before the first pass
  const knownAtBaseline = useRef<Set<string>>(new Set()) // ids present as of newestSeenAt's own pass — tie-breaks posts sharing its create_at
  // WebKit queues a scroll event for every element scrolled by script (the
  // virtualizer scrolls each new feed to its end) and keeps the element until
  // the next paint dispatches it; a hidden window never paints, so every
  // switched-away feed would stay alive with all its rows. Empty the scroller
  // when the feed goes away: only the bare element waits for the paint.
  // React removes just this top node; the rows below are not touched again.
  // Checked after the commit: StrictMode's rehearsal unmount keeps the node.
  useLayoutEffect(() => {
    const el = scroller.current
    return () => {
      queueMicrotask(() => {
        if (el && !el.isConnected) el.replaceChildren()
      })
    }
  }, [])
  // Mid-scroll compensation for rows measured above the fold goes into a
  // negative margin on the rows' container, not into scrollTop (which cancels
  // WebKit's wheel animation) — see ScrollShift.
  const sizer = useRef<HTMLDivElement>(null)
  const afterGesture = useRef(() => {}) // the bottom-stick's re-pin deferred past a wheel/touch gesture (below)
  const [, bump] = useReducer((x: number) => x + 1, 0)
  const [shift] = useState(() => {
    let queued = false
    // The shifted rows must be committed before the next paint: the
    // virtualizer's own notify for them is async. A microtask runs after the
    // ResizeObserver callback that took the shift and before the paint.
    const rerender = () => {
      if (queued) return
      queued = true
      queueMicrotask(() => {
        queued = false
        flushSync(bump)
      })
    }
    return new ScrollShift(() => scroller.current, () => sizer.current, rerender, () => afterGesture.current())
  })
  useEffect(() => () => shift.dispose(), [shift])
  useLayoutEffect(() => shift.apply()) // every commit: rows and shift reach the screen together

  // Bottom-stick on a size change. The rows effect below follows the bottom
  // only when rows change; the feed's own height also changes without a new
  // row — a banner or status bar above it, the composer growing with a
  // multi-line draft or attachment chips, the window or a splitter resized,
  // the thread panel reflowing the rows — and so does the content's (the
  // last row re-measured taller). A shorter scroller keeps its scrollTop and
  // fires no scroll event, so the last post slid below the fold and stayed
  // there (user report 2026-09-29: a download's banner pushed the last post
  // under the composer). `stick` re-pins a feed that was at its bottom
  // (atBottom, as of the last scroll event): a ResizeObserver on the
  // scroller calls it for the viewport (its callback runs after layout,
  // before paint: the same frame), and a layout effect on the virtualizer's
  // total size for the content (right in the commit that grew the rows'
  // container). Not a ResizeObserver on that container: the virtualizer
  // re-renders it synchronously from its own row observer, a shallower
  // element resized inside the broadcast — "ResizeObserver loop completed
  // with undelivered notifications" (review M6, seen in e2e). Away from the
  // bottom nothing is written: the viewport's top edge keeps its scrollTop,
  // so the rows on screen stay put, and rows re-measured above the fold are
  // the virtualizer's (and ScrollShift's). Mid-gesture (wheel/touch) nothing
  // is written either — a script write to scrollTop cancels WebKitGTK's
  // wheel animation (see ScrollShift); the pin waits for the gesture to go
  // idle and applies only if the feed is still at its bottom then. The
  // jump-to-latest button needs nothing here: the pin's own scroll event
  // runs onScroll, which hides it within NEAR_BOTTOM.
  const stick = useRef(() => {})
  useLayoutEffect(() => {
    const el = scroller.current
    if (!el) return
    let deferred = false
    const pin = () => {
      if (!ready.current || !atBottom.current) return
      if (shift.gesturing) {
        deferred = true
        return
      }
      shift.flush()
      const end = el.scrollHeight - el.clientHeight
      if (end - el.scrollTop > 0.5) el.scrollTop = end
      bottomTop.current = el.scrollTop
    }
    stick.current = pin
    afterGesture.current = () => {
      if (!deferred) return
      deferred = false
      pin()
    }
    const ro = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(pin)
    ro?.observe(el)
    return () => {
      ro?.disconnect()
      stick.current = () => {}
      afterGesture.current = () => {}
    }
  }, [shift])
  const v = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: (i) => estimate(rows[i]),
    getItemKey: (i) => rows[i].key,
    overscan: 8,
    // The scroller's new size is taken in the next frame, not inside the
    // ResizeObserver delivery: mid-scroll the virtualizer's notify is a
    // flushSync, and a shorter scroller (the composer growing) unmounted the
    // row that left the range right there. That row is still observed by the
    // virtualizer's row observer and, detached, sits shallower than the
    // scroller, so Chromium skipped it: "ResizeObserver loop completed with
    // undelivered notifications" (stick-bottom.spec, composer fix round 1).
    // The first measurement (not from an observer) passes straight through;
    // overscan covers the frame, and the bottom-stick has its own observer.
    // Not virtual-core's useAnimationFrameWithResizeObserver: that one
    // schedules a frame per notification, neither coalesced nor cancelled on
    // unmount — a hidden window never runs them and they pin the unmounted
    // feed (AGENTS.md, hidden-window rule); useFrames does both.
    observeElementRect: (instance, cb) => {
      let sync = true
      let latest: Parameters<typeof cb>[0] | null = null
      const off = observeElementRect(instance, (rect) => {
        if (sync) return cb(rect)
        latest = rect
        frame('rect', () => latest && cb(latest))
      })
      sync = false
      return off
    },
    observeElementOffset: shift.observeOffset,
    scrollToFn: shift.scrollTo,
  })
  v.shouldAdjustScrollPositionOnItemSizeChange = shift.shouldAdjust
  // The content half of the bottom-stick (above): the rows' total height
  // changed in this commit.
  const total = v.getTotalSize()
  const lastTotal = useRef(total)
  useLayoutEffect(() => {
    if (total === lastTotal.current) return
    lastTotal.current = total
    stick.current()
  }, [total])
  // Our own scrolls land a pending shift *before* the virtualizer computes
  // the target: it clamps to the scroll range, which the shift has shrunk,
  // and would undershoot by it for a frame (index scrolls) or for good
  // (offset restores). ScrollShift.scrollTo landing it later is too late.
  const scrollToIndex: typeof v.scrollToIndex = (...a) => {
    shift.flush()
    v.scrollToIndex(...a)
  }

  // Resets the jump-to-latest badge: the user reached the bottom, by any
  // means (scrolling there, or clicking the button itself).
  const resetNewPosts = () => {
    if (newIds.current.size) {
      newIds.current.clear()
      setNewCount(0)
    }
  }

  // Tracks posts that arrive at the *live end* while the user is away from
  // the bottom, for the jump-to-latest badge — own posts don't count. Driven
  // by rows changing (new data), not by scroll events. Compares create_at
  // against the newest post already accounted for, not "was this id seen
  // before": scrolling up loads older history pages, which also make
  // previously-unseen posts appear in rows — those have an older create_at
  // than everything already known and must not inflate the badge (e2e-caught:
  // scrolling to the top of a 150-post channel read as dozens of "new"
  // posts). A post exactly at the baseline counts only if it wasn't already
  // known as of that baseline (knownAtBaseline) — two posts can share a
  // create_at (same millisecond, or several arriving in one batch), and a
  // plain ">" would silently drop the second of such a pair on a later pass.
  // newestSeenAt/knownAtBaseline seed silently on the first pass so the
  // channel's initial posts are never counted.
  useEffect(() => {
    let newest: number | null = null
    const ids = new Set<string>()
    for (const r of rows) {
      if (r.kind !== 'post') continue
      ids.add(r.post.id)
      if (newest === null || r.post.create_at > newest) newest = r.post.create_at
    }
    const baseline = newestSeenAt.current
    if (baseline !== null && !atBottom.current) {
      let grew = false
      for (const r of rows) {
        if (r.kind !== 'post' || r.post.user_id === me.id || newIds.current.has(r.post.id)) continue
        const isNew = r.post.create_at > baseline || (r.post.create_at === baseline && !knownAtBaseline.current.has(r.post.id))
        if (isNew) {
          newIds.current.add(r.post.id)
          grew = true
        }
      }
      if (grew) setNewCount(newIds.current.size)
    }
    if (newest !== null && (baseline === null || newest > baseline)) {
      newestSeenAt.current = newest
      knownAtBaseline.current = ids
    }
  }, [rows, me.id])

  // Anchor on the post the user actually sees at the top, read from the
  // DOM. Not v.range.startIndex: the virtualizer's own scroll offset
  // tracking lags the real scrollTop during scrolling, so its "first
  // visible" index can be several rows off; nor the first entry of
  // getVirtualItems(), which is overscan rendered above the viewport.
  // Re-run on every scroll while the page is in flight (onScroll), so the
  // anchor is where the user is when the rows land, not where they were
  // when the fetch started.
  // Only rows the page lands above can anchor: a thread's root heads the
  // panel before and after every page (the older replies land under it and
  // its "N replies" line), so keeping the root in place kept the feed at its
  // top — the page just loaded was skipped and the next one requested
  // (threads.spec, "a long thread … without gaps": whenever the load started
  // with the root on screen). But only when another post is on screen: with
  // the root alone in view (a tall root, a short panel) the next candidate is
  // a reply below the fold, and keeping that one in place pushed the root the
  // user was reading off screen — then the root anchors, the page lands below
  // (re-review I-1).
  const captureAnchor = () => {
    const el = scroller.current
    const first = rows.find((r) => r.kind === 'post')
    const head = variant === 'thread' && first?.kind === 'post' && !first.post.root_id ? first.key : null
    let found: { key: string; offset: number } | null = null
    if (el) {
      const viewTop = el.getBoundingClientRect().top
      const viewBottom = viewTop + el.clientHeight
      const boxes = [...el.querySelectorAll<HTMLElement>('[data-kind="post"]')].map((r) => {
        const b = r.getBoundingClientRect()
        return { key: r.dataset.key ?? '', top: b.top, bottom: b.bottom }
      })
      const others = boxes.filter((b) => b.key !== head && b.top < viewBottom && b.bottom > viewTop)
      found = pickAnchor(head !== null && others.length ? boxes.filter((b) => b.key !== head) : boxes, viewTop)
    }
    anchor.current = found?.key ?? null
    anchorOffset.current = found?.offset ?? 0
  }

  const loadOlder = async () => {
    if (loading.current || !data.has_more) return
    loading.current = true
    setLoadingOlder(true)
    lastLoad.current = { rows: rows.length, top: scroller.current?.scrollTop ?? 0 }
    captureAnchor()
    try {
      if (!(await onLoadOlder())) anchor.current = null
    } finally {
      loading.current = false
      setLoadingOlder(false)
    }
  }

  // Fetches another page when the current one still doesn't fill the
  // viewport (e.g. a short first page), or when the feed is still within
  // NEAR_TOP of the top — onScroll's own rule. Runs after every rows change,
  // not just the first mount, so the feed can't get stuck with has_more
  // still true: under-filled after one page wasn't enough, or left at
  // scrollTop 0 by a page whose anchor restore could not move it (e2e caught
  // it; at the top neither scrollTo(0) nor the wheel fires another scroll
  // event, so onScroll would never ask again).
  // Bounded (review of d1ba0ac — unbounded, it re-requested every frame): it
  // continues only if the last load changed something (more rows, or a
  // scrollTop that moved — a page Go dropped changes neither), and at most
  // MAX_AUTO_LOADS times until a wheel/touch gesture or the feed leaving the
  // top. A restore that keeps failing costs two extra pages, not the
  // channel's whole history.
  const fillViewportIfShort = () => {
    frame('fill', () => {
      const el = scroller.current
      if (!el || !data.has_more || loading.current) return
      if (el.scrollHeight > el.clientHeight && el.scrollTop >= NEAR_TOP) return
      if (autoLoads.current >= MAX_AUTO_LOADS) return
      const last = lastLoad.current
      if (last && rows.length <= last.rows && el.scrollTop === last.top) return // the last load changed nothing
      if (import.meta.env.DEV && anchorTried.current) console.debug('feed: history restore left the feed at the top; loading on')
      autoLoads.current++
      void loadOlder()
    })
  }

  // The initial scrollToOffset restore (above the call site below) can be
  // off by a few px: the newly-prepended rows above the anchor were never
  // rendered before that call, so v.getOffsetForIndex used estimateSize for
  // them (and the virtualizer's own reconciliation, which can also nudge
  // scrollTop as those rows get measured, runs on its own schedule). Nudge
  // scrollTop to close that gap over a couple of frames, re-measuring the
  // anchor's *real* DOM position each time rather than trusting another
  // estimate. Aborts if a real user gesture (wheel/touch) happened since —
  // userScrolling is reset right before the restore call and only a
  // wheel/touchmove handler on the scroller sets it, so this can't mistake
  // the virtualizer's own scroll adjustments for the user and fight them.
  const correctAnchorPosition = (key: string, target: number, attempt: number) => {
    if (attempt > MAX_CORRECTIONS) return
    frame('anchor', () => {
      const el = scroller.current
      if (!el || userScrolling.current) return
      // A plain DOM query, not v.elementsCache: the anchor row is being
      // (re-)mounted for the first time at the restored offset, and the
      // virtualizer can skip registering a freshly-rendered row there for a
      // frame. Same box (the positioned wrapper) the capture measured.
      const rowEl = el.querySelector(`[data-key="${CSS.escape(key)}"]`)
      if (!rowEl) {
        correctAnchorPosition(key, target, attempt + 1) // not painted yet — retry
        return
      }
      const measured = rowEl.getBoundingClientRect().top - el.getBoundingClientRect().top
      const nudge = anchorNudge(measured, target)
      if (nudge === null) return // converged
      el.scrollTop += nudge
      correctAnchorPosition(key, target, attempt + 1)
    })
  }

  useLayoutEffect(() => {
    if (!rows.length) return
    if (!ready.current) {
      // First rows: the "new messages" line, else the latest post.
      ready.current = true
      const i = rows.findIndex((r) => r.kind === 'new')
      if (i >= 0) {
        atBottom.current = false
        scrollToIndex(i, { align: 'start' })
      } else {
        scrollToIndex(rows.length - 1, { align: 'end' })
      }
      fillViewportIfShort()
      return
    }
    anchorTried.current = !!anchor.current
    if (anchor.current) {
      // History landed above: keep the anchored row at the exact screen
      // position it had before the prepend (not necessarily the viewport
      // top — it may not have been there).
      const key = anchor.current
      const offset = anchorOffset.current
      anchor.current = null
      const el = scroller.current
      shift.flush() // before the target is computed (see scrollToIndex)
      const i = rows.findIndex((r) => r.key === key)
      const info = i >= 0 ? v.getOffsetForIndex(i, 'start') : undefined
      if (info && el) {
        userScrolling.current = false
        v.scrollToOffset(Math.max(0, info[0] - offset), { align: 'start' })
        correctAnchorPosition(key, offset, 1)
      }
      fillViewportIfShort()
      return
    }
    const last = rows[rows.length - 1]
    if (atBottom.current || (last.kind === 'post' && last.post.pending)) scrollToIndex(rows.length - 1, { align: 'end' })
    fillViewportIfShort()
  }, [rows, v]) // loadOlder is recreated every render; the effect only needs rows

  // Scrolls the post being edited into view when editing starts. Matched by
  // the post's real id, not the row key: a just-confirmed own post keeps its
  // earlier pending_post_id as its row key (see feedRows), but editingId —
  // set by edit()/editLastOwn() — is always the real post id.
  useLayoutEffect(() => {
    if (!editingId) return
    const i = rows.findIndex((r) => r.kind === 'post' && r.post.id === editingId)
    if (i >= 0) scrollToIndex(i, { align: 'auto' })
  }, [editingId]) // only when editing starts, not on every new post

  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    shift.onScroll()
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    const wasAtBottom = atBottom.current
    // A feed at its bottom leaves it only by moving up. A scroll event
    // measured after the feed got shorter again (the scroll of our own pin,
    // one frame late, while the composer keeps growing) shows a distance past
    // NEAR_BOTTOM with scrollTop where the pin put it: still the bottom — the
    // bottom-stick re-pins it in this frame's ResizeObserver pass.
    const stayed = wasAtBottom && bottomTop.current !== null && el.scrollTop >= bottomTop.current - 1
    atBottom.current = distance < NEAR_BOTTOM || stayed
    bottomTop.current = atBottom.current ? el.scrollTop : null
    if (!wasAtBottom && atBottom.current) resetNewPosts()
    if (loading.current && anchor.current) captureAnchor() // still waiting for the page: track where the user is now
    if (el.scrollTop < NEAR_TOP) void loadOlder()
    else autoLoads.current = 0 // away from the top: the next arrival there is a new episode
    // Hysteresis: show past one viewport from the bottom, hide within
    // NEAR_BOTTOM, leave it as-is in between — so it doesn't flicker.
    if (distance > el.clientHeight) {
      if (!far.current) {
        far.current = true
        setJumpVisible(true)
      }
    } else if (distance < NEAR_BOTTOM) {
      if (far.current) {
        far.current = false
        setJumpVisible(false)
      }
    }
  }

  // Uses the feed's own scroll path (flushes any pending ScrollShift first —
  // see the scrollToIndex wrapper above and AGENTS.md's scroll-shift rule).
  // rows.length - 1 is always the last *loaded* row: when the window is
  // stale with a gap (channel.gap_after set), buildRows appends the gap
  // marker immediately after the last loaded post, so this already lands on
  // "the end of what is loaded" without a dedicated load-latest binding —
  // none exists yet (AGENTS.md has no such API; see the report).
  const goToLatest = () => {
    scrollToIndex(rows.length - 1, { align: 'end' })
    atBottom.current = true
    far.current = false
    setJumpVisible(false)
    resetNewPosts()
  }

  const renderRow = (r: Row) => {
    switch (r.kind) {
      case 'more':
        // The label is always rendered and only hidden, so the row's height
        // never changes when loading starts: the scroll anchor is captured
        // just before this re-render, and a growing row above it would push
        // the anchored post down after the capture.
        return (
          <div className="py-3 text-center text-xs text-fg-muted">
            <span className={loadingOlder ? undefined : 'invisible'}>{t('feed.loadingOlder')}</span>
          </div>
        )
      case 'day':
        return (
          <div className="flex items-center px-4 py-2">
            <div className="h-px flex-1 bg-line" />
            <span className="px-3 text-xs font-semibold text-fg-muted">{formatDay(r.ms, locale)}</span>
            <div className="h-px flex-1 bg-line" />
          </div>
        )
      case 'new':
        return (
          <div className="flex items-center px-4 py-1">
            <div className="h-px flex-1 bg-danger" />
            <span className="px-3 text-xs font-semibold text-danger">{t('feed.new')}</span>
            <div className="h-px flex-1 bg-danger" />
          </div>
        )
      case 'gap':
        return <div role="status" className="py-2 text-center text-xs text-fg-muted">{t('feed.gap')}</div>
      case 'threadReplies':
        return (
          <div className="flex items-center px-4 py-2">
            <div className="h-px flex-1 bg-line" />
            <span className="px-3 text-xs font-semibold text-fg-muted">{t('thread.replies', { n: String(r.count) })}</span>
            <div className="h-px flex-1 bg-line" />
          </div>
        )
      case 'post':
        return <PostItem serverId={serverId} post={r.post} head={r.head} me={me} locale={locale} variant={variant} crt={data.crt} replyContext={r.replyContext} isInlineReply={r.isInlineReply} actions={actions} editing={r.post.id === editingId} />
    }
  }

  // A real wheel/touch gesture: a fresh budget for automatic history loads,
  // and at the top — where it fires no scroll event — a try of its own (the
  // user asked; a load that changed nothing does not block it).
  const onUserGesture = () => {
    userScrolling.current = true
    shift.onUserInput()
    autoLoads.current = 0
    lastLoad.current = null
    const el = scroller.current
    if (el && el.scrollTop < NEAR_TOP) fillViewportIfShort()
  }

  return (
    // The jump-to-latest button must NOT be a child of the scrolling element
    // itself: an absolutely positioned descendant of an overflow:auto box
    // anchors `bottom` to the bottom of the box's full *scrollable* content,
    // not its visible clientHeight, so it scrolls away with the content and
    // lands off-screen the moment the user scrolls up (measured: bottom:-5926px
    // in a channel scrolled near the top) instead of floating in place. This
    // outer div (not scrolling, `relative`) is the button's containing block;
    // the inner div is the actual `overflow-y-auto` scroller.
    <div className="relative min-h-0 flex-1">
      <div
        ref={scroller}
        onScroll={onScroll}
        onWheel={onUserGesture}
        onTouchMove={onUserGesture}
        role="log"
        aria-label={t('feed.label')}
        data-feed={variant}
        tabIndex={-1}
        className="relative h-full overflow-y-auto pb-2 [overflow-anchor:none]"
      >
        {!rows.length && (
          <div className="absolute inset-0 flex items-center justify-center text-fg-muted">{t(data.loaded ? 'feed.empty' : 'feed.loading')}</div>
        )}
        <div ref={sizer} style={{ height: v.getTotalSize(), position: 'relative', width: '100%' }}>
          {v.getVirtualItems().map((it) => (
            <div
              key={it.key}
              data-index={it.index}
              data-key={it.key}
              data-kind={rows[it.index].kind}
              ref={v.measureElement}
              style={{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${it.start}px)` }}
            >
              {renderRow(rows[it.index])}
            </div>
          ))}
        </div>
      </div>
      <button
        type="button"
        onClick={goToLatest}
        aria-hidden={!jumpVisible}
        tabIndex={jumpVisible ? 0 : -1}
        aria-label={newCount > 0 ? t('feed.jumpToLatestNew', { n: String(newCount) }) : t('feed.jumpToLatest')}
        title={newCount > 0 ? t('feed.jumpToLatestNew', { n: String(newCount) }) : t('feed.jumpToLatest')}
        className={`absolute bottom-4 right-5 z-10 flex h-9 w-9 items-center justify-center rounded-full bg-panel text-fg shadow-lg ring-1 ring-line transition-opacity duration-150 ${jumpVisible ? 'opacity-100' : 'pointer-events-none opacity-0'}`}
      >
        <IconArrowDown size={18} />
        {newCount > 0 && (
          <span
            aria-hidden="true"
            className="absolute -right-1 -top-1 min-w-4 rounded-full bg-accent px-1 text-center text-[10px] font-bold leading-4 text-accent-fg"
          >
            {newCount > NEW_BADGE_CAP ? '99+' : newCount}
          </span>
        )}
      </button>
      {toastHost !== undefined && <ToastHost priority={toastHost} />}
    </div>
  )
}

// ToastHost: the feed's toast host — a box over the feed's visible area
// that takes no pointer events itself.
function ToastHost({ priority }: { priority: number }) {
  const ref = useRef<HTMLDivElement>(null)
  useToastHost(ref, priority, 'feed')
  return <div ref={ref} data-toast-host="feed" className="pointer-events-none absolute inset-0" />
}
