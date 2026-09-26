import { useEffect, useRef, useState } from 'react'
import type { FileView } from '../api/types'
import { formatSize } from '../format'
import { t } from '../i18n'
import { FileCard } from './FileCard'
import { fileKind, imageSrc } from './files'
import { ImageZoom, type ImageZoomHandle } from './ImageZoom'
import { TextView } from './TextView'

interface Props {
  serverId: number
  files: FileView[] // the post's previewable files (images and texts)
  index: number
  onIndex(i: number): void
  onClose(): void
  onDownload(file: FileView): void
  onOpen(file: FileView): void
}

// Viewer is a modal over the app: Escape closes, ←/→ go through the post's
// files (wrapping), focus starts on Close and returns to where it was.
export function Viewer({ serverId, files, index, onIndex, onClose, onDownload, onOpen }: Props) {
  const closeRef = useRef<HTMLButtonElement>(null)
  // The media endpoint can 404/413/415 an image that looked fine in the
  // feed (e.g. it changed on the server); track failures by file id (all of
  // them, for the life of the viewer) so a fallback survives ←/→ back to a
  // file already known to fail, and an earlier failure elsewhere in the
  // post isn't forgotten when a later one comes in.
  const [failedIds, setFailedIds] = useState<ReadonlySet<string>>(new Set())
  // The full image can be large; show a loading indicator until it decodes
  // instead of an empty dim backdrop. Also kept for the whole session so
  // going A → B → back to A doesn't replay the loading flash.
  const [loadedIds, setLoadedIds] = useState<ReadonlySet<string>>(new Set())
  const addFailed = (id: string) => setFailedIds((s) => (s.has(id) ? s : new Set(s).add(id)))
  const addLoaded = (id: string) => setLoadedIds((s) => (s.has(id) ? s : new Set(s).add(id)))
  // The image's zoom/pan is per-file (ImageZoom is keyed by file.id and
  // resets to "fit" on remount); only its scale indicator and the Fit
  // button live in the header, so they're lifted here.
  const imageZoomRef = useRef<ImageZoomHandle>(null)
  const [scalePercent, setScalePercent] = useState(100)
  // Set right when a real drag (not just a click) ends, so the backdrop's
  // own click handler — fired right after, since the drag can end with the
  // pointer over the backdrop — doesn't treat it as a backdrop click.
  const draggedRef = useRef(false)
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
      } else if (e.key === 'ArrowRight' && files.length > 1) {
        e.preventDefault()
        onIndex((index + 1) % files.length)
      } else if (e.key === 'ArrowLeft' && files.length > 1) {
        e.preventDefault()
        onIndex((index - 1 + files.length) % files.length)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [files.length, index, onClose, onIndex])
  const file = files[index]
  if (!file) return null
  const src = imageSrc(file)
  const closeOnBackdrop = (e: React.MouseEvent) => {
    if (draggedRef.current) {
      draggedRef.current = false
      return
    }
    if (e.target === e.currentTarget) onClose()
  }
  const nav = 'absolute top-1/2 -translate-y-1/2 rounded-full bg-black/50 px-3 py-1 text-2xl text-white hover:bg-black/70'
  const showImage = fileKind(file) === 'image' && src && !failedIds.has(file.id)
  return (
    <div role="dialog" aria-modal="true" aria-label={t('viewer.label')} className="fixed inset-0 z-50 flex flex-col bg-black/85 text-fg" onClick={closeOnBackdrop}>
      <header className="flex items-center gap-3 px-4 py-2 text-sm">
        <span className="min-w-0 truncate font-medium">{file.name}</span>
        <span className="shrink-0 text-xs text-fg-muted">{formatSize(file.size)}</span>
        {files.length > 1 && (
          <span className="shrink-0 text-xs text-fg-muted">{t('viewer.counter', { i: String(index + 1), n: String(files.length) })}</span>
        )}
        <span className="ml-auto flex shrink-0 items-center gap-2">
          {showImage && (
            <>
              <span className="text-xs text-fg-muted" data-testid="viewer-zoom-percent">
                {t('viewer.zoom', { p: String(scalePercent) })}
              </span>
              <button type="button" className="rounded px-2 py-0.5 hover:bg-hover" onClick={() => imageZoomRef.current?.fit()}>
                {t('viewer.fit')}
              </button>
            </>
          )}
          <button type="button" className="rounded px-2 py-0.5 hover:bg-hover" onClick={() => onDownload(file)}>
            {t('viewer.download')}
          </button>
          <button type="button" className="rounded px-2 py-0.5 hover:bg-hover" onClick={() => onOpen(file)}>
            {t('viewer.open')}
          </button>
          <button ref={closeRef} type="button" aria-label={t('viewer.close')} title={t('viewer.close')} className="rounded px-2 py-0.5 hover:bg-hover" onClick={onClose}>
            ✕
          </button>
        </span>
      </header>
      {/* overflow-hidden: a zoomed-in image must clip to this pane — without
          it, the transformed <img> paints over the header (it comes later
          in the DOM) and steals clicks from the Fit button/percent. */}
      <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden p-4" onClick={closeOnBackdrop}>
        {files.length > 1 && (
          <button type="button" aria-label={t('viewer.prev')} className={`${nav} left-3`} onClick={() => onIndex((index - 1 + files.length) % files.length)}>
            ‹
          </button>
        )}
        {fileKind(file) === 'image' ? (
          showImage ? (
            <ImageZoom
              key={file.id}
              ref={imageZoomRef}
              serverId={serverId}
              file={file}
              loaded={loadedIds.has(file.id)}
              onLoaded={() => addLoaded(file.id)}
              onFail={() => addFailed(file.id)}
              onDragEnd={() => {
                draggedRef.current = true
              }}
              onPercent={setScalePercent}
            />
          ) : (
            <FileCard key={file.id} file={file} onDownload={onDownload} onOpen={onOpen} />
          )
        ) : (
          <TextView key={file.id} serverId={serverId} file={file} />
        )}
        {files.length > 1 && (
          <button type="button" aria-label={t('viewer.next')} className={`${nav} right-3`} onClick={() => onIndex((index + 1) % files.length)}>
            ›
          </button>
        )}
      </div>
    </div>
  )
}
