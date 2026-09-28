// Pure sizing logic for the sidebar/thread-panel splitters (theme brief
// 2026-09-28, scope 3a). Kept free of the DOM so clamping, stepping and
// drag math are unit-tested directly; Splitter.tsx wires this to pointer/
// keyboard events and a live CSS variable.

export const RAIL_WIDTH = 64 // ServerRail's fixed width (w-16)
export const MIN_FEED_WIDTH = 360 // the feed (ChannelPane) never gets narrower than this

export const SIDEBAR_MIN = 180
export const SIDEBAR_MAX = 480
export const SIDEBAR_DEFAULT = 256 // the old fixed w-64

export const THREAD_MIN = 320
export const THREAD_DEFAULT = 420 // the old fixed w-[420px]
const THREAD_MAX_CAP = 800

export const STEP = 16 // keyboard arrow step, px

// clamp: a degenerate range (max < min, e.g. a tiny window leaving no room
// at all) collapses to min — the floor always wins over a computed ceiling.
export function clamp(value: number, min: number, max: number): number {
  if (max < min) return min
  return Math.min(Math.max(value, min), max)
}

// threadMax: the smaller of 800px and half the window (brief: "min(50% of
// the window, 800px)").
export function threadMax(windowWidth: number): number {
  return Math.min(THREAD_MAX_CAP, windowWidth / 2)
}

export interface Bounds {
  min: number
  max: number
}

// sidebarBounds: [SIDEBAR_MIN, SIDEBAR_MAX], additionally shrinking the
// ceiling so the feed keeps at least MIN_FEED_WIDTH once the rail and (if
// open) the thread panel are accounted for. The floor (SIDEBAR_MIN) always
// wins over the feed reservation — a very narrow window makes the feed the
// one that's actually cramped, not the sidebar.
export function sidebarBounds(windowWidth: number, threadWidth: number): Bounds {
  const room = windowWidth - RAIL_WIDTH - MIN_FEED_WIDTH - threadWidth
  return { min: SIDEBAR_MIN, max: Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, room)) }
}

// threadBounds is sidebarBounds's counterpart: [THREAD_MIN,
// threadMax(window)], shrunk so the feed keeps MIN_FEED_WIDTH given the
// rail and the current sidebar width.
export function threadBounds(windowWidth: number, sidebarWidth: number): Bounds {
  const room = windowWidth - RAIL_WIDTH - sidebarWidth - MIN_FEED_WIDTH
  return { min: THREAD_MIN, max: Math.min(threadMax(windowWidth), Math.max(THREAD_MIN, room)) }
}

export function clampSidebarWidth(width: number, windowWidth: number, threadWidth: number): number {
  const b = sidebarBounds(windowWidth, threadWidth)
  return clamp(width, b.min, b.max)
}

export function clampThreadWidth(width: number, windowWidth: number, sidebarWidth: number): number {
  const b = threadBounds(windowWidth, sidebarWidth)
  return clamp(width, b.min, b.max)
}

export type StepKey = 'ArrowLeft' | 'ArrowRight' | 'Home' | 'End'

// stepValue: keyboard control of a splitter — arrows move by STEP px,
// Home/End jump to the ends.
export function stepValue(current: number, key: StepKey, min: number, max: number, step = STEP): number {
  switch (key) {
    case 'ArrowRight':
      return clamp(current + step, min, max)
    case 'ArrowLeft':
      return clamp(current - step, min, max)
    case 'Home':
      return min
    case 'End':
      return max
    default:
      return current
  }
}

// dragValue: the pane's width for a pointer at x, dragging from startX with
// the pane at startWidth. sign +1: the handle sits on the pane's right edge
// (the sidebar) — dragging right grows it. sign -1: the handle sits on the
// pane's left edge (the thread panel) — dragging right shrinks it.
export function dragValue(startWidth: number, startX: number, x: number, sign: 1 | -1, min: number, max: number): number {
  return clamp(startWidth + sign * (x - startX), min, max)
}
