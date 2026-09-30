// PdfView: a PDF rendered by pdf.js into canvases (plan ruling — option B:
// WebKitGTK's own built-in viewer is CVE-2024-4367-class and runs PDF
// JavaScript; a native pdftoppm parser is Linux-only and unsandboxed). This
// file is Viewer's own lazy chunk (`React.lazy`) — nothing of pdf.js is in
// the app's initial bundle (checked by scripts/check-pdf-bundle.mjs).
//
// Memory levers from the spike report (fixes-2026-09-28/pdf-spike-report.md,
// "these are the tuning levers"), built in from the start rather than left
// entirely to the later memory check:
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

// Optional pdf.js assets (final review I3): scanned pages (CCITT/JBIG2/JPX
// images) and ICC colour spaces decode through wasm modules the worker
// fetches on demand from wasmUrl; CJK text with predefined CMaps needs
// cMapUrl; the 14 standard (non-embedded) fonts need standardFontDataUrl.
// Without these, pdf.js silently leaves the page blank — no error, no
// fallback to the card (a corrupted/unparseable document is what triggers
// onFail, not a page it merely couldn't fully paint).
// vite.config.ts's copyPdfjsAssets plugin copies these straight from the
// pinned pdfjs-dist into dist/pdfjs/ at build time — none of it is in the
// initial bundle or even in this lazy chunk; each file is its own request,
// made only if a document actually needs it. Never quickjs-eval.* (pdf.js's
// scripting sandbox): the plugin does not copy it, and the plan forbids PDF
// scripting regardless.
const PDFJS_ASSETS = '/pdfjs/'

const KEEP = 1 // pages kept rendered on each side of the visible ones
const MIN_SCALE = 0.25
const MAX_SCALE = 4
const ZOOM_STEP = 1.25
const CANVAS_PIXEL_CAP = 2_000_000 // ~2 MP, independent of zoom/DPR
// The default zoom, the webapp's (ZoomSettings.DEFAULT_SCALE in
// webapp/channels/src/utils/constants.tsx; pdf_preview.tsx renders the
// page viewport at that scale, 1 CSS px per PDF point at scale 1): a Letter
// page opens ~1071 px wide, centred, capped so it never exceeds the pane
// (pdf-lag report 2026-09-30 — fitting every page to the pane opened a
// receipt at 301% on a 1920 px window). "Fit width" stays an explicit mode.
const DEFAULT_SCALE = 1.75
const GAP = 12

