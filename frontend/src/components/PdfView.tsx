// PdfView: a PDF rendered by pdf.js into canvases (plan ruling — option B:
// WebKitGTK's own built-in viewer is CVE-2024-4367-class and runs PDF
// JavaScript; a native pdftoppm parser is Linux-only and unsandboxed). This
// file is Viewer's own lazy chunk (`React.lazy`) — nothing of pdf.js is in
// the app's initial bundle (checked by scripts/check-pdf-bundle.mjs).
//
// Memory levers from the spike report (fixes-2026-09-28/pdf-spike-report.md,
// "these are the tuning levers"), built in from the start rather than left
// entirely to the later memory-gate task:
//  1. Only the visible pages plus one neighbour on each side keep a bitmap
//     (KEEP=1) — everything else is released.
//  2. Each page's canvas is capped at ~2 megapixels of backing store
//     regardless of zoom/device-pixel-ratio (still shown at full CSS size —
//     only the bitmap resolution is capped).
//  3. A small pool of canvas elements is reused across page swaps instead of
//     creating a fresh one per render — the harness found canvas backing
//     stores, not the JS heap, drive the slow creep across repeated opens.
//  4. Everything is released on unmount: `loadingTask.destroy()` (which
//     terminates the pdf.js worker), every render/text task cancelled, every
//     canvas zeroed.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { getDocument, GlobalWorkerOptions, TextLayer, type PDFDocumentProxy, type PDFPageProxy, type RenderTask } from 'pdfjs-dist'
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import type { FileView } from '../api/types'
import { t } from '../i18n'
import { isShortcut } from '../keyboard'
import { mediaURL } from '../media'
import './pdf.css'

GlobalWorkerOptions.workerSrc = workerUrl

const KEEP = 1 // pages kept rendered on each side of the visible ones
const MIN_SCALE = 0.25
const MAX_SCALE = 4
const ZOOM_STEP = 1.25
const CANVAS_PIXEL_CAP = 2_000_000 // ~2 MP, independent of zoom/DPR
const GAP = 12

interface Slot {
  page?: PDFPageProxy
  task?: RenderTask
  text?: TextLayer
  canvas?: HTMLCanvasElement
  scale?: number
}

function clampScale(s: number): number {
  return Math.max(MIN_SCALE, Math.min(MAX_SCALE, s))
}

function zeroCanvas(c: HTMLCanvasElement): void {
  c.width = 0
  c.height = 0
  c.remove()
}

