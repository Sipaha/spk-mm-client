import { useEffect, useMemo, useRef, useState } from 'react'
import type { FileView } from '../api/types'
import { t } from '../i18n'
import { isShortcut } from '../keyboard'
import { mediaURL } from '../media'
import { useLiveEpoch } from '../store'
import { IconChevronLeft, IconChevronRight } from './icons'
import { useTextFile } from './textFile'

const SEARCH_DEBOUNCE = 150
const MATCH_CAP = 5000

interface Match {
  start: number
  end: number
}

// foldForSearch lowercases text for case-insensitive matching while
// guaranteeing the result has exactly the same length (and so the same
// offsets) as the input. Plain String.prototype.toLowerCase() isn't
// length-preserving for a handful of code points — e.g. 'İ' (U+0130) becomes
// two UTF-16 units ('i' + a combining dot above, U+0307) — which would shift
// every match offset found after it, misaligning the slice taken from the
// original text. The fast path (a single native toLowerCase() call) covers
// the overwhelming majority of real text; the per-unit fallback (keeping a
// unit as-is whenever its lowercase form isn't exactly one unit long) only
// runs on the rare text where lengths actually differ.
function foldForSearch(text: string): string {
  const fast = text.toLowerCase()
  if (fast.length === text.length) return fast
  let out = ''
  for (let i = 0; i < text.length; i++) {
    const lower = text[i].toLowerCase()
    out += lower.length === 1 ? lower : text[i]
  }
  return out
}

// findMatches scans for a case-insensitive substring, stopping one match
// past the cap: enough to tell "more exist" honestly without walking the
// rest of a pathological query (e.g. "a" over 1 MiB of "aaaa…") to the end.
function findMatches(lowerText: string, needle: string): { matches: Match[]; more: boolean } {
  if (!needle) return { matches: [], more: false }
  const matches: Match[] = []
  let from = 0
  for (;;) {
    const i = lowerText.indexOf(needle, from)
    if (i === -1) break
    matches.push({ start: i, end: i + needle.length })
    if (matches.length > MATCH_CAP) break
    from = i + needle.length
  }
  const more = matches.length > MATCH_CAP
  return { matches: more ? matches.slice(0, MATCH_CAP) : matches, more }
}

