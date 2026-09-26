import { forwardRef, useEffect, useImperativeHandle, useLayoutEffect, useRef, useState } from 'react'
import type { FileView } from '../api/types'
import { t } from '../i18n'
import { mediaURL } from '../media'
import { imageOriginalOk } from './files'
import { FIT, MAX_ZOOM_OF_NATURAL, ZOOM_STEP, naturalScale, panBy, scalePercent, toggleFitAndNatural, zoomAround, type ZoomState } from './imageZoom'

export interface ImageZoomHandle {
  fit(): void
}

interface Props {
  serverId: number
  file: FileView
  // Whether this file's image was already shown once during this viewer
  // session — Viewer.tsx keeps this across ←/→ so re-visiting a file
  // doesn't replay the loading flash.
  loaded: boolean
  onLoaded(): void
  // Nothing displayable at all (no working preview and no working
  // original) — the caller falls back to a FileCard.
  onFail(): void
  // A real drag (not just a click) just ended — the caller must not treat
  // the resulting native `click` as a backdrop click.
  onDragEnd(): void
  onPercent(percent: number): void
}

const DRAG_THRESHOLD = 3
const GESTURE_HOLD_MS = 300

function centerOf(rect: DOMRect): { x: number; y: number } {
  return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
}

function isTypingTarget(el: Element | null): boolean {
  return el instanceof HTMLElement && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)
}

