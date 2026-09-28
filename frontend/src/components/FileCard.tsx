import type { ReactNode } from 'react'
import type { FileView } from '../api/types'
import { downloadErrorMessage } from '../errors'
import { formatSize } from '../format'
import { t } from '../i18n'
import { IconDownload, IconExpand, IconFile, IconOpenExternal } from './icons'

export interface FileHandlers {
  onView(file: FileView): void
  onDownload(file: FileView): void
  onOpen(file: FileView): void
}

export function IconButton({
  label,
  onClick,
  children,
  className = '',
}: {
  label: string
  onClick(): void
  children: ReactNode
  className?: string
}) {
  return (
    <button type="button" aria-label={label} title={label} onClick={onClick} className={`flex items-center justify-center rounded px-1 text-fg-muted hover:bg-hover hover:text-fg ${className}`}>
      {children}
    </button>
  )
}

// StagedProgress: a sending post's staged file's live upload state (Task 5
// controller carry-over) — a thin progress bar while it uploads, or an
// error with no bar once it fails; nothing for an ordinary server file
// (not staged) or once uploaded (its post is about to be created).
export function StagedProgress({ file }: { file: FileView }) {
  if (!file.staged || !file.state || file.state === 'uploaded') return null
  if (file.state === 'failed') {
    return (
      <p role="alert" className="text-danger">
        {t('attach.error', { detail: downloadErrorMessage(file.error ?? '') })}
      </p>
    )
  }
  const pct = file.size > 0 ? Math.min(100, Math.round(((file.sent ?? 0) / file.size) * 100)) : 0
  return (
    <div className="h-1 w-full overflow-hidden rounded bg-line" aria-hidden>
      <div className="h-full bg-accent transition-[width]" style={{ width: `${pct}%` }} />
    </div>
  )
}

// FileCard: a generic file's download/open card. `onView`, when given (a
// kind the Viewer knows how to open in place, currently pdf), adds a
// Preview action — same icon/label convention as the image/text/markdown
// "view" actions elsewhere (IconExpand + t('file.view')). A staged file (a
// pending post's, not sent yet) has no server id yet, so it gets none of
// download/open/preview, same reasoning as the other staged tiles.
export function FileCard({
  file,
  onDownload,
  onOpen,
  onView,
}: { file: FileView; onView?(file: FileView): void } & Pick<FileHandlers, 'onDownload' | 'onOpen'>) {
  return (
    <div className="flex max-w-sm flex-col gap-1 rounded border border-line px-2 py-1 text-xs">
      <div className="flex items-center gap-2">
        <IconFile className="shrink-0 text-fg-muted" />
        <span className="min-w-0 truncate" title={file.name}>
          {file.name}
        </span>
        <span className="shrink-0 text-fg-muted">{formatSize(file.size)}</span>
        {!file.staged && (
          <>
            {onView && (
              <IconButton label={t('file.view', { name: file.name })} onClick={() => onView(file)}>
                <IconExpand />
              </IconButton>
            )}
            <IconButton label={t('file.download', { name: file.name })} onClick={() => onDownload(file)}>
              <IconDownload />
            </IconButton>
            <IconButton label={t('file.open', { name: file.name })} onClick={() => onOpen(file)}>
              <IconOpenExternal />
            </IconButton>
          </>
        )}
      </div>
      <StagedProgress file={file} />
    </div>
  )
}
