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
