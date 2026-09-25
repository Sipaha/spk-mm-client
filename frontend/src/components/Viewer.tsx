import { useEffect, useRef, useState } from 'react'
import type { FileView } from '../api/types'
import { formatSize } from '../format'
import { t } from '../i18n'
import { mediaURL } from '../media'
import { FileCard } from './FileCard'
import { fileKind, imageSrc } from './files'
import { useTextFile } from './textFile'

interface Props {
  serverId: number
  files: FileView[] // the post's previewable files (images and texts)
  index: number
  onIndex(i: number): void
  onClose(): void
  onDownload(file: FileView): void
  onOpen(file: FileView): void
}

function FullText({ serverId, file }: { serverId: number; file: FileView }) {
  const res = useTextFile(mediaURL(serverId, 'text', file.id))
  return (
    <div className="flex h-full w-full max-w-5xl flex-col gap-1">
      {res.status === 'ok' && res.truncated && <p className="text-xs text-fg-muted">{t('file.truncated')}</p>}
      <pre className="min-h-0 flex-1 overflow-auto rounded bg-code-bg p-3 font-mono text-xs leading-5 text-fg">
        {res.status === 'ok' ? res.text : res.status === 'loading' ? t('file.loading') : t('err.no_file')}
      </pre>
    </div>
  )
}

// Viewer is a modal over the app: Escape closes, ←/→ go through the post's
// files (wrapping), focus starts on Close and returns to where it was.
export function Viewer({ serverId, files, index, onIndex, onClose, onDownload, onOpen }: Props) {
  const closeRef = useRef<HTMLButtonElement>(null)
  // The media endpoint can 404/413/415 an image that looked fine in the
  // feed (e.g. it changed on the server); track failures by file id so a
  // fallback survives ←/→ back to the same file without another decode.
  const [failedId, setFailedId] = useState<string | null>(null)
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
    if (e.target === e.currentTarget) onClose()
  }
  const nav = 'absolute top-1/2 -translate-y-1/2 rounded-full bg-black/50 px-3 py-1 text-2xl text-white hover:bg-black/70'
  const showImage = fileKind(file) === 'image' && src && failedId !== file.id
  return (
    <div role="dialog" aria-modal="true" aria-label={t('viewer.label')} className="fixed inset-0 z-50 flex flex-col bg-black/85 text-fg" onClick={closeOnBackdrop}>
      <header className="flex items-center gap-3 px-4 py-2 text-sm">
        <span className="min-w-0 truncate font-medium">{file.name}</span>
        <span className="shrink-0 text-xs text-fg-muted">{formatSize(file.size)}</span>
        {files.length > 1 && (
          <span className="shrink-0 text-xs text-fg-muted">{t('viewer.counter', { i: String(index + 1), n: String(files.length) })}</span>
        )}
        <span className="ml-auto flex shrink-0 gap-2">
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
      <div className="relative flex min-h-0 flex-1 items-center justify-center p-4" onClick={closeOnBackdrop}>
        {files.length > 1 && (
          <button type="button" aria-label={t('viewer.prev')} className={`${nav} left-3`} onClick={() => onIndex((index - 1 + files.length) % files.length)}>
            ‹
          </button>
        )}
        {fileKind(file) === 'image' ? (
          showImage ? (
            <img
              key={file.id}
              src={mediaURL(serverId, 'full', file.id, { src: src! })}
              alt={file.name}
              className="max-h-full max-w-full object-contain"
              onError={() => setFailedId(file.id)}
            />
          ) : (
            <FileCard key={file.id} file={file} onDownload={onDownload} onOpen={onOpen} />
          )
        ) : (
          <FullText key={file.id} serverId={serverId} file={file} />
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
