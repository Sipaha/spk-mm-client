import type { ReactNode } from 'react'
import type { FileView } from '../api/types'
import { fileKey } from '../chat'
import { downloadErrorMessage } from '../errors'
import { t } from '../i18n'
import { useStore } from '../store'
import { DownloadButton } from './DownloadButton'
import { cardSizeLabel, cardType, cardTypeLabel, type CardType } from './files'
import { IconAudio, IconFile, IconFileArchive, IconFileSheet, IconFileSlide, IconImage, IconNote, IconOpenExternal, IconVideo, type IconProps } from './icons'

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

// CARD_ICON/CARD_ICON_COLOR: FileCard's type glyph and its Catppuccin
// tint (file-cards brief, 2026-09-30 — see the token comment in
// index.css). pdf/generic reuse the plain page glyph (IconFile);
// document/text reuse the "page with text lines" glyph (IconNote) already
// used elsewhere for text files — a document and a text file read the same
// visually, only the colour and the meta line's TYPE word differ.
const CARD_ICON: Record<CardType, (p: IconProps) => ReactNode> = {
  pdf: IconFile,
  image: IconImage,
  video: IconVideo,
  audio: IconAudio,
  text: IconNote,
  document: IconNote,
  spreadsheet: IconFileSheet,
  presentation: IconFileSlide,
  archive: IconFileArchive,
  generic: IconFile,
}
const CARD_ICON_COLOR: Record<CardType, string> = {
  pdf: 'text-file-pdf',
  image: 'text-file-image',
  video: 'text-file-video',
  audio: 'text-file-audio',
  text: 'text-file-text',
  document: 'text-file-document',
  spreadsheet: 'text-file-spreadsheet',
  presentation: 'text-file-presentation',
  archive: 'text-file-archive',
  generic: 'text-fg-muted',
}

// FileCard: a generic file's download/open card, official-client-sized
// (fixed 320x64, shrinking down to 204px wide before the name/meta truncate
// any further — file-cards brief, 2026-09-30, matching mm-10.11 webapp's
// own .post-image__column). `onView`, when given (a kind the Viewer knows
// how to open in place, currently pdf), makes the card's own name/icon area
// the preview action ("View <name>"); without it (every other kind, and
// the broken-PDF fallback the Viewer itself renders — pdf.spec.ts) that same
// area downloads instead, labelled with the bare file name so it never
// collides with the secondary Download button's own "Download <name>" (both
// call the same onDownload — e2e clicks the secondary one by that exact
// name, e.g. media.spec.ts's "Download spec.pdf", stick-bottom.spec.ts's
// "Download stick-report.zip"). A staged file (a pending post's, not sent
// yet) has no server id yet, so it gets no primary action and no secondary
// actions at all, same reasoning as before this brief.
//
// The secondary actions (download, open) sit at the card's right, revealed
// on hover/focus-within like the webapp's own dropdown menu — reserved
// space (not display:none), so nothing shifts and Playwright's opacity-
// blind actionability checks still find them without a real hover (every
// e2e download/open click already worked this way before this brief and
// keeps working). They stay forced-visible for the rest of the download's
// life — busy or saved — regardless of hover, so the ring/✓/"Show in
// folder" feedback a click just started is never hidden by the pointer
// moving away (AGENTS.md's download-feedback ruling).
export function FileCard({
  serverId,
  file,
  onDownload,
  onOpen,
  onView,
}: { serverId: number; file: FileView; onView?(file: FileView): void } & Pick<FileHandlers, 'onDownload' | 'onOpen'>) {
  const kind = cardType(file)
  const Glyph = CARD_ICON[kind]
  const colorClass = CARD_ICON_COLOR[kind]
  const typeLabel = cardTypeLabel(file)
  const sizeLabel = cardSizeLabel(file.size)
  const save = useStore((s) => s.fileSaves[fileKey(serverId, file.id)])
  const forceShowActions = save?.state === 'saving' || save?.state === 'saved'

  const body = (
    <>
      <Glyph size={30} className={`shrink-0 ${colorClass}`} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-semibold text-fg" title={file.name}>
          {file.name}
        </span>
        <span className="block truncate text-xs text-fg-muted">
          {typeLabel} {sizeLabel}
        </span>
      </span>
    </>
  )

  return (
    // w-80 min-w-[204px] max-w-full sit on THIS outer wrapper, not the
    // bordered "group" div one level in: a flex item with no width of its
    // own is sized, for intrinsic/shrink-to-fit purposes, by its
    // descendants' *specified* width — percentages like max-w-full can't
    // resolve during that pass (no definite containing block yet) and are
    // ignored, so the inner div's own max-w-full would have let its
    // intrinsic contribution stay 320px regardless, forcing the row (and
    // the thread panel around it) wider than it actually had room for
    // instead of wrapping (file-cards brief, 2026-09-30 — caught by the
    // narrow-thread-panel screenshot, not a unit test: jsdom has no real
    // layout engine to catch this). Putting the width/min/max trio on this
    // outer, already-definite-sized wrapper breaks that circularity — its
    // own containing block (Attachments.tsx's now-w-full "others" row) is
    // definite, so max-w-full here actually clamps.
    <div className="flex w-80 min-w-[204px] max-w-full shrink flex-col gap-1">
      <div className="group relative flex h-16 w-full items-center gap-2.5 rounded border border-line bg-panel px-3">
        {file.staged ? (
          <div className="flex min-w-0 flex-1 items-center gap-2.5">{body}</div>
        ) : (
          <button
            type="button"
            aria-label={onView ? t('file.view', { name: file.name }) : file.name}
            title={onView ? t('file.view', { name: file.name }) : file.name}
            onClick={() => (onView ? onView(file) : onDownload(file))}
            className="flex min-w-0 flex-1 items-center gap-2.5 rounded text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            {body}
          </button>
        )}
        {!file.staged && (
          <div
            className={`flex shrink-0 items-center gap-0.5 transition-opacity ${
              forceShowActions ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 group-focus-within:opacity-100'
            }`}
          >
            <DownloadButton serverId={serverId} file={file} onDownload={onDownload} />
            <IconButton label={t('file.open', { name: file.name })} onClick={() => onOpen(file)}>
              <IconOpenExternal />
            </IconButton>
          </div>
        )}
      </div>
      <StagedProgress file={file} />
    </div>
  )
}
