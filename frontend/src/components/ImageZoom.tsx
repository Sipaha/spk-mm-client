import { forwardRef, useEffect, useImperativeHandle, useLayoutEffect, useRef, useState } from 'react'
import type { FileView } from '../api/types'
import { t } from '../i18n'
import { isShortcut } from '../keyboard'
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

// fitWidthOf: the box `el` renders at when scale is 1 ("fit"), derived from
// its current (possibly scaled) box — transforms never change layout, so
// this holds at any zoom level. Takes `scale` explicitly (rather than
// reading component state) so it can be called from event handlers that
// only read the latest state via a ref (see `latestRef` below).
function fitWidthOf(el: HTMLImageElement | null, scale: number): number {
  if (!el) return 0
  const rect = el.getBoundingClientRect()
  return scale > 0 ? rect.width / scale : rect.width
}

// natAndMax: the single place that turns (active element, current scale,
// natural pixel width) into the "100 %" factor and the 8×-natural cap —
// used by the percent indicator, wheel, double-click and keyboard zoom so
// the formula only exists once.
function natAndMax(el: HTMLImageElement | null, scale: number, naturalW: number): { nat: number; maxScale: number } {
  const fitW = fitWidthOf(el, scale)
  const nat = naturalScale(naturalW || fitW, fitW || 1)
  return { nat, maxScale: MAX_ZOOM_OF_NATURAL * nat }
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
  // GIFs keep their pre-Task-3 behaviour: only the (animated) original is
  // ever requested, never a static preview — has_preview on a GIF (if the
  // server ever sets it) describes a feed thumbnail, not something this
  // viewer should show in place of the animation.
  const isGif = (file.mime || '').toLowerCase() === 'image/gif'
  const previewAvailable = !!file.has_preview && !isGif
  const originalOk = imageOriginalOk(file)
  // The file's own width/height metadata describes the *original* — safe
  // to trust up front, and must not be overwritten by the preview's own
  // (usually smaller) decoded size once it loads (fix round 1 #2).
  const hasFileDims = !!(file.width && file.height)
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
  const dragHandlersRef = useRef<{ onMove: (e: MouseEvent) => void; onUp: (e: MouseEvent) => void } | null>(null)
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

  // Wheel and keyboard zoom subscribe once (mount) rather than resubscribe
  // whenever scale/natural/showOriginal change (fix round 1 #5 — that
  // churned add/removeEventListener on every tick). Their handlers read the
  // latest values through this ref instead of closing over render state.
  const latestRef = useRef({ zoom, natural, showOriginal })
  latestRef.current = { zoom, natural, showOriginal }

  useEffect(() => {
    if (shown) onLoaded()
  }, [shown])

  useEffect(() => {
    const originalDead = !originalOk || originalBroken
    const previewDead = !previewAvailable || previewBroken
    if (originalDead && previewDead) onFail()
  }, [originalBroken, previewBroken])

  const activeEl = () => (showOriginal ? originalRef.current : previewRef.current)

  useLayoutEffect(() => {
    const { nat } = natAndMax(activeEl(), zoom.scale, natural.w)
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
      const { natural: n0, showOriginal: so } = latestRef.current
      const active = so ? originalRef.current : previewRef.current
      if (!active) return
      const cursor = ((c) => ({ x: e.clientX - c.x, y: e.clientY - c.y }))(centerOf(active.getBoundingClientRect()))
      const factor = e.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP
      startGesture()
      setZoom((z) => zoomAround(z, cursor, factor, natAndMax(active, z.scale, n0.w).maxScale))
    }
    el.addEventListener('wheel', handler, { passive: false })
    return () => el.removeEventListener('wheel', handler)
    // Mount-once: `handler` reads fresh state via `latestRef` instead.
  }, [])

  const onDoubleClick = (e: React.MouseEvent<HTMLDivElement>) => {
    // Only a double-click that actually lands on the image toggles zoom. A
    // double-click on the empty area around it is, from the browser's own
    // event order (click, click, dblclick), preceded by two `click`s — the
    // first of which already closed the viewer via Viewer.tsx's
    // closeOnBackdrop (see `data-viewer-empty` below) before this handler
    // would even run. Checking the target here documents that choice
    // explicitly and keeps this component correct on its own, independent
    // of the parent's click behaviour.
    if ((e.target as HTMLElement).tagName !== 'IMG') return
    const el = activeEl()
    if (!el) return
    const rect = el.getBoundingClientRect()
    const cursor = ((c) => ({ x: e.clientX - c.x, y: e.clientY - c.y }))(centerOf(rect))
    const { nat, maxScale } = natAndMax(el, zoom.scale, natural.w)
    startGesture()
    setZoom((z) => toggleFitAndNatural(z, nat, cursor, maxScale))
  }

  const onMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
    if (e.button !== 0 || zoom.scale <= 1) return // nothing to pan at "fit"
    e.preventDefault()
    const startScale = zoom.scale
    const start = { x: e.clientX, y: e.clientY, tx: zoom.tx, ty: zoom.ty, scale: startScale, moved: false }
    setDragging(true)
    startGesture()
    const onMove = (ev: MouseEvent) => {
      const dx = ev.clientX - start.x
      const dy = ev.clientY - start.y
      if (Math.abs(dx) > DRAG_THRESHOLD || Math.abs(dy) > DRAG_THRESHOLD) start.moved = true
      setZoom(panBy({ scale: start.scale, tx: start.tx, ty: start.ty }, dx, dy))
    }
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      dragHandlersRef.current = null
      setDragging(false)
      if (start.moved) onDragEnd()
    }
    dragHandlersRef.current = { onMove, onUp }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }

  // If ImageZoom unmounts mid-drag (the user presses ←/→ or Esc while
  // holding the mouse button — ImageZoom is keyed by file.id, so switching
  // files unmounts this instance outright), onUp above never runs and the
  // window-level listeners would otherwise leak. Fix round 1 #1.
  useEffect(() => {
    return () => {
      const h = dragHandlersRef.current
      if (!h) return
      window.removeEventListener('mousemove', h.onMove)
      window.removeEventListener('mouseup', h.onUp)
      dragHandlersRef.current = null
    }
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(document.activeElement)) return
      const { zoom: z0, natural: n0, showOriginal: so } = latestRef.current
      const active = so ? originalRef.current : previewRef.current
      const { maxScale } = natAndMax(active, z0.scale, n0.w)
      // Matched by physical key (`code`), not `key` — see keyboard.ts:
      // layout-dependent punctuation (e.g. '+' is Shift+Equal on many
      // layouts, and layouts remap digits/punctuation too, not just
      // letters) would otherwise silently never fire on some layouts.
      // Equal matches with or without Shift (covers both "=" and "+").
      if (isShortcut(e, ['Digit0', 'Numpad0'])) {
        e.preventDefault()
        setZoom(FIT)
      } else if (isShortcut(e, ['Equal', 'NumpadAdd'])) {
        e.preventDefault()
        setZoom((z) => zoomAround(z, { x: 0, y: 0 }, ZOOM_STEP, maxScale))
      } else if (isShortcut(e, ['Minus', 'NumpadSubtract'])) {
        e.preventDefault()
        setZoom((z) => zoomAround(z, { x: 0, y: 0 }, 1 / ZOOM_STEP, maxScale))
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // Mount-once: `onKey` reads fresh state via `latestRef` instead.
  }, [])

  const transformStyle = {
    transform: `translate(${zoom.tx}px, ${zoom.ty}px) scale(${zoom.scale})`,
    willChange: gesturing ? ('transform' as const) : undefined,
    cursor: zoom.scale > 1 ? (dragging ? 'grabbing' : 'grab') : undefined,
  }
  // aspect-ratio (from the file's metadata, known up front): reserves the
  // correct "fit" box before the image itself decodes — without it, an
  // unloaded <img> with no width/height lays out at 0×0, so the very first
  // percent/max-zoom reading (before anything has loaded) would be off.
  const aspectStyle = hasFileDims ? { aspectRatio: `${file.width} / ${file.height}` } : undefined
  const styleFor = (visible: boolean) => (visible ? { ...aspectStyle, ...transformStyle } : aspectStyle)

  return (
    <div
      ref={containerRef}
      // data-viewer-empty: this container fills the whole pane (so the
      // image can be centered/panned within it), which means a real mouse
      // click "around" the image at fit scale (or on any part of the pane
      // the image doesn't cover when zoomed in) actually lands on this div,
      // not on the pane div behind it — Viewer.tsx's closeOnBackdrop treats
      // this marker the same as a literal backdrop hit. A click on the
      // <img> itself (or a control) is unaffected: its target is the <img>,
      // not this div, so it never matches.
      data-viewer-empty="true"
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
            // Only trust the preview's own decoded size when the file has
            // no width/height metadata of its own — the preview is usually
            // a smaller rendition, and letting it overwrite `natural` (set
            // from the original's real dimensions) made the scale
            // indicator jump twice during the preview→original swap.
            if (!hasFileDims && !originalReady) setNatural({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })
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