export default function PdfView({ serverId, file, onFail }: { serverId: number; file: FileView; onFail(): void }) {
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null)
  const [base, setBase] = useState<{ w: number; h: number } | null>(null) // page 1 at scale 1, CSS px
  // naturalSizes: each page's own size at scale 1, once known (fix round 1
  // — real PDFs mix portrait/landscape/oddly-sized pages; a page not yet
  // fetched falls back to page 1's size as a placeholder until it is).
  const [naturalSizes, setNaturalSizes] = useState<Record<number, { w: number; h: number }>>({})
  const [fit, setFit] = useState(true)
  const [zoom, setZoom] = useState(1)
  const [width, setWidth] = useState(0)
  const [current, setCurrent] = useState(1)
  const [visible, setVisible] = useState<ReadonlySet<number>>(new Set())
  const scroller = useRef<HTMLDivElement>(null)
  const slots = useRef(new Map<number, Slot>())
  // Free canvases available for reuse — lever 3 above. Canvases move here
  // (already zeroed) when their page scrolls out of the visible window, and
  // are handed back out before a new one is ever created.
  const pool = useRef<HTMLCanvasElement[]>([])
  const onFailRef = useRef(onFail)
  onFailRef.current = onFail

  const scale = base && fit && width ? clampScale((width - 48) / base.w) : zoom
  // Kept in a ref so the mount-once keyboard/wheel handlers below can read
  // the scale actually on screen (fit or manual) without resubscribing.
  const scaleRef = useRef(scale)
  scaleRef.current = scale

  function takeCanvas(): HTMLCanvasElement {
    return pool.current.pop() ?? document.createElement('canvas')
  }
  function returnCanvas(c: HTMLCanvasElement): void {
    zeroCanvas(c)
    pool.current.push(c)
  }
  function releaseSlot(s: Slot): void {
    s.task?.cancel()
    s.text?.cancel()
    if (s.canvas) returnCanvas(s.canvas)
    s.canvas = undefined
    s.page?.cleanup()
  }

  // Load: our own fetch of /media/<srv>/pdf/<id> (Go spools it to disk with
  // a size cap and checks the %PDF- magic before serving it) — the bytes
  // are handed to pdf.js's display API, which never loads pdf.js's own
  // scripting sandbox and never fetches a URL itself.
  useEffect(() => {
    let cancelled = false
    const ac = new AbortController()
    let loadingTask: ReturnType<typeof getDocument> | undefined
    ;(async () => {
      try {
        const r = await fetch(mediaURL(serverId, 'pdf', file.id), { signal: ac.signal })
        if (!r.ok) throw new Error(`status ${r.status}`)
        const data = new Uint8Array(await r.arrayBuffer())
        if (cancelled) return
        loadingTask = getDocument({ data, enableXfa: false })
        const d = await loadingTask.promise
        if (cancelled) return
        const p1 = await d.getPage(1)
        const v = p1.getViewport({ scale: 1 })
        if (cancelled) return
        setBase({ w: v.width, h: v.height })
        setNaturalSizes({ 1: { w: v.width, h: v.height } })
        setDoc(d)
      } catch {
        if (!cancelled) onFailRef.current()
      }
    })()
    return () => {
      cancelled = true
      ac.abort()
      for (const s of slots.current.values()) releaseSlot(s)
      slots.current.clear()
      for (const c of pool.current) zeroCanvas(c) // already zeroed, but drop the pool too
      pool.current = []
      void loadingTask?.destroy() // terminates the worker
    }
  }, [serverId, file.id])

  // Width of the pane → fit-to-width scale.
  useLayoutEffect(() => {
    const el = scroller.current
    if (!el) return
    setWidth(el.clientWidth)
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => setWidth(el.clientWidth))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const n = doc?.numPages ?? 0

  // boxOf: a page's own CSS box at the current scale — its measured size
  // once known (renderPage below records it the moment the page is
  // fetched, before it's actually rendered), or page 1's as a placeholder
  // until then. Never a single shared size for every page: a landscape
  // page stretched into a portrait page's box would distort its bitmap and
  // misalign the text layer over it (fix round 1).
  function boxOf(i: number): { w: number; h: number } {
    const sz = naturalSizes[i] ?? base
    return sz ? { w: Math.round(sz.w * scale), h: Math.round(sz.h * scale) } : { w: 0, h: 0 }
  }

  // layout: cumulative top offset and box height of every page, from
  // boxOf — recomputed whenever a page's real size becomes known, the zoom
  // changes, or the document (page count) changes. O(n) per change, which
  // is cheap next to the cost of actually rendering a page.
  const layout = useMemo(() => {
    const offsets: number[] = new Array(n + 1).fill(0) // 1-indexed
    const heights: number[] = new Array(n + 1).fill(0)
    let y = 0
    for (let i = 1; i <= n; i++) {
      const h = boxOf(i).h
      offsets[i] = y
      heights[i] = h
      y += h + GAP
    }
    return { offsets, heights }
  }, [naturalSizes, base, scale, n])

  // Which pages are on screen: from scrollTop and each page's own real (or
  // placeholder) height — no longer a uniform-page-size assumption (fix
  // round 1). `<=` matches the old floor-based formula's boundary exactly
  // in the uniform case: a page's slot is treated as reaching up to (but
  // not including) the next page's offset.
  useEffect(() => {
    const el = scroller.current
    if (!el || !n) return
    const update = () => {
      const top = el.scrollTop
      const bottom = top + el.clientHeight
      let first = 1
      for (let i = 1; i <= n; i++) if (layout.offsets[i] <= top) first = i
      let last = first
      for (let i = 1; i <= n; i++) if (layout.offsets[i] <= bottom) last = i
      setCurrent(first)
      const s = new Set<number>()
      for (let i = Math.max(1, first - KEEP); i <= Math.min(n, last + KEEP); i++) s.add(i)
      setVisible((old) => (old.size === s.size && [...s].every((i) => old.has(i)) ? old : s))
    }
    update()
    el.addEventListener('scroll', update, { passive: true })
    return () => el.removeEventListener('scroll', update)
  }, [layout, n])

  // Render the visible pages; release everything that fell out of the
  // window first, so its canvas is back in the pool before a newly visible
  // page asks for one.
  useEffect(() => {
    if (!doc) return
    const map = slots.current
    for (const [i, s] of map) {
      if (!visible.has(i)) {
        releaseSlot(s)
        map.delete(i)
      }
    }
    for (const i of visible) {
      const s = map.get(i) ?? {}
      map.set(i, s)
      if (s.scale === scale && s.canvas) continue
      void renderPage(doc, i, s, scale)
    }
  }, [doc, visible, scale])

  function zoomBy(factor: number): void {
    setFit(false)
    setZoom(clampScale(scaleRef.current * factor))
  }

  // Ctrl+=/−/0 (mount-once — reads the live scale via scaleRef, same
  // reasoning as ImageZoom.tsx). Plain arrow keys and Escape are left alone
  // so they keep reaching Viewer's own window-level handler (paging/close).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isShortcut(e, ['Equal', 'NumpadAdd'], { ctrl: true })) {
        e.preventDefault()
        setFit(false)
        setZoom(clampScale(scaleRef.current * ZOOM_STEP))
      } else if (isShortcut(e, ['Minus', 'NumpadSubtract'], { ctrl: true })) {
        e.preventDefault()
        setFit(false)
        setZoom(clampScale(scaleRef.current / ZOOM_STEP))
      } else if (isShortcut(e, ['Digit0', 'Numpad0'], { ctrl: true })) {
        e.preventDefault()
        setFit(true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Ctrl+wheel zoom. A plain React onWheel is passive at the root (React
  // 17+), so preventDefault() there is a silent no-op in real browsers —
  // same reasoning as ImageZoom.tsx's wheel handler.
  useEffect(() => {
    const el = scroller.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey) return
      e.preventDefault()
      zoomBy(e.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP)
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [])

  const btn = 'rounded px-2 py-0.5 hover:bg-hover'
  const pages = Array.from({ length: n }, (_, k) => k + 1)
  return (
    <div className="flex h-full w-full min-w-0 flex-col">
      {/* bg-panel (opaque): fix round 1, controller review — this row had no
          background of its own and relied on the dialog's translucent
          bg-black/85 backdrop, so the sidebar behind the viewer showed
          through the zoom controls. TextView.tsx's own toolbar row gets the
          same fix. */}
      <div className="mb-2 flex shrink-0 items-center gap-2 rounded bg-panel px-2 py-1.5 text-xs text-fg-muted">
        <span>{n ? t('pdf.page', { i: String(current), n: String(n) }) : t('file.loading')}</span>
        <button type="button" aria-label={t('pdf.zoomOut')} className={btn} onClick={() => zoomBy(1 / ZOOM_STEP)}>
          −
        </button>
        <span>{t('pdf.zoomPercent', { p: String(Math.round(scale * 100)) })}</span>
        <button type="button" aria-label={t('pdf.zoomIn')} className={btn} onClick={() => zoomBy(ZOOM_STEP)}>
          +
        </button>
        <button type="button" aria-pressed={fit} className={btn} onClick={() => setFit(true)}>
          {t('pdf.fitWidth')}
        </button>
      </div>
      <div ref={scroller} className="min-h-0 flex-1 overflow-auto">
        {base &&
          pages.map((i) => {
            // bg-panel, not a hard-coded white: the app is dark by default
            // (theme.test.ts guards against light patches) and this box is
            // only the placeholder shown before a page's canvas has
            // rendered — the canvas itself paints the PDF's own (usually
            // white) page background once it loads. Sized from the page's
            // own measured viewport (or page 1's, as a placeholder, until
            // it's been fetched) — never a single shared size for every
            // page (fix round 1).
            const box = boxOf(i)
            return (
              <div key={i} data-page={i} className="relative mx-auto bg-panel shadow" style={{ width: box.w, height: box.h, marginBottom: GAP }} />
            )
          })}
      </div>
    </div>
  )

  async function renderPage(d: PDFDocumentProxy, i: number, s: Slot, sc: number): Promise<void> {
    s.task?.cancel()
    s.text?.cancel()
    try {
      s.page ??= await d.getPage(i)
      if (slots.current.get(i) !== s) return // released while awaiting getPage
      // Measured the moment the page is known — before it's actually
      // rendered, so its placeholder box (and the scroll/current-page math
      // above) is corrected as early as possible, not just once painted.
      if (!naturalSizes[i]) {
        const v1 = s.page.getViewport({ scale: 1 })
        setNaturalSizes((old) => (old[i] ? old : { ...old, [i]: { w: v1.width, h: v1.height } }))
      }
      const vp = s.page.getViewport({ scale: sc })
      const dpr = window.devicePixelRatio || 1
      // Lever 2: cap the bitmap at ~2 MP regardless of zoom/DPR — the CSS
      // size (below) is unaffected, only the backing-store resolution is.
      const renderScale = vp.width * vp.height * dpr * dpr > CANVAS_PIXEL_CAP ? Math.sqrt(CANVAS_PIXEL_CAP / (vp.width * vp.height)) : dpr
      const canvas = s.canvas ?? takeCanvas()
      canvas.width = Math.max(1, Math.floor(vp.width * renderScale))
      canvas.height = Math.max(1, Math.floor(vp.height * renderScale))
      canvas.style.cssText = 'position:absolute;inset:0;width:100%;height:100%'
      const task = s.page.render({ canvas, viewport: vp, transform: renderScale !== 1 ? [renderScale, 0, 0, renderScale, 0, 0] : undefined })
      s.task = task
      await task.promise
      if (slots.current.get(i) !== s) {
        returnCanvas(canvas)
        return
      }
      s.canvas = canvas
      s.scale = sc
      const host = scroller.current?.querySelector<HTMLElement>(`[data-page="${i}"]`)
      if (!host) return
      host.replaceChildren(canvas)
      const layer = document.createElement('div')
      layer.className = 'pdf-text-layer'
      layer.style.setProperty('--total-scale-factor', String(sc))
      host.appendChild(layer)
      const text = new TextLayer({ textContentSource: s.page.streamTextContent(), container: layer, viewport: vp })
      s.text = text
      await text.render()
    } catch {
      // Cancelled (a newer render/unmount superseded this one) or a broken
      // page: leave the blank page box rather than throwing out of pdf.js
      // internals — a single bad page must not fail the whole document.
    }
  }
}
