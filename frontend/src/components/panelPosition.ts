// placeBelow puts a w×h panel under its anchor (right-aligned to the
// anchor's right edge), or above it when there is no room below, always
// inside the viewport. Shared by EmojiPicker's placePicker, Downloads'
// placePanel and PostMenu — three near-identical popovers anchored to a
// toolbar button (Task 2, UI pass 2026-09-28).
export function placeBelow(anchor: Pick<DOMRect, 'top' | 'bottom' | 'right'>, vw: number, vh: number, w: number, h: number) {
  const left = Math.max(8, Math.min(anchor.right - w, vw - w - 8))
  const below = anchor.bottom + 4
  const top = below + h <= vh - 8 ? below : Math.max(8, anchor.top - h - 4)
  return { left, top }
}

// placeBelowLeftAligned: same below/flip-above-when-no-room and
// viewport-clamp logic as placeBelow, but left-aligned to the anchor's
// *left* edge instead of right-aligned to its right edge. A separate
// function (not a parameter on placeBelow) so the three existing
// toolbar-button popovers built on placeBelow (EmojiPicker, Downloads,
// PostMenu — all right-aligned to a button near the right of their row)
// keep their exact behaviour untouched. Added for Reactions' hover tooltip
// (controller follow-up, 2026-09-29): once the tooltip shrinks to fit its
// (now much shorter, "reacted with :emoji:" removed) content, right-aligning
// it to the chip put most of the box to the *left* of the chip the user is
// actually pointing at — left-aligning keeps it anchored where the pointer
// is.
export function placeBelowLeftAligned(anchor: Pick<DOMRect, 'top' | 'bottom' | 'left'>, vw: number, vh: number, w: number, h: number) {
  const left = Math.max(8, Math.min(anchor.left, vw - w - 8))
  const below = anchor.bottom + 4
  const top = below + h <= vh - 8 ? below : Math.max(8, anchor.top - h - 4)
  return { left, top }
}
