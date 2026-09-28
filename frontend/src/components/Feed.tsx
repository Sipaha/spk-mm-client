import { useVirtualizer } from '@tanstack/react-virtual'
import { useCallback, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import type { ChannelDTO } from '../api/types'
import { formatDay } from '../format'
import { t } from '../i18n'
import { buildRows, type FeedVariant, type Row } from './feedRows'
import { IconArrowDown } from './icons'
import { PostItem, type PostActions } from './PostItem'
import { ScrollShift } from './scrollShift'

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
}

const NEAR_TOP = 300
const NEAR_BOTTOM = 48
const NEW_BADGE_CAP = 99
const MAX_CORRECTIONS = 10 // a few frames may pass before the anchor row is even (re-)mounted after scrollToOffset
const estimate = (r: Row) => (r.kind === 'post' ? (r.head ? 64 : 28) : 36)

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
export function Feed({ data, variant, serverId, me, locale, actions, editingId, onLoadOlder }: Props) {
  const rows = useMemo(() => buildRows(data, variant), [data, variant])
  const scroller = useRef<HTMLDivElement>(null)
  const ready = useRef(false)
  const atBottom = useRef(true)
  const anchor = useRef<string | null>(null)
  const anchorOffset = useRef(0) // anchor row's distance below the viewport top, px
  const userScrolling = useRef(false) // a real wheel/touch gesture since the last restore
  const loading = useRef(false)
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
    return new ScrollShift(() => scroller.current, () => sizer.current, rerender)
  })
  useEffect(() => () => shift.dispose(), [shift])
  useLayoutEffect(() => shift.apply()) // every commit: rows and shift reach the screen together
  const v = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: (i) => estimate(rows[i]),
    getItemKey: (i) => rows[i].key,
    overscan: 8,
    observeElementOffset: shift.observeOffset,
    scrollToFn: shift.scrollTo,
  })
  v.shouldAdjustScrollPositionOnItemSizeChange = shift.shouldAdjust
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
  const captureAnchor = () => {
    const el = scroller.current
    const found = el
      ? pickAnchor(
          [...el.querySelectorAll<HTMLElement>('[data-kind="post"]')].map((r) => {
            const b = r.getBoundingClientRect()
            return { key: r.dataset.key ?? '', top: b.top, bottom: b.bottom }
          }),
          el.getBoundingClientRect().top,
        )
      : null
    anchor.current = found?.key ?? null
    anchorOffset.current = found?.offset ?? 0
  }

  const loadOlder = async () => {
    if (loading.current || !data.has_more) return
    loading.current = true
    setLoadingOlder(true)
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
  // event, so onScroll would never ask again). A restore that worked puts
  // the feed a whole page below the top, so this doesn't cascade.
  const fillViewportIfShort = () => {
    frame('fill', () => {
      const el = scroller.current
      if (el && data.has_more && (el.scrollHeight <= el.clientHeight || el.scrollTop < NEAR_TOP)) void loadOlder()
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
    atBottom.current = distance < NEAR_BOTTOM
    if (!wasAtBottom && atBottom.current) resetNewPosts()
    if (loading.current && anchor.current) captureAnchor() // still waiting for the page: track where the user is now
    if (el.scrollTop < NEAR_TOP) void loadOlder()
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
        return <PostItem serverId={serverId} post={r.post} head={r.head} me={me} locale={locale} variant={variant} crt={data.crt} replyContext={r.replyContext} actions={actions} editing={r.post.id === editingId} />
    }
  }

  const onUserGesture = () => {
    userScrolling.current = true
    shift.onUserInput()
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
    </div>
  )
}
