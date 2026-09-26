// Pure zoom math for the image viewer (Viewer.tsx / ImageZoom.tsx). Kept
// free of the DOM so wheel/drag/keyboard/double-click behaviour can be
// verified with plain numbers.
//
// Coordinate model: the image element is laid out at its "fit" box (CSS
// object-contain within the viewer, transform `scale` 1 == "fit"), centered
// by its flex parent; a CSS `transform: translate(tx px, ty px) scale(s)`
// is then painted on top. Because scale/translate never change layout, the
// transformed element's on-screen center is always
// `layout-center + (tx, ty)` — so any point expressed "relative to that
// on-screen center" is already in the same coordinate space as (tx, ty),
// regardless of the current scale. `zoomAround`'s `cursor` argument is such
// a point (screen pixels, relative to the transformed box's own center).

export interface ZoomState {
  scale: number
  tx: number
  ty: number
}

export const ZOOM_STEP = 1.2
export const MAX_ZOOM_OF_NATURAL = 8
export const FIT: ZoomState = { scale: 1, tx: 0, ty: 0 }

const EPS = 1e-6

export function isFit(state: ZoomState): boolean {
  return Math.abs(state.scale - FIT.scale) < EPS && Math.abs(state.tx) < EPS && Math.abs(state.ty) < EPS
}

// clampScale: never below "fit" (1), never above maxScale (at least 1).
export function clampScale(scale: number, maxScale: number): number {
  return Math.min(Math.max(scale, 1), Math.max(1, maxScale))
}

// naturalScale: the transform `scale` that renders one natural image pixel
// per device pixel ("100 %"), given the image's natural size and the size
// it renders at when `scale` is 1 ("fit").
export function naturalScale(naturalPx: number, fitPx: number): number {
  if (!naturalPx || !fitPx) return 1
  return naturalPx / fitPx
}

// zoomAround: zoom by `factor` (>1 in, <1 out), anchored at `cursor` — a
// point in the coordinate space described above — so the image pixel under
// the cursor stays under the cursor. Returns `state` unchanged if clamping
// leaves the scale unchanged (e.g. already at the limit).
export function zoomAround(state: ZoomState, cursor: { x: number; y: number }, factor: number, maxScale: number): ZoomState {
  const s0 = state.scale
  const s1 = clampScale(s0 * factor, maxScale)
  if (s1 === s0) return state
  // Clamped back to exactly "fit": always snap to FIT (tx=ty=0), regardless
  // of where `cursor` is or how far `state`'s translate had drifted (e.g.
  // from a pan after the zoom-in that anchored it elsewhere) — otherwise a
  // pan-then-zoom-out-to-fit sequence lands at scale 1 with a stale,
  // off-centre translate that `ImageZoom.tsx`'s `zoom.scale <= 1` guard
  // then makes undraggable.
  if (s1 === FIT.scale) return FIT
  const r = s1 / s0
  return {
    scale: s1,
    tx: cursor.x * (1 - r) + state.tx * r,
    ty: cursor.y * (1 - r) + state.ty * r,
  }
}

// panBy: translate by a screen-pixel delta (a mouse drag) — the same drag
// distance moves the image by the same screen distance at any zoom level.
export function panBy(state: ZoomState, dx: number, dy: number): ZoomState {
  return { ...state, tx: state.tx + dx, ty: state.ty + dy }
}

// toggleFitAndNatural: double-click behaviour. From "fit", zoom to natural
// size (100 %, capped at maxScale) anchored at `cursor`; from anything
// else, back to "fit".
export function toggleFitAndNatural(state: ZoomState, natural: number, cursor: { x: number; y: number }, maxScale: number): ZoomState {
  if (isFit(state)) return zoomAround(state, cursor, natural / state.scale, maxScale)
  return FIT
}

// scalePercent: the viewer's scale indicator — 100 means natural size.
export function scalePercent(scale: number, natural: number): number {
  return Math.round((scale / (natural || 1)) * 100)
}
