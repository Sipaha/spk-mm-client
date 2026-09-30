import { Fragment, useCallback, useEffect, useLayoutEffect, useRef } from 'react'
import type { SearchHit, ServerDTO } from '../api/types'
import { closeSearch, emojiInfo, loadMoreSearch, openHit, openLink, retrySearch } from '../chat'
import { formatDay, formatLocale } from '../format'
import { t } from '../i18n'
import { useStore, type SearchSession } from '../store'
import { useNarrow } from '../useNarrow'
import { IconChevronLeft, IconClose } from './icons'
import { SearchBox } from './SearchBox'
import { SearchHitItem } from './SearchHitItem'

// NEAR_END: how close to the list's end (px) the next page is asked.
const NEAR_END = 400
const dayKey = (ms: number) => new Date(ms).toDateString()

interface Anchor {
  id: string
  offset: number
}

// readAnchor: the first hit on screen and where it is against the
// list's top — the position a width change or a remount restores (a bare
// scrollTop would land elsewhere once the cards rewrap).
function readAnchor(list: HTMLElement): Anchor | null {
  const top = list.scrollTop
  for (const el of list.querySelectorAll<HTMLElement>('[data-hit-id]')) {
    if (el.offsetTop + el.offsetHeight > top) return { id: el.dataset.hitId!, offset: el.offsetTop - top }
  }
  return null
}

function applyAnchor(list: HTMLElement, a: Anchor | null) {
  if (!a) return
  const el = list.querySelector<HTMLElement>(`[data-hit-id="${CSS.escape(a.id)}"]`)
  if (el) list.scrollTop = el.offsetTop - a.offset
}

