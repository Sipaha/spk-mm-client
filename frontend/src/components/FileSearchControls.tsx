import { forwardRef } from 'react'
import { t } from '../i18n'
import { IconChevronLeft, IconChevronRight } from './icons'

export const FileSearchControls = forwardRef<HTMLInputElement, {
  query: string
  active: boolean
  current: number
  total: string
  disabled: boolean
  onQuery(value: string): void
  onClear(): void
  onNext(): void
  onPrev(): void
}>(({ query, active, current, total, disabled, onQuery, onClear, onNext, onPrev }, ref) => {
  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      if (e.shiftKey) onPrev()
      else onNext()
    } else if (e.key === 'Escape' && query) {
      e.preventDefault()
      e.stopPropagation()
      onClear()
    } else if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      e.stopPropagation()
    }
  }
  return (
    // Both columns are permanently reserved. Match controls appear to the
    // left after the debounce, while the input stays in the fixed right
    // column; DOM order still keeps keyboard focus on the input first.
    <span className="grid w-[21rem] shrink-0 grid-cols-[1fr_14rem] items-center gap-1.5">
      <input
        ref={ref}
        type="text"
        aria-label={t('viewer.search.label')}
        placeholder={t('viewer.search.label')}
        value={query}
        onChange={(e) => onQuery(e.target.value)}
        onKeyDown={onKeyDown}
        className="col-start-2 row-start-1 w-full rounded border border-line bg-app px-2 py-1 text-sm text-fg focus:border-accent focus:outline-none"
      />
      <span className={`col-start-1 row-start-1 flex min-w-0 items-center justify-end gap-0.5 ${active ? '' : 'invisible'}`} aria-hidden={active ? undefined : 'true'}>
        <span className="whitespace-nowrap text-xs text-fg-muted">{t('viewer.search.counter', { i: String(current), n: total })}</span>
        <button type="button" tabIndex={active ? 0 : -1} aria-label={t('viewer.search.prev')} title={t('viewer.search.prev')} disabled={!active || disabled} onClick={onPrev} className="flex items-center justify-center rounded px-1 text-fg-muted hover:bg-hover hover:text-fg disabled:opacity-40">
          <IconChevronLeft />
        </button>
        <button type="button" tabIndex={active ? 0 : -1} aria-label={t('viewer.search.next')} title={t('viewer.search.next')} disabled={!active || disabled} onClick={onNext} className="flex items-center justify-center rounded px-1 text-fg-muted hover:bg-hover hover:text-fg disabled:opacity-40">
          <IconChevronRight />
        </button>
      </span>
    </span>
  )
})

FileSearchControls.displayName = 'FileSearchControls'
