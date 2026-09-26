import { useEffect, useRef } from 'react'
import type { DownloadView } from '../api/types'
import { downloadErrorMessage } from '../errors'
import { formatSize, formatTime } from '../format'
import { t } from '../i18n'
import { IconButton } from './FileCard'
import { fileKind } from './files'

const W = 360
const H = 420

const KIND_GLYPH: Record<string, string> = {
  image: '🖼', video: '🎬', audio: '🎵', text: '📄', markdown: '📄', other: '📎',
}

function kindGlyph(d: DownloadView): string {
  return KIND_GLYPH[fileKind({ id: String(d.id), name: d.name, size: d.size, mime: d.mime })]
}

// placePanel puts the panel under its button, or above it when there is no
// room below, always inside the viewport — same rule as the emoji picker's
// placePicker (frontend/src/components/EmojiPicker.tsx).
export function placePanel(anchor: Pick<DOMRect, 'top' | 'bottom' | 'right'>, vw: number, vh: number, h = H) {
  const left = Math.max(8, Math.min(anchor.right - W, vw - W - 8))
  const below = anchor.bottom + 4
  const top = below + h <= vh - 8 ? below : Math.max(8, anchor.top - h - 4)
  return { left, top }
}

interface Props {
  anchor: DOMRect
  downloads: DownloadView[]
  locale: string
  // primaryAction: what a row click / Enter does (chat.ts's
  // downloadPrimaryAction — Open when possible, else Show in folder, null
  // for an in-progress or deleted entry); kept out of this component so it
  // stays a pure, prop-driven view like EmojiPicker.
  primaryAction(d: DownloadView): (() => void) | null
  onOpen(id: number): void
  onReveal(id: number): void
  onRemove(id: number): void
  onClear(): void
  onClose(): void
}

// Downloads is the browser-like downloads panel: a portal into
// document.body (like EmojiPicker), closed by Esc or a click outside.
// Rows are plain divs (not <button>, which they nest — Open/Show/Remove
// are real buttons of their own): ↑/↓ moves focus between rows, Enter
// runs the row's primary action, and a row click other than on one of its
// buttons does the same.
export default function Downloads({ anchor, downloads, locale, primaryAction, onOpen, onReveal, onRemove, onClear, onClose }: Props) {
  const root = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) onClose()
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [onClose])

  const rows = () => [...(root.current?.querySelectorAll<HTMLElement>('[data-row]') ?? [])]

  const onListKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const el = e.target as HTMLElement
    if (el.dataset.row === undefined) return
    const i = Number(el.dataset.row)
    const list = rows()
    if (e.key === 'ArrowDown' && i + 1 < list.length) {
      e.preventDefault()
      list[i + 1].focus()
    } else if (e.key === 'ArrowUp' && i > 0) {
      e.preventDefault()
      list[i - 1].focus()
    }
  }

  const pos = placePanel(anchor, window.innerWidth, window.innerHeight)
  return (
    <div
      ref={root}
      role="dialog"
      aria-label={t('downloads.panelLabel')}
      className="fixed z-50 flex flex-col rounded-lg border border-line bg-panel text-fg shadow-xl"
      style={{ left: pos.left, top: pos.top, width: W, maxHeight: H }}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.preventDefault()
          e.stopPropagation()
          onClose()
        }
      }}
    >
      <div role="list" aria-label={t('downloads.panelLabel')} className="min-h-0 flex-1 overflow-y-auto p-1" onKeyDown={onListKey}>
        {downloads.length === 0 && <p className="p-3 text-xs text-fg-muted">{t('downloads.empty')}</p>}
        {downloads.map((d, i) => {
          const primary = primaryAction(d)
          return (
            <div
              key={d.id}
              role="listitem"
              data-row={i}
              tabIndex={i === 0 ? 0 : -1}
              onClick={(e) => {
                if (!(e.target as HTMLElement).closest('button')) primary?.()
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') primary?.()
              }}
              className={`flex items-center gap-2 rounded px-2 py-1.5 text-xs focus:bg-hover focus:outline-none ${primary ? 'cursor-pointer hover:bg-hover' : ''}`}
            >
              <span aria-hidden className="shrink-0 text-base">
                {kindGlyph(d)}
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate" title={d.name}>
                  {d.name}
                </p>
                {d.state === 'downloading' && (
                  <>
                    <p className="text-fg-muted">{t('downloads.progress', { received: formatSize(d.received), total: formatSize(d.size) })}</p>
                    <div className="mt-0.5 h-1 w-full overflow-hidden rounded bg-line">
                      <div className="h-full bg-accent" style={{ width: `${d.size > 0 ? Math.min(100, (d.received / d.size) * 100) : 0}%` }} />
                    </div>
                  </>
                )}
                {d.state === 'done' && d.exists && (
                  <p className="text-fg-muted">{t('downloads.savedAt', { size: formatSize(d.size), time: formatTime(d.finished_at, locale) })}</p>
                )}
                {d.state === 'done' && !d.exists && <p className="text-danger">{t('downloads.deleted')}</p>}
                {d.state === 'failed' && <p className="text-danger">{t('downloads.error', { detail: downloadErrorMessage(d.error) })}</p>}
              </div>
              <div className="flex shrink-0 items-center gap-0.5">
                {d.state === 'done' && d.exists && d.openable && (
                  <IconButton label={t('downloads.open', { name: d.name })} onClick={() => onOpen(d.id)}>
                    ↗
                  </IconButton>
                )}
                {d.state === 'done' && d.exists && (
                  <IconButton label={t('downloads.reveal', { name: d.name })} onClick={() => onReveal(d.id)}>
                    📂
                  </IconButton>
                )}
                {d.state !== 'downloading' && (
                  <IconButton label={t('downloads.remove', { name: d.name })} onClick={() => onRemove(d.id)}>
                    ✕
                  </IconButton>
                )}
              </div>
            </div>
          )
        })}
      </div>
      <div className="border-t border-line p-1.5">
        <button type="button" className="w-full rounded px-2 py-1 text-xs text-fg-muted hover:bg-hover hover:text-fg" onClick={onClear}>
          {t('downloads.clear')}
        </button>
      </div>
    </div>
  )
}
