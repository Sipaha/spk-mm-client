import { useEffect, useReducer } from 'react'
import type { FileView } from '../api/types'
import { fileKey, revealSavedFile } from '../chat'
import { t } from '../i18n'
import { useStore } from '../store'
import { IconCheck, IconDownload, IconFolder } from './icons'

// How long a finished download's ✓ replaces the download icon.
export const SAVED_CHECK_MS = 2000

// DownloadButton: a file's download action with its own feedback — no banner
// above the feed (it shifted the layout: user report 2026-09-29). While the
// file is being saved the icon is a ring (the downloads-list entry's
// progress, a spinner until there is one); once saved, a brief ✓ and, for
// the rest of the session, a "Show in folder" button next to it. A failure
// is a toast (chat.ts). The icon box keeps its size in every state and the
// reveal slot is reserved (hidden) before the save, so the card keeps its
// width and height — and the feed row around it never changes. `text` is the
// viewer header's variant: text buttons, the status icon before the label.
export function DownloadButton({ serverId, file, onDownload, text = false }: { serverId: number; file: FileView; onDownload(file: FileView): void; text?: boolean }) {
  const save = useStore((s) => s.fileSaves[fileKey(serverId, file.id)])
  const entry = useStore((s) => (save?.state === 'saving' ? s.downloads.find((d) => d.server_id === serverId && d.file_id === file.id && d.state === 'downloading') : undefined))
  const [, rerender] = useReducer((x: number) => x + 1, 0)
  const checkLeft = save?.state === 'saved' ? save.savedAt + SAVED_CHECK_MS - Date.now() : 0
  useEffect(() => {
    if (checkLeft <= 0) return
    const timer = setTimeout(rerender, checkLeft)
    return () => clearTimeout(timer)
  }, [checkLeft])

  const busy = save?.state === 'saving'
  const saved = save?.state === 'saved'
  const pct = entry && entry.size > 0 ? Math.min(100, Math.round((entry.received / entry.size) * 100)) : null
  const size = text ? 14 : 18
  const icon = busy ? <Ring pct={pct} size={size} /> : checkLeft > 0 ? <IconCheck size={size} className="text-accent" /> : <IconDownload size={size} />
  const state = busy ? (pct === null ? 'spinner' : 'progress') : checkLeft > 0 ? 'saved' : 'idle'
  const title = busy ? t('file.downloading', { name: file.name }) : checkLeft > 0 ? t('file.saved', { path: save.path }) : undefined
  const path = saved || (busy && save.path) ? save.path : ''

  if (text) {
    return (
      <>
        <button type="button" aria-busy={busy || undefined} title={title} className="flex items-center gap-1 rounded px-2 py-0.5 hover:bg-hover" onClick={() => onDownload(file)}>
          {(busy || checkLeft > 0) && (
            <span data-state={state} data-pct={pct ?? undefined} className="flex">
              {icon}
            </span>
          )}
          {t('viewer.download')}
        </button>
        {path && (
          <button type="button" className="rounded px-2 py-0.5 hover:bg-hover" onClick={() => void revealSavedFile(path)}>
            {t('downloads.showInFolder')}
          </button>
        )}
      </>
    )
  }
  const label = t('file.download', { name: file.name })
  return (
    <>
      <button
        type="button"
        aria-label={label}
        aria-busy={busy || undefined}
        title={title ?? label}
        onClick={() => onDownload(file)}
        className="flex items-center justify-center rounded px-1 text-fg-muted hover:bg-hover hover:text-fg"
      >
        <span data-state={state} data-pct={pct ?? undefined} className="flex">
          {icon}
        </span>
      </button>
      {/* Reserved in every state, hidden and inert until saved: the card
          never gets wider when it appears (review I1 — in a post with
          several cards a wider card could wrap onto a new line). */}
      <button
        type="button"
        data-slot="reveal"
        aria-label={path ? t('downloads.reveal', { name: file.name }) : undefined}
        aria-hidden={path ? undefined : true}
        inert={!path}
        tabIndex={path ? undefined : -1}
        title={path ? t('downloads.showInFolder') : undefined}
        onClick={() => path && void revealSavedFile(path)}
        className={`flex items-center justify-center rounded px-1 text-fg-muted hover:bg-hover hover:text-fg${path ? '' : ' invisible'}`}
      >
        <IconFolder size={size} />
      </button>
    </>
  )
}

// Ring: download progress in the icon's own box — an arc growing with pct,
// or a spinning quarter arc while the size or the entry is not known yet.
function Ring({ pct, size }: { pct: number | null; size: number }) {
  const r = 9
  const c = 2 * Math.PI * r
  const dash = pct === null ? c / 4 : (c * pct) / 100
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false" className={pct === null ? 'motion-safe:animate-spin' : undefined}>
      <circle cx="12" cy="12" r={r} fill="none" stroke="currentColor" strokeOpacity="0.25" strokeWidth="3" />
      <circle cx="12" cy="12" r={r} fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeDasharray={`${dash} ${c}`} transform="rotate(-90 12 12)" className="text-accent" />
    </svg>
  )
}
