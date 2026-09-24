import { useVirtualizer } from '@tanstack/react-virtual'
import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { ChannelDTO } from '../api/types'
import { formatDay } from '../format'
import { t } from '../i18n'
import { buildRows, type Row } from './feedRows'
import { PostItem, type PostActions } from './PostItem'

interface Props {
  channel: ChannelDTO
  me: { id: string; username: string }
  locale: string
  actions: PostActions
  editingId: string | null
  onLoadOlder(): Promise<boolean>
}

const NEAR_TOP = 300
const NEAR_BOTTOM = 48
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

// Feed must be keyed by channel id: another channel is a fresh mount, so
// the scroll bookkeeping below never leaks between channels.
export function Feed({ channel, me, locale, actions, editingId, onLoadOlder }: Props) {
  const rows = useMemo(() => buildRows(channel), [channel])
  const scroller = useRef<HTMLDivElement>(null)
  const ready = useRef(false)
  const atBottom = useRef(true)
  const anchor = useRef<string | null>(null)
  const anchorOffset = useRef(0) // anchor row's distance below the viewport top, px
  const userScrolling = useRef(false) // a real wheel/touch gesture since the last restore
  const loading = useRef(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const v = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: (i) => estimate(rows[i]),
    getItemKey: (i) => rows[i].key,
    overscan: 8,
  })

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
    if (loading.current || !channel.has_more) return
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
  // viewport (e.g. a short first page). Runs after every rows change, not
  // just the first mount, so the feed can't get stuck under-filled with
  // has_more still true after one page wasn't enough.
  const fillViewportIfShort = () => {
    requestAnimationFrame(() => {
      const el = scroller.current
      if (el && channel.has_more && el.scrollHeight <= el.clientHeight) void loadOlder()
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
    requestAnimationFrame(() => {
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
        v.scrollToIndex(i, { align: 'start' })
      } else {
        v.scrollToIndex(rows.length - 1, { align: 'end' })
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
    if (atBottom.current || (last.kind === 'post' && last.post.pending)) v.scrollToIndex(rows.length - 1, { align: 'end' })
    fillViewportIfShort()
  }, [rows, v]) // loadOlder is recreated every render; the effect only needs rows

  // Scrolls the post being edited into view when editing starts. Matched by
  // the post's real id, not the row key: a just-confirmed own post keeps its
  // earlier pending_post_id as its row key (see feedRows), but editingId —
  // set by edit()/editLastOwn() — is always the real post id.
  useLayoutEffect(() => {
    if (!editingId) return
    const i = rows.findIndex((r) => r.kind === 'post' && r.post.id === editingId)
    if (i >= 0) v.scrollToIndex(i, { align: 'auto' })
  }, [editingId]) // only when editing starts, not on every new post

  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM
    if (loading.current && anchor.current) captureAnchor() // still waiting for the page: track where the user is now
    if (el.scrollTop < NEAR_TOP) void loadOlder()
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
      case 'post':
        return <PostItem post={r.post} head={r.head} me={me} locale={locale} crt={channel.crt} actions={actions} editing={r.post.id === editingId} />
    }
  }

  const onUserGesture = () => {
    userScrolling.current = true
  }

  return (
    <div
      ref={scroller}
      onScroll={onScroll}
      onWheel={onUserGesture}
      onTouchMove={onUserGesture}
      role="log"
      aria-label={t('feed.label')}
      className="relative min-h-0 flex-1 overflow-y-auto pb-2"
    >
      {!rows.length && (
        <div className="absolute inset-0 flex items-center justify-center text-fg-muted">{t(channel.loaded ? 'feed.empty' : 'feed.loading')}</div>
      )}
      <div style={{ height: v.getTotalSize(), position: 'relative', width: '100%' }}>
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
  )
}
