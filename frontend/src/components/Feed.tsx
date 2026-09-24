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
    const top = v.getVirtualItems().find((it) => rows[it.index]?.kind === 'post')
    anchor.current = top ? rows[top.index].key : null
    try {
      if (!(await onLoadOlder())) anchor.current = null
    } finally {
      loading.current = false
      setLoadingOlder(false)
    }
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
      requestAnimationFrame(() => {
        const el = scroller.current
        if (el && el.scrollHeight <= el.clientHeight) void loadOlder() // too short to scroll: fetch history now
      })
      return
    }
    if (anchor.current) {
      // History landed above: keep the post that was on top in place.
      const i = rows.findIndex((r) => r.key === anchor.current)
      anchor.current = null
      if (i >= 0) v.scrollToIndex(i, { align: 'start' })
      return
    }
    const last = rows[rows.length - 1]
    if (atBottom.current || (last.kind === 'post' && last.post.pending)) v.scrollToIndex(rows.length - 1, { align: 'end' })
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
