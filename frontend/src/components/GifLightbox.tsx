import { useEffect, useRef } from 'react'
import type { GifPreviewView } from '../api/types'
import { openExternalLink } from '../chat'
import { t } from '../i18n'
import { mediaURL } from '../media'
import { IconClose } from './icons'

interface Props {
  serverId: number
  postId: string
  gif: GifPreviewView
  onClose(): void
}

// GifLightbox is a lightweight modal dialog for external/markdown GIF previews
// served via /media/<srv>/postgif/<postId>?k=<id>. Kept strictly separate from
// Viewer / FileView (which is for uploaded Mattermost files only).
export function GifLightbox({ serverId, postId, gif, onClose }: Props) {
  const closeRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
    closeRef.current?.focus()
    return () => opener?.focus()
  }, [])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const src = mediaURL(serverId, 'postgif', postId, { k: gif.id })
  const title = gif.alt || gif.url
  const onBackdrop = (e: React.MouseEvent) => {
    if (e.target === e.currentTarget) onClose()
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={t('gif.viewerLabel')}
      data-overlay="true"
      data-testid="gif-lightbox"
      className="fixed inset-0 z-50 flex flex-col bg-black/85 text-fg"
      onClick={onBackdrop}
    >
      <header className="flex items-center gap-3 px-4 py-2 text-sm">
        <span className="min-w-0 flex-1 truncate font-medium" title={gif.url}>
          {title}
        </span>
        <button
          type="button"
          className="shrink-0 rounded px-2 py-0.5 hover:bg-hover"
          onClick={() => openExternalLink(gif.url)}
        >
          {t('thread.openInBrowser')}
        </button>
        <button
          ref={closeRef}
          type="button"
          aria-label={t('viewer.close')}
          title={t('viewer.close')}
          className="flex shrink-0 items-center justify-center rounded px-2 py-0.5 hover:bg-hover"
          onClick={onClose}
        >
          <IconClose />
        </button>
      </header>
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-hidden p-4" onClick={onBackdrop}>
        <img
          src={src}
          alt={gif.alt || ''}
          draggable={false}
          className="max-h-full max-w-full rounded object-contain"
        />
      </div>
    </div>
  )
}