// SearchPane: the search results in the right panel (spec «Поиск»,
// Секция 2) — the thread panel's place, width and narrow overlay (App.tsx
// shows one of them). Day separators, the hits (SearchHitItem), the next
// page as the list nears its end, and the states: searching, nothing
// found, a failure (offline apart) with Retry, the session's page limit.
// Its position is an anchor kept in the session: the panel comes back
// where it was after a thread opened from it, and a width change keeps the
// same hit on top. In a narrow window it overlays the feed with its own
// query field ("← Back to channel" leaves it; the session stays).
export function SearchPane({ server, search }: { server: ServerDTO; search: SearchSession }) {
  const narrow = useNarrow()
  const paneRef = useRef<HTMLElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const anchor = useRef<Anchor | null>(search.anchor)
  const gen = search.gen
  const locale = formatLocale()

  // The anchor goes to the store once scrolling pauses and on unmount —
  // not per frame: every store change re-renders App.
  const saveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const saveAnchor = useCallback(() => {
    clearTimeout(saveTimer.current)
    useStore.getState().patchSearch(gen, { anchor: anchor.current })
  }, [gen])
  useEffect(() => saveAnchor, [saveAnchor])

  const nearEnd = () => {
    const el = listRef.current
    if (el && el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_END) void loadMoreSearch()
  }

  const onScroll = () => {
    const el = listRef.current
    if (!el) return
    anchor.current = readAnchor(el)
    clearTimeout(saveTimer.current)
    saveTimer.current = setTimeout(saveAnchor, 200)
    nearEnd()
  }

  // Back where it was (a thread opened from the results, then closed).
  useLayoutEffect(() => {
    if (listRef.current) applyAnchor(listRef.current, anchor.current)
  }, [])
  // A new session starts at the top.
  useLayoutEffect(() => {
    anchor.current = search.anchor
    if (!search.anchor && listRef.current) listRef.current.scrollTop = 0
  }, [gen])
  // A width change rewraps the cards: the same hit stays on top.
  useEffect(() => {
    const el = listRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    let width = el.clientWidth
    const ro = new ResizeObserver(() => {
      if (el.clientWidth === width) return
      width = el.clientWidth
      applyAnchor(el, anchor.current)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  // A page that added nothing new (dedup) or a short list: ask on.
  useEffect(nearEnd, [search.hits.length, search.loading, search.hasNext])

  const close = () => {
    closeSearch()
    document.querySelector<HTMLElement>('[data-feed="channel"]')?.focus()
  }
  const closeRef = useRef(close)
  closeRef.current = close

  // Esc closes — only with focus inside the panel, no popover over it
  // (they portal outside) and nobody took the key (the field's own Esc
  // steps, an open suggestion list). As ThreadPane.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      if (!paneRef.current?.contains(document.activeElement)) return
      closeRef.current()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  // A hit opened over the feed (narrow) lets the feed show it.
  const onOpen = useCallback(
    async (hit: SearchHit) => {
      await openHit(hit)
      const s = useStore.getState()
      if (narrow && s.search?.gen === gen && s.rhs === 'search') s.setRhs(null)
    },
    [narrow, gen],
  )

  const { hits, loading, error } = search
  const failed = error && (
    <div role="alert" className="flex flex-col items-center gap-2 px-4 py-6 text-center text-sm">
      <span className={error === 'offline' ? 'text-fg-muted' : 'text-danger'}>{t(error === 'offline' ? 'search.offline' : 'search.error')}</span>
      <button type="button" className="rounded border border-line px-2 py-0.5 text-xs text-fg hover:bg-hover" onClick={() => void retrySearch()}>
        {t('search.retry')}
      </button>
    </div>
  )

  return (
    <aside
      ref={paneRef}
      role="complementary"
      aria-label={t('search.title')}
      style={narrow ? undefined : { width: 'var(--spk-thread-width, 420px)' }}
      className={narrow ? 'absolute inset-0 z-20 flex min-h-0 flex-col bg-app' : 'relative flex min-h-0 shrink-0 flex-col border-l border-line bg-app'}
    >
      <header className="flex h-8 items-center gap-2 border-b border-line bg-panel px-3">
        {narrow && (
          <button
            type="button"
            aria-label={t('thread.backToChannel')}
            title={t('thread.backToChannel')}
            onClick={() => useStore.getState().setRhs(null)}
            className="flex shrink-0 items-center gap-1 rounded px-1.5 py-1 text-sm text-fg-muted hover:bg-hover hover:text-fg"
          >
            <IconChevronLeft size={18} />
            {t('thread.backToChannel')}
          </button>
        )}
        <h2 className="min-w-0 flex-1 truncate font-semibold text-fg" title={search.submitted}>
          {t('search.title')}
          {!narrow && <span className="font-normal text-fg-muted"> · {search.submitted}</span>}
        </h2>
        <button
          type="button"
          aria-label={t('search.close')}
          title={t('search.close')}
          onClick={close}
          className="flex shrink-0 items-center justify-center rounded px-1.5 py-1 text-fg-muted hover:bg-hover hover:text-fg"
        >
          <IconClose />
        </button>
      </header>
      {narrow && (
        <div className="border-b border-line px-3 py-1.5">
          <SearchBox variant="pane" />
        </div>
      )}
      <div ref={listRef} onScroll={onScroll} data-search-results="" aria-busy={loading} className="relative min-h-0 flex-1 overflow-y-auto pb-2">
        {hits.length === 0 ? (
          loading ? (
            <div role="status" className="px-4 py-6 text-center text-sm text-fg-muted">
              {t('search.loading')}
            </div>
          ) : (
            failed || (
              <div role="status" className="px-4 py-6 text-center text-sm text-fg-muted">
                {t('search.empty')}
              </div>
            )
          )
        ) : (
          <>
            {hits.map((hit, i) => (
              <Fragment key={hit.id}>
                {(i === 0 || dayKey(hits[i - 1].create_at) !== dayKey(hit.create_at)) && (
                  <div className="flex items-center px-4 pb-1 pt-2">
                    <div className="h-px flex-1 bg-line" />
                    <span className="px-3 text-xs font-semibold text-fg-muted">{formatDay(hit.create_at, locale)}</span>
                    <div className="h-px flex-1 bg-line" />
                  </div>
                )}
                <SearchHitItem
                  serverId={server.id}
                  hit={hit}
                  terms={search.terms}
                  me={server.username}
                  locale={locale}
                  onOpen={onOpen}
                  onLink={openLink}
                  emojiInfo={emojiInfoFor(server.id)}
                />
              </Fragment>
            ))}
            {loading && (
              <div role="status" className="px-4 py-2 text-center text-xs text-fg-muted">
                {t('search.loading')}
              </div>
            )}
            {failed}
            {search.limitReached && (
              <div role="status" className="px-4 py-3 text-center text-xs text-fg-muted">
                {t('search.limit')}
              </div>
            )}
          </>
        )}
      </div>
    </aside>
  )
}

// emojiInfoFor: one stable function per server (SearchHitItem is memoized).
const emojiInfoCache = new Map<number, () => ReturnType<typeof emojiInfo>>()
function emojiInfoFor(serverId: number) {
  let f = emojiInfoCache.get(serverId)
  if (!f) {
    f = () => emojiInfo(serverId)
    emojiInfoCache.set(serverId, f)
  }
  return f
}
