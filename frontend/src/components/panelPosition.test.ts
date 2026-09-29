import { placeBelow, placeBelowLeftAligned } from './panelPosition'

test('below its anchor, right-aligned to the anchor\'s right edge, when there is room', () => {
  expect(placeBelow({ top: 100, bottom: 120, right: 520 }, 1400, 900, 220, 150)).toEqual({ left: 300, top: 124 })
})

test('flips above the anchor when there is no room below, still inside the viewport', () => {
  expect(placeBelow({ top: 800, bottom: 820, right: 520 }, 1400, 900, 220, 150)).toEqual({ left: 300, top: 646 })
})

test('clamped to the left edge (min 8px) when the anchor is near the left of the viewport', () => {
  expect(placeBelow({ top: 100, bottom: 120, right: 50 }, 1400, 900, 220, 150)).toEqual({ left: 8, top: 124 })
})

test('clamped to the right edge (vw - w - 8) when the anchor is near the right of the viewport', () => {
  expect(placeBelow({ top: 100, bottom: 120, right: 1395 }, 1400, 900, 220, 150)).toEqual({ left: 1172, top: 124 })
})

test('clamped to the top edge (min 8px) when flipped above and the anchor is near the top', () => {
  expect(placeBelow({ top: 20, bottom: 40, right: 520 }, 1400, 900, 220, 150)).toEqual({ left: 300, top: 44 })
})

// PostMenu's own dimensions (W=220, ITEM_H=34, PAD=8 — see PostMenu.tsx):
// a 4-item menu (own post: mark unread/copy link/edit/delete) placed near
// the viewport's right and bottom edges, as PostMenu.test.tsx's own
// "…" button rect would produce in the app.
test("PostMenu-shaped panel (220×144, 4 items) near the viewport's bottom-right corner", () => {
  const w = 220
  const h = 4 * 34 + 8 // 144
  // anchor sits close to both edges: below/right of it has no room for
  // either dimension.
  const pos = placeBelow({ top: 860, bottom: 880, right: 1395 }, 1400, 900, w, h)
  expect(pos.left).toBe(1172) // clamped to vw - w - 8
  expect(pos.top).toBe(712) // flipped above: anchor.top(860) - h(144) - 4
})

test('PostMenu-shaped panel (220×76, 2 items — someone else\'s post) fits below near the bottom edge', () => {
  const w = 220
  const h = 2 * 34 + 8 // 76
  // anchor leaves just enough room below (880 + 4 + 76 = 960... too much —
  // pick a top that actually fits, to also cover the "still fits" branch
  // near an edge, not just the flip).
  const pos = placeBelow({ top: 700, bottom: 720, right: 1395 }, 1400, 900, w, h)
  expect(pos.left).toBe(1172)
  expect(pos.top).toBe(724) // below: anchor.bottom(720) + 4 — fits (724 + 76 = 800 <= 892)
})

// ---- placeBelowLeftAligned (Reactions' hover tooltip, controller follow-up 2026-09-29) ----

test('placeBelowLeftAligned: below its anchor, left-aligned to the anchor\'s left edge, when there is room', () => {
  expect(placeBelowLeftAligned({ top: 100, bottom: 120, left: 300 }, 1400, 900, 90, 20)).toEqual({ left: 300, top: 124 })
})

test('placeBelowLeftAligned: flips above the anchor when there is no room below, still inside the viewport', () => {
  expect(placeBelowLeftAligned({ top: 800, bottom: 820, left: 300 }, 1400, 900, 90, 150)).toEqual({ left: 300, top: 646 })
})

test('placeBelowLeftAligned: clamped to the left edge (min 8px) when the anchor is near the left of the viewport', () => {
  expect(placeBelowLeftAligned({ top: 100, bottom: 120, left: 2 }, 1400, 900, 90, 20)).toEqual({ left: 8, top: 124 })
})

test('placeBelowLeftAligned: clamped to the right edge (vw - w - 8) when the anchor is near the right of the viewport (the box would otherwise overflow)', () => {
  expect(placeBelowLeftAligned({ top: 100, bottom: 120, left: 1350 }, 1400, 900, 90, 20)).toEqual({ left: 1302, top: 124 })
})

test('placeBelowLeftAligned: clamped to the top edge (min 8px) when flipped above and the anchor is near the top', () => {
  expect(placeBelowLeftAligned({ top: 20, bottom: 40, left: 300 }, 1400, 900, 90, 150)).toEqual({ left: 300, top: 44 })
})

// The reactors tooltip shape this replaced a fixed 280×60 assumption with —
// a short "You, bob and carol" string is much narrower, so left-aligning to
// the chip (rather than placeBelow's right-alignment) keeps the box next to
// what the user actually hovered instead of stretching away to the left.
test('placeBelowLeftAligned: a short-content tooltip stays anchored at the chip, not stretched away from it', () => {
  // A reaction chip near the middle of a narrow feed column, with a small
  // measured tooltip (e.g. "You, bob and carol" at ~140x20).
  const pos = placeBelowLeftAligned({ top: 400, bottom: 424, left: 260 }, 900, 700, 140, 20)
  expect(pos.left).toBe(260) // starts right at the chip's own left edge
  expect(pos.top).toBe(428)
})
