import { useVirtualizer } from '@tanstack/react-virtual'
import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { ChannelDTO } from '../api/types'
import { formatDay } from '../format'
import { t } from '../i18n'
import { buildRows, firstVisiblePostIndex, type Row } from './feedRows'
import { PostItem, type PostActions } from './PostItem'

interface Props {
  channel: ChannelDTO
  me: { id: string; username: string }
  locale: string
  actions: PostActions
  onLoadOlder(): Promise<boolean>
}

const NEAR_TOP = 300
const NEAR_BOTTOM = 48
const estimate = (r: Row) => (r.kind === 'post' ? (r.head ? 64 : 28) : 36)

// Feed must be keyed by channel id: another channel is a fresh mount, so
// the scroll bookkeeping below never leaks between channels.
export function Feed({ channel, me, locale, actions, onLoadOlder }: Props) {
  const rows = useMemo(() => buildRows(channel), [channel])
  const scroller = useRef<HTMLDivElement>(null)
  const ready = useRef(false)
  const atBottom = useRef(true)
  const anchor = useRef<string | null>(null)
  const anchorOffset = useRef(0) // anchor row's distance below the viewport top, px
  const loading = useRef(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const v = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: (i) => estimate(rows[i]),
    getItemKey: (i) => rows[i].key,
    overscan: 8,
  })

  const loadOlder = async () => {
    if (loading.current || !channel.has_more) return
    loading.current = true
    setLoadingOlder(true)
    // Anchor on the first post at or after the *true* first visible row
    // (v.range.startIndex), not the first entry of getVirtualItems() — that
    // list also carries the overscan buffer rendered above the viewport,
    // which would anchor on a row that was never actually on screen and
    // jump the view once older rows are prepended.
    const el = scroller.current
    const i = firstVisiblePostIndex(rows, v.range?.startIndex ?? 0)
    const info = i >= 0 ? v.getOffsetForIndex(i, 'start') : undefined
    if (info && el) {
      anchor.current = rows[i].key
      anchorOffset.current = info[0] - el.scrollTop // preserve its on-screen position, not just align it to top
    } else {
      anchor.current = null
    }
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
      const i = rows.findIndex((r) => r.key === key)
      const info = i >= 0 ? v.getOffsetForIndex(i, 'start') : undefined
      if (info) v.scrollToOffset(Math.max(0, info[0] - offset), { align: 'start' })
      fillViewportIfShort()
      return
    }
    const last = rows[rows.length - 1]
    if (atBottom.current || (last.kind === 'post' && last.post.pending)) v.scrollToIndex(rows.length - 1, { align: 'end' })
    fillViewportIfShort()
  }, [rows, v]) // loadOlder is recreated every render; the effect only needs rows

  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM
    if (el.scrollTop < NEAR_TOP) void loadOlder()
  }

  const renderRow = (r: Row) => {
    switch (r.kind) {
      case 'more':
        return <div className="py-3 text-center text-xs text-neutral-500">{loadingOlder ? t('feed.loadingOlder') : ''}</div>
      case 'day':
        return (
          <div className="flex items-center px-4 py-2">
            <div className="h-px flex-1 bg-neutral-200" />
            <span className="px-3 text-xs font-semibold text-neutral-500">{formatDay(r.ms, locale)}</span>
            <div className="h-px flex-1 bg-neutral-200" />
          </div>
        )
      case 'new':
        return (
          <div className="flex items-center px-4 py-1">
            <div className="h-px flex-1 bg-red-400" />
            <span className="px-3 text-xs font-semibold text-red-600">{t('feed.new')}</span>
            <div className="h-px flex-1 bg-red-400" />
          </div>
        )
      case 'gap':
        return <div role="status" className="py-2 text-center text-xs text-neutral-500">{t('feed.gap')}</div>
      case 'post':
        return <PostItem post={r.post} head={r.head} me={me} locale={locale} crt={channel.crt} actions={actions} />
    }
  }

  return (
    <div ref={scroller} onScroll={onScroll} role="log" aria-label={t('feed.label')} className="relative min-h-0 flex-1 overflow-y-auto pb-2">
      {!rows.length && (
        <div className="absolute inset-0 flex items-center justify-center text-neutral-500">{t(channel.loaded ? 'feed.empty' : 'feed.loading')}</div>
      )}
      <div style={{ height: v.getTotalSize(), position: 'relative', width: '100%' }}>
        {v.getVirtualItems().map((it) => (
          <div
            key={it.key}
            data-index={it.index}
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