// auto: DEFAULT_SCALE, capped at each page's own fit-width scale (the
// default, and what Ctrl+0 returns to); fit: each page fit to the pane's
// width; manual: one uniform `zoom` for every page (+/−, Ctrl+wheel).
type ZoomMode = 'auto' | 'fit' | 'manual'

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
  const [mode, setMode] = useState<ZoomMode>('auto')
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
  // The scroll anchor's reading position (page + fraction of it scrolled
  // past) — see the scroll-anchor effect below.
  const anchor = useRef({ page: 1, frac: 0 })
  const onFailRef = useRef(onFail)
  onFailRef.current = onFail

  // scaleFor: fix round 2 — in fit mode each page is fit to *its own*
  // width, not a single scale derived from page 1 and applied to every
  // page's box; a landscape page at page 1's scale would be ~41% wider
  // than the pane, forcing a horizontal scrollbar onto the whole scroller
  // (a CSS overflow container's scrollbar is governed by its widest
  // child), even while looking at a page that itself fits fine. The default
  // (auto) mode caps DEFAULT_SCALE at that same per-page fit scale. In
  // manual zoom, one uniform scale applies to every page, unchanged.
  function scaleFor(i: number): number {
    const sz = naturalSizes[i] ?? base
    if (mode !== 'manual' && sz && width) {
      const fitScale = (width - 48) / sz.w
      return clampScale(mode === 'fit' ? fitScale : Math.min(DEFAULT_SCALE, fitScale))
    }
    return zoom
  }
  // The zoom % shown in auto/fit mode is the *current* page's own scale.
  const displayScale = scaleFor(current)
  // Kept in a ref so the mount-once keyboard/wheel handlers below can read
  // the scale actually on screen (auto, fit or manual) without resubscribing.
  const scaleRef = useRef(displayScale)
  scaleRef.current = displayScale

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
        loadingTask = getDocument({
          data,
          enableXfa: false,
          cMapUrl: `${PDFJS_ASSETS}cmaps/`,
          cMapPacked: true,
          standardFontDataUrl: `${PDFJS_ASSETS}standard_fonts/`,
          wasmUrl: `${PDFJS_ASSETS}wasm/`,
        })
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

  // boxOf: a page's own CSS box — its measured size once known (renderPage
  // below records it the moment the page is fetched, before it's actually
  // rendered), or page 1's as a placeholder until then, at *that page's
  // own* scale (scaleFor(i), fix round 2) — never a single shared size or
  // a single shared scale for every page: either would distort a page's
  // bitmap/text layer (fix round 1) or force it to overflow horizontally
  // in fit mode (fix round 2).
  function boxOf(i: number): { w: number; h: number } {
    const sz = naturalSizes[i] ?? base
    if (!sz) return { w: 0, h: 0 }
    const sc = scaleFor(i)
    return { w: Math.round(sz.w * sc), h: Math.round(sz.h * sc) }
  }

  // layout: cumulative top offset and box height of every page, from
  // boxOf — recomputed whenever a page's real size becomes known, the fit
  // pane width changes, the zoom changes, the zoom mode changes, or the document
  // (page count) changes. O(n) per change, which is cheap next to the cost
  // of actually rendering a page.
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
  }, [naturalSizes, base, mode, width, zoom, n])

  // Which pages are on screen: from scrollTop and each page's own real (or
  // placeholder) height — no longer a uniform-page-size assumption (fix
  // round 1). `<=` matches the old floor-based formula's boundary exactly
  // in the uniform case: a page's slot is treated as reaching up to (but
  // not including) the next page's offset.
  useEffect(() => {
    const el = scroller.current
    if (!el || !n) return
    const update = (e?: Event) => {
      const top = el.scrollTop
      const bottom = top + el.clientHeight
      let first = 1
      for (let i = 1; i <= n; i++) if (layout.offsets[i] <= top) first = i
      let last = first
      for (let i = 1; i <= n; i++) if (layout.offsets[i] <= bottom) last = i
      setCurrent(first)
      // The reading position within the page, for the scroll anchor below.
      // Only from real scroll events: on a layout change this effect's own
      // first call pairs the *new* layout with the *old* scrollTop.
      if (e) {
        const h = layout.heights[first] || 1
        anchor.current = { page: first, frac: Math.min(1, Math.max(0, (top - layout.offsets[first]) / h)) }
      }
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
      const sc = scaleFor(i)
      if (s.scale === sc && s.canvas) continue
      void renderPage(doc, i, s, sc)
    }
  }, [doc, visible, mode, width, zoom, naturalSizes, base])

  // Scroll anchor (fix round 2): whenever the scale actually on screen
  // changes — zoom mode switched, manual zoom changed, or the pane resized (which
  // re-fits every page) — keep the same place of the same page under the
  // top of the scroller (the page plus the fraction of it scrolled past,
  // from the last scroll event), rather than leaving scrollTop at its old
  // pixel value (which would land on a different page once every page's
  // height has changed). Only when that place actually moved (review M2 of
  // the pdf-lag fix): a resize that leaves the scale alone — the default
  // zoom capped at 175% on a wide pane — must not touch scrollTop at all
  // (it used to jump to the page's top). Skipped on mount (prevAnchorKey
  // starts null): nothing to anchor to yet.
  const prevAnchorKey = useRef<string | null>(null)
  useEffect(() => {
    const key = `${mode}|${zoom}|${width}`
    const el = scroller.current
    if (el && n && prevAnchorKey.current !== null && prevAnchorKey.current !== key) {
      const { page, frac } = anchor.current
      const top = Math.round((layout.offsets[page] ?? 0) + frac * (layout.heights[page] ?? 0))
      if (Math.abs(top - el.scrollTop) > 1) el.scrollTo({ top })
    }
    prevAnchorKey.current = key
  }, [mode, zoom, width])

  function zoomBy(factor: number): void {
    setMode('manual')
    setZoom(clampScale(scaleRef.current * factor))
  }

  // Ctrl+=/−/0 (mount-once — reads the live scale via scaleRef, same
  // reasoning as ImageZoom.tsx). Plain arrow keys and Escape are left alone
  // so they keep reaching Viewer's own window-level handler (paging/close).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isShortcut(e, ['Equal', 'NumpadAdd'], { ctrl: true })) {
        e.preventDefault()
        setMode('manual')
        setZoom(clampScale(scaleRef.current * ZOOM_STEP))
      } else if (isShortcut(e, ['Minus', 'NumpadSubtract'], { ctrl: true })) {
        e.preventDefault()
        setMode('manual')
        setZoom(clampScale(scaleRef.current / ZOOM_STEP))
      } else if (isShortcut(e, ['Digit0', 'Numpad0'], { ctrl: true })) {
        e.preventDefault()
        setMode('auto')
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
    // data-viewer-empty (root and scroller, not the page boxes): the dark
    // area around the pages counts as the viewer's empty area — a click
    // there closes the viewer, like the image viewer's (Viewer.tsx's
    // closeOnBackdrop, which also ignores drags, text selections and
    // scrollbar presses).
    <div data-viewer-empty="true" className="flex h-full w-full min-w-0 flex-col">
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
        <span>{t('pdf.zoomPercent', { p: String(Math.round(displayScale * 100)) })}</span>
        <button type="button" aria-label={t('pdf.zoomIn')} className={btn} onClick={() => zoomBy(ZOOM_STEP)}>
          +
        </button>
        {/* A toggle (review M3): pressed again, it returns to the default zoom. */}
        <button type="button" aria-pressed={mode === 'fit'} className={btn} onClick={() => setMode((m) => (m === 'fit' ? 'auto' : 'fit'))}>
          {t('pdf.fitWidth')}
        </button>
      </div>
      <div ref={scroller} data-viewer-empty="true" className="min-h-0 flex-1 overflow-auto">
        {base &&
          pages.map((i) => {
            // bg-panel, not a hard-coded white: the app is dark by default
            // (theme.test.ts guards against light patches) and this box is
            // only the placeholder shown before a page's canvas has
            // rendered — the canvas itself paints the PDF's own (usually
            // white) page background once it loads. Sized from the page's
            // own measured viewport (or page 1's, as a placeholder, until
            // it's been fetched) — never a single shared size for every
            // page (fix round 1). No box-shadow (or any blur/filter): WebKitGTK
            // repaints this page-sized box on every wheel-scroll step, and a
            // blurred shadow made each step cost 125–200 ms instead of ~32 ms
            // (janky scrolling — pdf-lag report 2026-09-30; Chromium doesn't
            // care). The dark backdrop frames the page anyway.
            const box = boxOf(i)
            return (
              <div key={i} data-page={i} className="relative mx-auto bg-panel" style={{ width: box.w, height: box.h, marginBottom: GAP }} />
            )
          })}
      </div>
    </div>
  )

  async function renderPage(d: PDFDocumentProxy, i: number, s: Slot, sc: number): Promise<void> {
    s.task?.cancel()
    s.text?.cancel()
    // Hoisted so the catch block below can return it to the pool if this
    // render never finished (M5, final review): it may already have been
    // taken from the pool and sized before the cancel/error.
    let canvas: HTMLCanvasElement | undefined
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
      canvas = s.canvas ?? takeCanvas()
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
      // internals — a single bad page must not fail the whole document. A
      // canvas taken from the pool (or newly created) before the cancel,
      // but never committed as s.canvas, must go back to the pool zeroed
      // (M5, final review) — canvas backing stores, not the JS heap, are
      // the memory driver found in the spike. `canvas !== s.canvas` also
      // correctly leaves alone a canvas that WAS already showing a previous
      // successful render of this same page (a re-render at a new scale
      // was cancelled) — it's still valid, still attached, still s.canvas.
      if (canvas && canvas !== s.canvas) returnCanvas(canvas)
    }
  }
}
