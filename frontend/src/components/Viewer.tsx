import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import type { FileView } from '../api/types'
import { formatSize } from '../format'
import { t } from '../i18n'
import { DownloadButton } from './DownloadButton'
import { FileCard } from './FileCard'
import { TOAST_HOST, useToastHost } from './Toast'
import { fileKind, imageSrc } from './files'
import { ImageZoom, type ImageZoomHandle } from './ImageZoom'
import { IconChevronLeft, IconChevronRight, IconClose } from './icons'
import { MarkdownView } from './MarkdownView'
import { MediaPlayer } from './MediaPlayer'
import { TextView } from './TextView'

// pdf.js (PdfView) is loaded only once a PDF is actually opened — ruling
// (plan.md): "nothing may block startup", checked by
// scripts/check-pdf-bundle.mjs against the built initial chunk.
const PdfView = lazy(() => import('./PdfView'))

// A press that moved further than this is a drag (a text selection), not a
// click on the empty area.
const CLICK_SLOP = 4
// Width of the zone along a scrollable element's right/bottom edge treated
// as its scrollbar: GTK's overlay scrollbars take no layout space, so
// clientWidth alone would not exclude them.
const SCROLLBAR_ZONE = 16

// onScrollbar: is (x, y) over el's scrollbar? A press on a scrollbar (or a
// scrollbar drag) of the PDF scroller — itself an "empty area" — must not
// close the viewer.
function onScrollbar(el: EventTarget | null, x: number, y: number): boolean {
  if (!(el instanceof HTMLElement)) return false
  const vbar = el.scrollHeight > el.clientHeight
  const hbar = el.scrollWidth > el.clientWidth
  if (!vbar && !hbar) return false
  const r = el.getBoundingClientRect()
  const dx = x - r.left
  const dy = y - r.top
  return (vbar && dx >= Math.min(el.clientLeft + el.clientWidth, r.width - SCROLLBAR_ZONE)) || (hbar && dy >= Math.min(el.clientTop + el.clientHeight, r.height - SCROLLBAR_ZONE))
}

interface Props {
  serverId: number
  files: FileView[] // the post's previewable files (images and texts)
  index: number
  me: string
  onLink(href: string): void
  onIndex(i: number): void
  onClose(): void
  onDownload(file: FileView): void
  onOpen(file: FileView): void
}

