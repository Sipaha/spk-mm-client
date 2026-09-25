import type { ReactNode } from 'react'
import type { FileView } from '../api/types'
import { formatSize } from '../format'
import { t } from '../i18n'

export interface FileHandlers {
  onView(file: FileView): void
  onDownload(file: FileView): void
  onOpen(file: FileView): void
}

export function IconButton({ label, onClick, children }: { label: string; onClick(): void; children: ReactNode }) {
  return (
    <button type="button" aria-label={label} title={label} onClick={onClick} className="rounded px-1 text-fg-muted hover:bg-hover hover:text-fg">
      {children}
    </button>
  )
}

export function FileCard({ file, onDownload, onOpen }: { file: FileView } & Pick<FileHandlers, 'onDownload' | 'onOpen'>) {
  return (
    <div className="flex max-w-sm items-center gap-2 rounded border border-line px-2 py-1 text-xs">
      <span aria-hidden>📎</span>
      <span className="min-w-0 truncate" title={file.name}>
        {file.name}
      </span>
      <span className="shrink-0 text-fg-muted">{formatSize(file.size)}</span>
      <IconButton label={t('file.download', { name: file.name })} onClick={() => onDownload(file)}>
        ⬇
      </IconButton>
      <IconButton label={t('file.open', { name: file.name })} onClick={() => onOpen(file)}>
        ↗
      </IconButton>
    </div>
  )
}
