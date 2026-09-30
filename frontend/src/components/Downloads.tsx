import type { ReactElement } from 'react'
import { useEffect, useRef } from 'react'
import type { DownloadView } from '../api/types'
import { downloadErrorMessage } from '../errors'
import { formatSize, formatTime } from '../format'
import { t } from '../i18n'
import { IconButton } from './FileCard'
import { fileKind, type FileKind } from './files'
import { IconAudio, IconClose, IconFile, IconFolder, IconImage, IconNote, IconOpenExternal, IconVideo, type IconProps } from './icons'
import { placeBelow } from './panelPosition'

const W = 360
const H = 420

// Record<FileKind, ...> (not Record<string, ...>): a new FileKind (like
// 'pdf', Task 2) that forgot this map is a compile error, not a crash at
// render (Cmp undefined → "Element type is invalid").
const KIND_ICON: Record<FileKind, (p: IconProps) => ReactElement> = {
  image: IconImage, video: IconVideo, audio: IconAudio, text: IconNote, markdown: IconNote, pdf: IconFile, other: IconFile,
}

function KindIcon({ d, className }: { d: DownloadView; className?: string }) {
  const Cmp = KIND_ICON[fileKind({ id: String(d.id), name: d.name, size: d.size, mime: d.mime })]
  return <Cmp className={className} />
}

// placePanel puts the panel under its button, or above it when there is no
// room below, always inside the viewport — see placeBelow (also used by the
// emoji picker's placePicker and PostMenu).
export function placePanel(anchor: Pick<DOMRect, 'top' | 'bottom' | 'right'>, vw: number, vh: number, h = H) {
  return placeBelow(anchor, vw, vh, W, h)
}

interface Props {
  // anchorEl: the header's downloads button — its rect places the panel, and it is
  // (a) excluded from the outside-click check, so the same click that
  // toggles it open doesn't also toggle it closed via that check, and (b)
  // where focus returns to once the panel closes.
  anchorEl: HTMLElement
  downloads: DownloadView[]
  locale: string
  // primaryAction: what a row's button does (chat.ts's
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
// document.body (like EmojiPicker), closed by Esc (a document-level
// listener — the panel's own focus state must not gate this) or a click
// outside (not on anchorEl, which has its own toggle). Each row's name is a
// real <button> (its primary action, or inert when there is none); Open/
// Show/Remove are sibling buttons, not nested inside it. ↑/↓ moves focus
// between the rows' primary buttons.
export default function Downloads({ anchorEl, downloads, locale, primaryAction, onOpen, onReveal, onRemove, onClear, onClose }: Props) {
  const root = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node
      if (root.current && !root.current.contains(target) && !anchorEl.contains(target)) onClose()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose, anchorEl])

  // Move focus into the panel on open (the first row's button, or the
  // panel itself when the list is empty) and back to the button that
  // opened it once this unmounts — same rule as Viewer.
  useEffect(() => {
    const first = root.current?.querySelector<HTMLElement>('[data-row]')
    ;(first ?? root.current)?.focus()
    return () => anchorEl.focus()
  }, [anchorEl])

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

  const pos = placePanel(anchorEl.getBoundingClientRect(), window.innerWidth, window.innerHeight)
  return (
    <div
      ref={root}
      role="dialog"
      aria-label={t('downloads.panelLabel')}
      tabIndex={-1}
      data-overlay="true"
      className="fixed z-50 flex flex-col rounded-lg border border-line bg-panel text-fg shadow-xl focus:outline-none"
      style={{ left: pos.left, top: pos.top, width: W, maxHeight: H }}
    >
      {downloads.length === 0 && <p className="p-3 text-xs text-fg-muted">{t('downloads.empty')}</p>}
      {downloads.length > 0 && (
        <div role="list" aria-label={t('downloads.panelLabel')} className="min-h-0 flex-1 overflow-y-auto p-1" onKeyDown={onListKey}>
          {downloads.map((d, i) => {
            const primary = primaryAction(d)
            return (
              <div key={d.id} role="listitem" className="flex items-center gap-2 rounded px-1 py-1.5 text-xs">
                <KindIcon d={d} className="shrink-0 text-fg-muted" />
                <button
                  type="button"
                  data-row={i}
                  aria-disabled={primary ? undefined : true}
                  onClick={() => primary?.()}
                  className={`min-w-0 flex-1 rounded px-1 py-0.5 text-left focus:bg-hover focus:outline-none ${primary ? 'cursor-pointer hover:bg-hover' : 'cursor-default'}`}
                >
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
                </button>
                <div className="flex shrink-0 items-center gap-0.5">
                  {d.state === 'done' && d.exists && d.openable && (
                    <IconButton label={t('downloads.open', { name: d.name })} onClick={() => onOpen(d.id)}>
                      <IconOpenExternal />
                    </IconButton>
                  )}
                  {d.state === 'done' && d.exists && (
                    <IconButton label={t('downloads.reveal', { name: d.name })} onClick={() => onReveal(d.id)}>
                      <IconFolder />
                    </IconButton>
                  )}
                  {d.state !== 'downloading' && (
                    <IconButton label={t('downloads.remove', { name: d.name })} onClick={() => onRemove(d.id)}>
                      <IconClose />
                    </IconButton>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}
      <div className="border-t border-line p-1.5">
        <button type="button" className="w-full rounded px-2 py-1 text-xs text-fg-muted hover:bg-hover hover:text-fg" onClick={onClear}>
          {t('downloads.clear')}
        </button>
      </div>
    </div>
  )
}
