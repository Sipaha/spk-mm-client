import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { FileView } from '../api/types'
import { t } from '../i18n'
import { isShortcut } from '../keyboard'
import { mediaURL } from '../media'
import { useLiveEpoch } from '../store'
import { Markdown } from './Markdown'
import { FileSearchControls } from './FileSearchControls'
import { useTextFile } from './textFile'

// MarkdownView is the viewer's rendered pane for a markdown file: full
// width, scrolling, up to 1 MiB via ?full=1 (same source TextView's source
// pane reads). Remote images become links and links go to the system
// browser, same as the post's Markdown component.
export function MarkdownView({
  serverId,
  file,
  me,
  onLink,
  searchHost,
  query = '',
  onQueryChange,
}: {
  serverId: number
  file: FileView
  me: string
  onLink(href: string): void
  searchHost?: HTMLElement | null
  query?: string
  onQueryChange?(value: string): void
}) {
  const res = useTextFile(mediaURL(serverId, 'text', file.id, { full: '1' }), useLiveEpoch(serverId))
  const [find, setFind] = useState(query.trim())
  const [current, setCurrent] = useState(0)
  const [count, setCount] = useState(0)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const inputRef = useRef<HTMLInputElement>(null)
  const rootRef = useRef<HTMLDivElement>(null)

  const changeQuery = (value: string) => {
    onQueryChange?.(value)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setFind(value.trim()), 150)
  }
  const clearQuery = () => {
    clearTimeout(timer.current)
    onQueryChange?.('')
    setFind('')
  }
  useEffect(() => () => clearTimeout(timer.current), [])
  useEffect(() => {
    const marks = rootRef.current?.querySelectorAll<HTMLElement>('mark[data-file-find="true"]') ?? []
    setCount(marks.length)
    setCurrent(0)
  }, [find, res.status])
  useEffect(() => {
    const marks = rootRef.current?.querySelectorAll<HTMLElement>('mark[data-file-find="true"]') ?? []
    marks.forEach((mark, index) => {
      const active = index === current && marks.length > 0
      if (active) mark.setAttribute('data-current', 'true')
      else mark.removeAttribute('data-current')
      mark.classList.toggle('bg-accent', active)
      mark.classList.toggle('text-accent-fg', active)
      mark.classList.toggle('bg-mention-bg', !active)
      mark.classList.toggle('text-mention-fg', !active)
    })
    marks[current]?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
  }, [current, count])
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
  const goNext = () => count > 0 && setCurrent((current + 1) % count)
  const goPrev = () => count > 0 && setCurrent((current - 1 + count) % count)
  const controls = (
    <FileSearchControls
      ref={inputRef}
      query={query}
      active={!!find}
      current={count === 0 ? 0 : current + 1}
      total={String(count)}
      disabled={count === 0}
      onQuery={changeQuery}
      onClear={clearQuery}
      onNext={goNext}
      onPrev={goPrev}
    />
  )
  return (
    // A full document uses the app surface so code can sit on the raised
    // panel token. The inner article owns the readable measure and spacing.
    <div ref={rootRef} className="h-full w-full overflow-auto rounded bg-app px-8 py-6 sm:px-10 sm:py-8">
      {searchHost ? createPortal(controls, searchHost) : null}
      {res.status === 'ok' ? (
        <article className="mx-auto w-full max-w-4xl">
          {res.truncated && <p className="mb-2 text-xs text-fg-muted">{t('file.truncated')}</p>}
          <Markdown text={res.text} me={me} onLink={onLink} serverId={serverId} document find={find} />
        </article>
      ) : (
        <p className="text-fg-muted">{res.status === 'loading' ? t('file.loading') : t('err.no_file')}</p>
      )}
    </div>
  )
}