// ImageZoom shows one previewable image with wheel-zoom (anchored at the
// cursor), drag-to-pan, double-click fit/100% toggle and +/-/0 keys. It
// also owns the "original loads immediately, preview is a placeholder"
// swap: both <img>s are mounted from the start (so both can load in the
// background), but only one is ever visible/accessible at a time.
export const ImageZoom = forwardRef<ImageZoomHandle, Props>(function ImageZoom(
  { serverId, file, loaded, onLoaded, onFail, onDragEnd, onPercent },
  ref,
) {
  const previewAvailable = !!file.has_preview
  const originalOk = imageOriginalOk(file)
  const previewUrl = previewAvailable ? mediaURL(serverId, 'full', file.id, { src: 'preview' }) : null
  const originalUrl = originalOk ? mediaURL(serverId, 'full', file.id, { src: 'file' }) : null

  const [zoom, setZoom] = useState<ZoomState>(FIT)
  const [dragging, setDragging] = useState(false)
  const [gesturing, setGesturing] = useState(false)
  const [previewReady, setPreviewReady] = useState(false)
  const [previewBroken, setPreviewBroken] = useState(false)
  const [originalReady, setOriginalReady] = useState(false)
  const [originalBroken, setOriginalBroken] = useState(false)
  const [natural, setNatural] = useState({ w: file.width ?? 0, h: file.height ?? 0 })

  const containerRef = useRef<HTMLDivElement | null>(null)
  const previewRef = useRef<HTMLImageElement | null>(null)
  const originalRef = useRef<HTMLImageElement | null>(null)
  const dragRef = useRef<{ x: number; y: number; tx: number; ty: number; scale: number; moved: boolean } | null>(null)
  const gestureTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useImperativeHandle(ref, () => ({ fit: () => setZoom(FIT) }), [])

  // Which slot is visible/accessible: the original once it has loaded, or
  // right away if there is no preview to show meanwhile (matches the old,
  // single-<img> behaviour — a loading overlay covers it until it decodes)
  // or if the preview has died (nothing else to show while waiting).
  // Once the original loads it takes over; the preview stays mounted
  // (still loaded, no flash) but hidden.
  const originalUsable = originalOk && !originalBroken
  const previewUsable = previewAvailable && !previewBroken
  const showOriginal = originalUsable && (originalReady || !previewUsable)
  const showPreview = !showOriginal && previewUsable
  const shown = (showOriginal && originalReady) || (showPreview && previewReady)

  useEffect(() => {
    if (shown) onLoaded()
  }, [shown])

  useEffect(() => {
    const originalDead = !originalOk || originalBroken
    const previewDead = !previewAvailable || previewBroken
    if (originalDead && previewDead) onFail()
  }, [originalBroken, previewBroken])

  const activeEl = () => (showOriginal ? originalRef.current : previewRef.current)

  // fitWidth: the box the active image renders at when scale is 1 ("fit"),
  // derived from its current (possibly scaled) box — transforms never
  // change layout, so this holds at any zoom level.
  const fitWidthOf = (el: HTMLImageElement | null): number => {
    if (!el) return 0
    const rect = el.getBoundingClientRect()
    return zoom.scale > 0 ? rect.width / zoom.scale : rect.width
  }

  const maxScale = (() => {
    const fitW = fitWidthOf(activeEl())
    const nat = naturalScale(natural.w || fitW, fitW || 1)
    return MAX_ZOOM_OF_NATURAL * nat
  })()

  useLayoutEffect(() => {
    const fitW = fitWidthOf(activeEl())
    const nat = naturalScale(natural.w || fitW, fitW || 1)
    onPercent(scalePercent(zoom.scale, nat))
  }, [zoom.scale, natural.w, natural.h, showOriginal])

  const startGesture = () => {
    setGesturing(true)
    clearTimeout(gestureTimer.current)
    gestureTimer.current = setTimeout(() => setGesturing(false), GESTURE_HOLD_MS)
  }
  useEffect(() => () => clearTimeout(gestureTimer.current), [])

  // React makes its synthetic onWheel passive at the root (a deliberate
  // scroll-perf default since React 17), so preventDefault() inside a JSX
  // onWheel handler is silently a no-op in real browsers — it would not
  // stop the page from scrolling/the browser from pinch-zooming under the
  // cursor. A plain addEventListener with { passive: false } is required.
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const handler = (e: WheelEvent) => {
      e.preventDefault()
      const active = showOriginal ? originalRef.current : previewRef.current
      if (!active) return
      const cursor = ((c) => ({ x: e.clientX - c.x, y: e.clientY - c.y }))(centerOf(active.getBoundingClientRect()))
      const factor = e.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP
      startGesture()
      setZoom((z) => zoomAround(z, cursor, factor, maxScale))
    }
    el.addEventListener('wheel', handler, { passive: false })
    return () => el.removeEventListener('wheel', handler)
  }, [maxScale, showOriginal])

  const onDoubleClick = (e: React.MouseEvent<HTMLDivElement>) => {
    const el = activeEl()
    if (!el) return
    const rect = el.getBoundingClientRect()
    const cursor = ((c) => ({ x: e.clientX - c.x, y: e.clientY - c.y }))(centerOf(rect))
    const fitW = zoom.scale > 0 ? rect.width / zoom.scale : rect.width
    const nat = naturalScale(natural.w || fitW, fitW || 1)
    startGesture()
    setZoom((z) => toggleFitAndNatural(z, nat, cursor, MAX_ZOOM_OF_NATURAL * nat))
  }

  const onMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
    if (e.button !== 0 || zoom.scale <= 1) return // nothing to pan at "fit"
    e.preventDefault()
    const startScale = zoom.scale
    dragRef.current = { x: e.clientX, y: e.clientY, tx: zoom.tx, ty: zoom.ty, scale: startScale, moved: false }
    setDragging(true)
    startGesture()
    const onMove = (ev: MouseEvent) => {
      const d = dragRef.current
      if (!d) return
      const dx = ev.clientX - d.x
      const dy = ev.clientY - d.y
      if (Math.abs(dx) > DRAG_THRESHOLD || Math.abs(dy) > DRAG_THRESHOLD) d.moved = true
      setZoom(panBy({ scale: d.scale, tx: d.tx, ty: d.ty }, dx, dy))
    }
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      setDragging(false)
      if (dragRef.current?.moved) onDragEnd()
      dragRef.current = null
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(document.activeElement)) return
      if (e.key === '0') {
        e.preventDefault()
        setZoom(FIT)
      } else if (e.key === '+' || e.key === '=') {
        e.preventDefault()
        setZoom((z) => zoomAround(z, { x: 0, y: 0 }, ZOOM_STEP, maxScale))
      } else if (e.key === '-') {
        e.preventDefault()
        setZoom((z) => zoomAround(z, { x: 0, y: 0 }, 1 / ZOOM_STEP, maxScale))
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [maxScale])

  const transformStyle = {
    transform: `translate(${zoom.tx}px, ${zoom.ty}px) scale(${zoom.scale})`,
    willChange: gesturing ? ('transform' as const) : undefined,
    cursor: zoom.scale > 1 ? (dragging ? 'grabbing' : 'grab') : undefined,
  }
  // aspect-ratio (from the file's metadata, known up front): reserves the
  // correct "fit" box before the image itself decodes — without it, an
  // unloaded <img> with no width/height lays out at 0×0, so the very first
  // percent/max-zoom reading (before anything has loaded) would be off.
  const aspectStyle = file.width && file.height ? { aspectRatio: `${file.width} / ${file.height}` } : undefined
  const styleFor = (visible: boolean) => (visible ? { ...aspectStyle, ...transformStyle } : aspectStyle)

  return (
    <div
      ref={containerRef}
      className="relative flex h-full w-full items-center justify-center"
      onMouseDown={onMouseDown}
      onDoubleClick={onDoubleClick}
    >
      {previewAvailable && (
        <img
          ref={previewRef}
          src={previewUrl!}
          alt={showPreview ? file.name : ''}
          draggable={false}
          onLoad={(e) => {
            setPreviewReady(true)
            if (!originalReady) setNatural({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })
          }}
          onError={() => setPreviewBroken(true)}
          className={`max-h-full max-w-full object-contain ${showPreview ? '' : 'hidden'}`}
          style={styleFor(showPreview)}
        />
      )}
      {originalOk && (
        <img
          ref={originalRef}
          src={originalUrl!}
          alt={showOriginal ? file.name : ''}
          draggable={false}
          onLoad={(e) => {
            setOriginalReady(true)
            setNatural({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })
          }}
          onError={() => setOriginalBroken(true)}
          className={`max-h-full max-w-full object-contain ${showOriginal ? '' : 'hidden'}`}
          style={styleFor(showOriginal)}
        />
      )}
      {!shown && !loaded && (
        // pointer-events-none: sits over the backdrop while nothing has
        // loaded yet, but a click on it must still close the viewer like a
        // click on the bare backdrop does (it falls through).
        <p className="pointer-events-none absolute inset-0 flex items-center justify-center text-sm text-fg-muted">{t('file.loading')}</p>
      )}
    </div>
  )
})