// Viewer is a modal over the app: Escape closes, ←/→ go through the post's
// files (wrapping), focus starts on Close and returns to where it was.
export function Viewer({ serverId, files, index, me, onLink, onIndex, onClose, onDownload, onOpen }: Props) {
  const closeRef = useRef<HTMLButtonElement>(null)
  // The viewer is a fixed z-50 modal: while open it hosts the toast
  // (Toast.tsx), or a failed Download here would sit unseen under it.
  const dialogRef = useRef<HTMLDivElement>(null)
  useToastHost(dialogRef, TOAST_HOST.viewer, 'viewer')
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
  // Where the last press started (pointerdown, captured on the dialog): a
  // click only closes the viewer when the press started on the same empty
  // area it ended on, did not move (a text selection dragged from a PDF
  // page — or from the empty area into a page — ends in a `click` on their
  // common ancestor, the empty scroller) and was not on a scrollbar.
  const downRef = useRef<{ target: EventTarget | null; x: number; y: number; onBar: boolean } | null>(null)
  const [searchHost, setSearchHost] = useState<HTMLSpanElement | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  // Markdown files default to the rendered view; the header's Source/
  // Rendered switch flips this. Per-file, so paging ←/→ to a different
  // file always starts on the rendered view again.
  const [mdSource, setMdSource] = useState(false)
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
  }, [files, index, onClose, onIndex])
  useEffect(() => {
    setMdSource(false)
    setSearchQuery('')
  }, [files[index]?.id])
  const file = files[index]
  if (!file) return null
  const src = imageSrc(file)
  const closeOnBackdrop = (e: React.MouseEvent) => {
    if (draggedRef.current) {
      draggedRef.current = false
      return
    }
    const target = e.target as HTMLElement
    const down = downRef.current
    if (down && (down.target !== target || down.onBar || Math.hypot(e.clientX - down.x, e.clientY - down.y) > CLICK_SLOP)) return
    if (onScrollbar(target, e.clientX, e.clientY)) return
    // A literal hit on this handler's own element (the classic "backdrop"
    // case), or a hit on ImageZoom's root div: that div fills the whole
    // pane (so the image can be centered/panned within it) and sits in
    // front of the pane div wherever the image itself doesn't cover —
    // `data-viewer-empty` marks it so a click there still counts as
    // clicking the empty area, not the pane div underneath (833c3e6 made
    // that space unreachable by `target === currentTarget` alone). A click
    // on the <img> itself (or any other control) has that element as its
    // target, never this one, so it's unaffected. PdfView marks its root and
    // its scroller the same way (the dark area around the pages; a page box
    // is not marked, so clicks on a page — text selection — never close).
    // Other viewer kinds (video/audio, markdown/text, file cards) don't need
    // this: video/audio has no such wrapper, and markdown/text intentionally
    // fill the pane with real content that must not close on click.
    const isEmptyArea = target === e.currentTarget || target.hasAttribute('data-viewer-empty')
    if (isEmptyArea) {
      // Stop this bubbling further: the dialog's own onClick also uses
      // closeOnBackdrop, and without this a click matched here by the
      // `data-viewer-empty` branch (target !== that handler's own
      // currentTarget) would otherwise satisfy it too and call onClose a
      // second time.
      e.stopPropagation()
      onClose()
    }
  }
  // py-2 (+ the 24px chevron) makes a ~40px hit area — the WCAG/Fitts-law
  // touch-target minimum (final-review RULING #3, UI pass 2026-09-28: this
  // was ~32px, a Task 1 review's already-flagged-but-deferred finding).
  const nav = 'absolute top-1/2 -translate-y-1/2 flex items-center justify-center rounded-full bg-black/50 px-3 py-2 text-white hover:bg-black/70'
  const kind = fileKind(file)
  const showImage = kind === 'image' && src && !failedIds.has(file.id)
  const isMarkdown = kind === 'markdown'
  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-label={t('viewer.label')}
      data-overlay="true"
      className="fixed inset-0 z-50 flex flex-col bg-black/85 text-fg"
      onPointerDownCapture={(e) => {
        downRef.current = { target: e.target, x: e.clientX, y: e.clientY, onBar: onScrollbar(e.target, e.clientX, e.clientY) }
      }}
      onClick={closeOnBackdrop}
    >
      <header className="flex items-center gap-3 px-4 py-2 text-sm">
        <span className="min-w-0 truncate font-medium">{file.name}</span>
        <span className="shrink-0 text-xs text-fg-muted">{formatSize(file.size)}</span>
        {files.length > 1 && (
          <span className="shrink-0 text-xs text-fg-muted">{t('viewer.counter', { i: String(index + 1), n: String(files.length) })}</span>
        )}
        {/* Deliberate empty hit area: it closes the viewer like the dark
            backdrop, while mx-2 leaves a miss-safe gutter around every
            visible control. */}
        <span aria-hidden="true" data-viewer-empty data-viewer-header-empty className="mx-2 min-w-4 self-stretch flex-1" />
        <span className="flex shrink-0 items-center gap-2">
          {(kind === 'text' || isMarkdown) && <span ref={setSearchHost} className="flex items-center" />}
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
          {isMarkdown && (
            <span role="group" aria-label={t('viewer.md.group')} className="flex shrink-0 items-center gap-1 rounded border border-line p-0.5">
              <button
                type="button"
                aria-pressed={!mdSource}
                className={`rounded px-2 py-0.5 ${!mdSource ? 'bg-hover' : 'hover:bg-hover'}`}
                onClick={() => setMdSource(false)}
              >
                {t('viewer.md.rendered')}
              </button>
              <button
                type="button"
                aria-pressed={mdSource}
                className={`rounded px-2 py-0.5 ${mdSource ? 'bg-hover' : 'hover:bg-hover'}`}
                onClick={() => setMdSource(true)}
              >
                {t('viewer.md.source')}
              </button>
            </span>
          )}
          <DownloadButton serverId={serverId} file={file} onDownload={onDownload} text />
          <button type="button" className="rounded px-2 py-0.5 hover:bg-hover" onClick={() => onOpen(file)}>
            {t('viewer.open')}
          </button>
          <button ref={closeRef} type="button" aria-label={t('viewer.close')} title={t('viewer.close')} className="flex items-center justify-center rounded px-2 py-0.5 hover:bg-hover" onClick={onClose}>
            <IconClose />
          </button>
        </span>
      </header>
      {/* overflow-hidden: a zoomed-in image must clip to this pane — without
          it, the transformed <img> paints over the header (it comes later
          in the DOM) and steals clicks from the Fit button/percent. */}
      <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden px-4 pb-4 pt-1" onClick={closeOnBackdrop}>
        {files.length > 1 && (
          <button type="button" aria-label={t('viewer.prev')} className={`${nav} left-3`} onClick={() => onIndex((index - 1 + files.length) % files.length)}>
            <IconChevronLeft size={24} />
          </button>
        )}
        {kind === 'image' ? (
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
                // Normally the very next `click` (the browser's own,
                // synthesized right after this mouseup) reaches
                // `closeOnBackdrop` and resets the flag itself. But if the
                // drag ends outside the window (mouseup delivered to
                // `window` while the pointer is no longer over any element
                // in the document — e.g. released past the OS window's
                // edge), no `click` event fires at all, and the flag would
                // otherwise stay set until some later, unrelated backdrop
                // click gets wrongly swallowed by it. This macrotask runs
                // after any same-tick click already handled it, so it only
                // ever clears a flag no real click consumed.
                setTimeout(() => {
                  draggedRef.current = false
                }, 0)
              }}
              onPercent={setScalePercent}
            />
          ) : (
            <FileCard key={file.id} serverId={serverId} file={file} onDownload={onDownload} onOpen={onOpen} />
          )
        ) : kind === 'video' || kind === 'audio' ? (
          <MediaPlayer key={file.id} serverId={serverId} file={file} kind={kind} big onDownload={onDownload} onOpen={onOpen} />
        ) : kind === 'pdf' ? (
          failedIds.has(file.id) ? (
            <FileCard key={file.id} serverId={serverId} file={file} onDownload={onDownload} onOpen={onOpen} />
          ) : (
            // Never an <iframe>/<embed>/<object> (plan.md ruling): WebKitGTK
            // renders application/pdf with its own bundled pdf.js 4.1.392,
            // which has eval and PDF scripting on (CVE-2024-4367 class) and
            // cannot be configured off. This is pdf.js run by us, in a
            // Suspense'd lazy chunk, drawing into canvases only.
            <Suspense key={file.id} fallback={<span className="text-fg-muted">{t('file.loading')}</span>}>
              <PdfView serverId={serverId} file={file} onFail={() => addFailed(file.id)} />
            </Suspense>
          )
        ) : isMarkdown && !mdSource ? (
          <MarkdownView key={file.id} serverId={serverId} file={file} me={me} onLink={onLink} searchHost={searchHost} query={searchQuery} onQueryChange={setSearchQuery} />
        ) : (
          <TextView key={file.id} serverId={serverId} file={file} searchHost={searchHost} searchInHeader value={searchQuery} onValueChange={setSearchQuery} />
        )}
        {files.length > 1 && (
          <button type="button" aria-label={t('viewer.next')} className={`${nav} right-3`} onClick={() => onIndex((index + 1) % files.length)}>
            <IconChevronRight size={24} />
          </button>
        )}
      </div>
    </div>
  )
}
