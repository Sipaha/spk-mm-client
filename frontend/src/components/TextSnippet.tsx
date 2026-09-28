import { useState } from 'react'
import type { FileView } from '../api/types'
import { formatSize } from '../format'
import { t } from '../i18n'
import { mediaURL } from '../media'
import { useLiveEpoch } from '../store'
import { FileCard, IconButton, type FileHandlers } from './FileCard'
import { IconDownload, IconExpand, IconNote, IconOpenExternal } from './icons'
import { useTextFile } from './textFile'

// The collapsed snippet is 8 lines high whether loaded or not: the feed
// row never changes size by itself (only when the user expands it).
export function TextSnippet({ serverId, file, onView, onDownload, onOpen }: { serverId: number; file: FileView } & FileHandlers) {
  const res = useTextFile(mediaURL(serverId, 'text', file.id), useLiveEpoch(serverId))
  const [open, setOpen] = useState(false)
  if (res.status === 'error') return <FileCard file={file} onDownload={onDownload} onOpen={onOpen} />
  return (
    <figure className="w-full max-w-3xl overflow-hidden rounded border border-line bg-code-bg text-xs">
      <figcaption className="flex items-center gap-2 border-b border-line px-2 py-1">
        <IconNote className="shrink-0 text-fg-muted" />
        <span className="min-w-0 truncate font-medium" title={file.name}>
          {file.name}
        </span>
        <span className="shrink-0 text-fg-muted">{formatSize(file.size)}</span>
        <span className="ml-auto flex shrink-0 items-center gap-1">
          <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="rounded px-1.5 text-fg-muted hover:bg-hover hover:text-fg">
            {t(open ? 'file.collapse' : 'file.expand')}
          </button>
          <IconButton label={t('file.view', { name: file.name })} onClick={() => onView(file)}>
            <IconExpand />
          </IconButton>
          <IconButton label={t('file.download', { name: file.name })} onClick={() => onDownload(file)}>
            <IconDownload />
          </IconButton>
          <IconButton label={t('file.open', { name: file.name })} onClick={() => onOpen(file)}>
            <IconOpenExternal />
          </IconButton>
        </span>
      </figcaption>
      <pre className={`overflow-x-auto px-2 py-1 font-mono leading-5 text-fg ${open ? 'max-h-96 overflow-y-auto' : 'h-40 overflow-y-hidden'}`}>
        {res.status === 'loading' ? <span className="text-fg-muted">{t('file.loading')}</span> : res.text}
      </pre>
    </figure>
  )
}