// TextView is the viewer's text panel: full width, up to 1 MiB via
// mediaURL(…, 'text', id, { full: '1' }), and an in-file search. Kept
// self-contained (its own header row, its own key handling) so it doesn't
// need to know about the rest of the viewer beyond the file it's showing.
export function TextView({
  serverId,
  file,
  autoFocusSearch,
}: {
  serverId: number
  file: FileView
  // Focus (and select) the search field once, right after mount — used by
  // Viewer.tsx when Ctrl+F in a markdown file's Rendered view switches to
  // this component (Source): there is no search field to intercept for
  // until this mounts, so the viewer hands off the focus request instead.
  autoFocusSearch?: boolean
}) {
  const res = useTextFile(mediaURL(serverId, 'text', file.id, { full: '1' }), useLiveEpoch(serverId))
  const text = res.status === 'ok' ? res.text : ''
  const lowerText = useMemo(() => foldForSearch(text), [text])

  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const [current, setCurrent] = useState(0)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const inputRef = useRef<HTMLInputElement>(null)
  const currentRef = useRef<HTMLElement | null>(null)

  const changeQuery = (v: string) => {
    setQuery(v)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setDebounced(v), SEARCH_DEBOUNCE)
  }
  // Clears both the field and the active search immediately — used by
  // Escape, which must drop the highlights/counter at once, not 150 ms
  // later like a normal keystroke.
  const clearQuery = () => {
    clearTimeout(timer.current)
    setQuery('')
    setDebounced('')
  }
  useEffect(() => () => clearTimeout(timer.current), [])

  // Mount-once, not on every `autoFocusSearch` change: the caller passes a
  // read-once flag (Viewer.tsx's `consumeFocusSearch`), true only for the
  // very render that mounts this component.
  useEffect(() => {
    if (autoFocusSearch) {
      inputRef.current?.focus()
      inputRef.current?.select()
    }
  }, [])

  const needle = foldForSearch(debounced.trim())
  const { matches, more } = useMemo(() => findMatches(lowerText, needle), [lowerText, needle])
  // A fresh search (or the text changing under it) jumps back to the first
  // match, like a browser's find-in-page.
  useEffect(() => setCurrent(0), [lowerText, needle])
  const safeCurrent = matches.length === 0 ? 0 : Math.min(current, matches.length - 1)

  useEffect(() => {
    currentRef.current?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
  }, [safeCurrent, matches])

  // Ctrl+F is intercepted at the window so it works no matter where focus
  // is in the viewer, and so the webview's own find-in-page never opens.
  // Matched by physical key (`code`), not `key` — see keyboard.ts: `key`
  // is 'а' for this same key on a Russian layout, so matching it directly
  // would silently never fire there.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isShortcut(e, 'KeyF', { ctrl: true })) {
        e.preventDefault()
        inputRef.current?.focus()
        inputRef.current?.select()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const goNext = () => matches.length > 0 && setCurrent((safeCurrent + 1) % matches.length)
  const goPrev = () => matches.length > 0 && setCurrent((safeCurrent - 1 + matches.length) % matches.length)

  const onInputKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      if (e.shiftKey) goPrev()
      else goNext()
    } else if (e.key === 'Escape') {
      if (query) {
        // Clear first; an empty field's Escape is left alone so it bubbles
        // to the viewer's own handler and closes it, as before.
        e.preventDefault()
        e.stopPropagation()
        clearQuery()
      }
    } else if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      // Let the input move its caret; just don't let the viewer page files.
      e.stopPropagation()
    }
  }

  const renderText = (): React.ReactNode => {
    if (!needle || matches.length === 0) return text
    const parts: React.ReactNode[] = []
    let pos = 0
    matches.forEach((m, i) => {
      if (m.start > pos) parts.push(text.slice(pos, m.start))
      const isCurrent = i === safeCurrent
      parts.push(
        <mark
          key={m.start}
          ref={
            isCurrent
              ? (el) => {
                  currentRef.current = el
                }
              : undefined
          }
          data-current={isCurrent ? 'true' : undefined}
          className={isCurrent ? 'bg-accent text-black' : 'bg-yellow-500/70 text-black'}
        >
          {text.slice(m.start, m.end)}
        </mark>,
      )
      pos = m.end
    })
    if (pos < text.length) parts.push(text.slice(pos))
    return parts
  }

  const count = matches.length === 0 ? 0 : safeCurrent + 1
  const total = more ? `${MATCH_CAP}+` : String(matches.length)

  return (
    <div className="flex h-full w-full flex-col gap-1">
      <div className="flex shrink-0 items-center gap-2">
        <input
          ref={inputRef}
          type="text"
          aria-label={t('viewer.search.label')}
          placeholder={t('viewer.search.label')}
          value={query}
          onChange={(e) => changeQuery(e.target.value)}
          onKeyDown={onInputKeyDown}
          className="w-56 rounded border border-line bg-app px-2 py-1 text-sm text-fg focus:border-accent focus:outline-none"
        />
        {needle && (
          <>
            <span className="text-xs text-fg-muted">{t('viewer.search.counter', { i: String(count), n: total })}</span>
            <button
              type="button"
              aria-label={t('viewer.search.prev')}
              title={t('viewer.search.prev')}
              disabled={matches.length === 0}
              onClick={goPrev}
              className="flex items-center justify-center rounded px-1.5 text-fg-muted hover:bg-hover hover:text-fg disabled:opacity-40"
            >
              <IconChevronLeft />
            </button>
            <button
              type="button"
              aria-label={t('viewer.search.next')}
              title={t('viewer.search.next')}
              disabled={matches.length === 0}
              onClick={goNext}
              className="flex items-center justify-center rounded px-1.5 text-fg-muted hover:bg-hover hover:text-fg disabled:opacity-40"
            >
              <IconChevronRight />
            </button>
          </>
        )}
      </div>
      {res.status === 'ok' && res.truncated && <p className="shrink-0 text-xs text-fg-muted">{t('file.truncated')}</p>}
      <pre className="min-h-0 flex-1 overflow-auto whitespace-pre rounded bg-code-bg p-3 font-mono text-xs leading-5 text-fg">
        {res.status === 'ok' ? renderText() : res.status === 'loading' ? t('file.loading') : t('err.no_file')}
      </pre>
    </div>
  )
}
