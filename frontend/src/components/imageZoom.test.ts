import { describe, expect, test } from 'vitest'
import { FIT, MAX_ZOOM_OF_NATURAL, ZOOM_STEP, clampScale, isFit, naturalScale, panBy, scalePercent, toggleFitAndNatural, zoomAround } from './imageZoom'

describe('clampScale', () => {
  test('never goes below fit (1)', () => {
    expect(clampScale(0.3, 8)).toBe(1)
  })
  test('never exceeds maxScale', () => {
    expect(clampScale(50, 8)).toBe(8)
  })
  test('maxScale below 1 still allows fit', () => {
    expect(clampScale(5, 0.5)).toBe(1)
  })
})

describe('naturalScale', () => {
  test('ratio of natural size to the fitted box', () => {
    expect(naturalScale(1920, 960)).toBe(2)
  })
  test('guards against a zero fit box', () => {
    expect(naturalScale(1920, 0)).toBe(1)
  })
})

describe('zoomAround', () => {
  test('zooming in around the center keeps translate at zero', () => {
    const next = zoomAround(FIT, { x: 0, y: 0 }, ZOOM_STEP, 8)
    expect(next.scale).toBeCloseTo(1.2)
    expect(next.tx).toBeCloseTo(0)
    expect(next.ty).toBeCloseTo(0)
  })

  test('the image point under the cursor stays under the cursor', () => {
    // Anchor at (100, 40) relative to the box's own center, zoom in.
    const cursor = { x: 100, y: 40 }
    const s0 = zoomAround(FIT, cursor, 1.5, 8)
    // The image-space point under the cursor before the zoom:
    const p0 = { x: (cursor.x - FIT.tx) / FIT.scale, y: (cursor.y - FIT.ty) / FIT.scale }
    // After the zoom, the same image-space point must map back to `cursor`.
    const screenAfter = { x: s0.scale * p0.x + s0.tx, y: s0.scale * p0.y + s0.ty }
    expect(screenAfter.x).toBeCloseTo(cursor.x)
    expect(screenAfter.y).toBeCloseTo(cursor.y)
  })

  test('repeated zoom-in at the same cursor keeps that point fixed', () => {
    const cursor = { x: -50, y: 30 }
    let s = FIT
    for (let i = 0; i < 5; i++) s = zoomAround(s, cursor, ZOOM_STEP, 8)
    const p0 = { x: cursor.x / FIT.scale, y: cursor.y / FIT.scale } // FIT.tx/ty are 0
    const screenAfter = { x: s.scale * p0.x + s.tx, y: s.scale * p0.y + s.ty }
    expect(screenAfter.x).toBeCloseTo(cursor.x)
    expect(screenAfter.y).toBeCloseTo(cursor.y)
  })

  test('clamped at maxScale: zooming further in is a no-op (same reference)', () => {
    const atMax = { scale: 8, tx: 3, ty: 4 }
    const next = zoomAround(atMax, { x: 10, y: 10 }, ZOOM_STEP, 8)
    expect(next).toBe(atMax)
  })

  test('zooming out from fit clamps back to exactly fit', () => {
    const next = zoomAround(FIT, { x: 5, y: 5 }, 1 / ZOOM_STEP, 8)
    expect(next).toEqual(FIT)
  })

  test('zooming out from a zoomed state moves toward fit, anchored at the cursor', () => {
    const zoomedIn = zoomAround(FIT, { x: 20, y: 0 }, 2, 8)
    const back = zoomAround(zoomedIn, { x: 20, y: 0 }, 0.5, 8)
    expect(back.scale).toBeCloseTo(1)
    expect(back.tx).toBeCloseTo(0)
  })

  test('pan then wheel out to fit gives FIT, not an off-centre scale-1 state', () => {
    // Reviewer's repro: zoom in a few steps anchored off-center, pan away
    // from that anchor (so the zoomed-in translate no longer cancels out),
    // then wheel out repeatedly until scale clamps back to exactly 1. The
    // clamp used to keep whatever translate the last step computed instead
    // of snapping to FIT, leaving the image off-centre and (per
    // ImageZoom.tsx's `zoom.scale <= 1` guard) undraggable.
    let s = FIT
    const cursor = { x: -200, y: -100 }
    for (let i = 0; i < 3; i++) s = zoomAround(s, cursor, ZOOM_STEP, 8)
    s = panBy(s, 300, 0)
    for (let i = 0; i < 10; i++) s = zoomAround(s, cursor, 1 / ZOOM_STEP, 8)
    expect(s).toEqual(FIT)
  })
})

describe('panBy', () => {
  test('moves translate by the drag delta, independent of scale', () => {
    const state = { scale: 3, tx: 10, ty: -5 }
    expect(panBy(state, 4, 6)).toEqual({ scale: 3, tx: 14, ty: 1 })
  })
})

describe('isFit / toggleFitAndNatural', () => {
  test('isFit is true only at exactly scale 1, tx 0, ty 0', () => {
    expect(isFit(FIT)).toBe(true)
    expect(isFit({ scale: 1.001, tx: 0, ty: 0 })).toBe(false)
    expect(isFit({ scale: 1, tx: 2, ty: 0 })).toBe(false)
  })

  test('from fit, jumps to natural size anchored at the cursor', () => {
    const next = toggleFitAndNatural(FIT, 2.5, { x: 12, y: -8 }, 8)
    expect(next.scale).toBeCloseTo(2.5)
    // Anchored zoom: the cursor's image point stays put.
    const p0 = { x: 12, y: -8 } // FIT has scale 1, tx/ty 0
    expect(next.tx).toBeCloseTo(12 - 2.5 * p0.x)
    expect(next.ty).toBeCloseTo(-8 - 2.5 * p0.y)
  })

  test('natural size above maxScale is clamped', () => {
    const next = toggleFitAndNatural(FIT, 20, { x: 0, y: 0 }, 8)
    expect(next.scale).toBe(8)
  })

  test('from anything other than fit, goes back to fit', () => {
    expect(toggleFitAndNatural({ scale: 2.5, tx: 5, ty: 5 }, 2.5, { x: 0, y: 0 }, 8)).toEqual(FIT)
    expect(toggleFitAndNatural({ scale: 1, tx: 5, ty: 0 }, 2.5, { x: 0, y: 0 }, 8)).toEqual(FIT)
  })
})

describe('scalePercent', () => {
  test('100 at natural size', () => {
    expect(scalePercent(2, 2)).toBe(100)
  })
  test('rounds and reflects fit below natural size', () => {
    expect(scalePercent(1, 4)).toBe(25)
  })
  test('guards a zero/unknown natural size', () => {
    expect(scalePercent(1.5, 0)).toBe(150)
  })
})

test('MAX_ZOOM_OF_NATURAL is the documented 8x cap', () => {
  expect(MAX_ZOOM_OF_NATURAL).toBe(8)
})
