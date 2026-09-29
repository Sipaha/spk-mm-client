// composerToolbarFit: how many of the composer's formatting buttons fit the
// toolbar row before the rest collapse into the "more formatting" popover
// (coordinator ruling 2026-09-29, follow-up on the composer brief: the
// thread panel's narrower toolbar must not show a horizontal scrollbar —
// collapse the overflow instead, the way the webapp's responsive
// formatting bar does, but measured from real pixel widths rather than a
// handful of fixed breakpoints).
//
// Pure arithmetic, no DOM — kept separate from Composer.tsx so the fitting
// logic has its own fast, dependency-free tests. Composer.tsx supplies the
// live "available width" (its own ResizeObserver reading, minus the
// always-visible right-hand group) and calls fitCount with it.

// TOOLBAR_BUTTON_SIZE / TOOLBAR_GAP: a ToolbarButton's own footprint
// (h-7 w-7 = 28px) and the row's gap-0.5 (2px) between consecutive flex
// children — exported separately (not just pre-summed) so Composer.tsx can
// size its always-visible right-hand group without re-deriving or
// double-counting either number itself (review fix round 1, Minor: the
// previous TOOLBAR_RESERVED counted one trailing gap too many).
export const TOOLBAR_BUTTON_SIZE = 28
export const TOOLBAR_GAP = 2
// TOOLBAR_ITEM_WIDTH: one button *plus* its own trailing gap to the next
// flex child — correct for the left group's buttons, every one of which is
// always followed by something (another button, a separator, the "more"
// button, or the right-hand group).
export const TOOLBAR_ITEM_WIDTH = TOOLBAR_BUTTON_SIZE + TOOLBAR_GAP
// TOOLBAR_SEPARATOR_WIDTH: a group-break divider's footprint (mx-1 = 8px of
// margin plus its own w-px = 1px).
export const TOOLBAR_SEPARATOR_WIDTH = 9
// TOOLBAR_MORE_WIDTH: space reserved for the "more formatting" button
// itself whenever not everything fits — same footprint as any other button.
export const TOOLBAR_MORE_WIDTH = TOOLBAR_ITEM_WIDTH

// widthForCount: the pixel width of showing the first `count` of `total`
// buttons, given the (0-based) indices that render a separator right
// before them. A separator before index i only counts once count > i —
// i.e. once that button itself is among the ones shown.
export function widthForCount(count: number, separatorBeforeIndex: readonly number[]): number {
  let w = count * TOOLBAR_ITEM_WIDTH
  for (const i of separatorBeforeIndex) {
    if (i < count) w += TOOLBAR_SEPARATOR_WIDTH
  }
  return w
}

// fitCount: the largest number of buttons, 0..total, that fits within
// `available` px — every one of them if they fit outright with no "more"
// button needed at all, otherwise as many as fit alongside a reserved
// TOOLBAR_MORE_WIDTH for the "more formatting" button.
export function fitCount(available: number, total: number, separatorBeforeIndex: readonly number[]): number {
  if (available >= widthForCount(total, separatorBeforeIndex)) return total
  for (let n = total - 1; n >= 0; n--) {
    if (widthForCount(n, separatorBeforeIndex) + TOOLBAR_MORE_WIDTH <= available) return n
  }
  return 0
}
