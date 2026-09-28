import type { KeyboardEvent } from 'react'
import { useEffect, useRef } from 'react'
import type { AttachmentView } from '../api/types'
import { downloadErrorMessage } from '../errors'
import { formatSize } from '../format'
import { t } from '../i18n'
import { mediaURL, useLoadFailure } from '../media'
import { IconFile } from './icons'

// isImage: whether a staged attachment's own mime is worth trying as a
// picture — /media/<srv>/staged/<id> only ever serves raster types
// (internal/media's stagedPicture); anything else (or a load that fails —
// e.g. a huge/corrupt file the cache refuses) falls back to the file icon.
const isImage = (mime: string) => mime.startsWith('image/') && mime !== 'image/svg+xml'

function Chip({
  serverId,
  a,
  onRemove,
  onRetry,
  onKeyRemove,
  chipRef,
}: {
  serverId: number
  a: AttachmentView
  onRemove(): void
  onRetry(): void
  onKeyRemove(): void
  chipRef(el: HTMLDivElement | null): void
}) {
  const url = mediaURL(serverId, 'staged', a.id)
  const [failed, fail] = useLoadFailure(serverId, url)
  const showImage = isImage(a.mime) && !failed
  const pct = a.size > 0 ? Math.min(100, Math.round((a.sent / a.size) * 100)) : 0

  // Delete/Backspace removes the chip while it (or something inside it) has
  // focus — the tray has no other use for either key. onKeyRemove (not
  // onRemove directly) lets the tray remember where to send focus once the
  // removal actually lands (it goes through the API/store, not synchronously).
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Delete' || e.key === 'Backspace') {
      e.preventDefault()
      onKeyRemove()
    }
  }

  return (
    <div
      ref={chipRef}
      tabIndex={0}
      onKeyDown={onKeyDown}
      className="flex max-w-xs items-center gap-2 rounded border border-line bg-app px-2 py-1 text-xs focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
    >
      {showImage ? (
        <img src={url} alt="" onError={fail} className="h-8 w-8 shrink-0 rounded object-cover" />
      ) : (
        <span aria-hidden className="flex h-8 w-8 shrink-0 items-center justify-center rounded bg-hover">
          <IconFile />
        </span>
      )}
      <div className="min-w-0 flex-1">
        <div className="truncate" title={a.name}>
          {a.name}
        </div>
        {a.state === 'failed' ? (
          <div role="alert" className="flex items-center gap-1 text-danger">
            <span className="truncate">{t('attach.error', { detail: downloadErrorMessage(a.error) })}</span>
            <button type="button" aria-label={t('attach.retry', { name: a.name })} className="shrink-0 underline" onClick={onRetry}>
              {t('post.retry')}
            </button>
          </div>
        ) : (
          <div className="flex items-center gap-1.5">
            <span className="shrink-0 text-fg-muted">{formatSize(a.size)}</span>
            {a.state !== 'uploaded' && (
              <div className="h-1 w-16 shrink-0 overflow-hidden rounded bg-line" aria-hidden>
                <div className="h-full bg-accent transition-[width]" style={{ width: `${pct}%` }} />
              </div>
            )}
          </div>
        )}
      </div>
      <button
        type="button"
        aria-label={t('attach.remove', { name: a.name })}
        title={t('attach.remove', { name: a.name })}
        onClick={onRemove}
        className="shrink-0 rounded px-1 text-fg-muted hover:bg-hover hover:text-fg"
      >
        ×
      </button>
    </div>
  )
}

// AttachmentsTray: the composer's strip of chips above the input, one per
// attachment of the channel's next message (internal/attach.Attachment via
// api.Attachments/EventAttachmentsChanged) — a thumbnail (raster) or a type
// icon, name, size, upload progress or an error with Retry, and a remove
// button. Renders nothing when there is nothing attached.
export function AttachmentsTray({
  serverId,
  items,
  onRemove,
  onRetry,
  onFocusTextarea,
}: {
  serverId: number
  items: AttachmentView[]
  onRemove(id: string): void
  onRetry(id: string): void
  onFocusTextarea?(): void
}) {
  const chipRefs = useRef(new Map<string, HTMLDivElement>())
  // A keyboard removal doesn't take the chip out of `items` synchronously —
  // onRemove goes through the store/API, and the chip disappears only once
  // that round-trips back as a prop change. `pending` remembers where the
  // removed chip was so the effect below can move focus once it actually
  // does: the chip now at that index (what was "next"), else the one before
  // it ("previous"), else the composer's textarea (no chips left). It is
  // keyed by the removed chip's id: a removal that failed (the chip stays)
  // never moves focus when some other chip goes later.
  const pending = useRef<{ id: string; index: number } | null>(null)

  useEffect(() => {
    const p = pending.current
    if (!p || items.some((a) => a.id === p.id)) return // removal hasn't landed (or failed)
    pending.current = null
    if (items.length === 0) {
      onFocusTextarea?.()
      return
    }
    const target = items[Math.min(p.index, items.length - 1)]
    chipRefs.current.get(target.id)?.focus()
  }, [items, onFocusTextarea])

  if (items.length === 0) return null
  return (
    <div role="list" aria-label={t('attach.tray')} className="mb-2 flex flex-wrap gap-2">
      {items.map((a, i) => (
        <div role="listitem" key={a.id}>
          <Chip
            serverId={serverId}
            a={a}
            onRemove={() => onRemove(a.id)}
            onRetry={() => onRetry(a.id)}
            onKeyRemove={() => {
              pending.current = { id: a.id, index: i }
              onRemove(a.id)
            }}
            chipRef={(el) => {
              if (el) chipRefs.current.set(a.id, el)
              else chipRefs.current.delete(a.id)
            }}
          />
        </div>
      ))}
    </div>
  )
}
