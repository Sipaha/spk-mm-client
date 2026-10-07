import { describe, expect, test } from 'vitest'
import {
  RAIL_WIDTH,
  MIN_FEED_WIDTH,
  SIDEBAR_DEFAULT,
  SIDEBAR_MAX,
  SIDEBAR_MIN,
  THREAD_DEFAULT,
  THREAD_MIN,
  clamp,
  clampSidebarWidth,
  clampThreadWidth,
  dragValue,
  sidebarBounds,
  stepValue,
  threadBounds,
  threadMax,
} from './splitterGeometry'

describe('clamp', () => {
  test('keeps a value inside [min, max]', () => {
    expect(clamp(100, 180, 480)).toBe(180)
    expect(clamp(300, 180, 480)).toBe(300)
    expect(clamp(900, 180, 480)).toBe(480)
  })
  test('a degenerate range (max < min) collapses to min', () => {
    expect(clamp(300, 400, 100)).toBe(400)
  })
})

describe('threadMax', () => {
  test('the smaller of 800 and half the window', () => {
    expect(threadMax(2000)).toBe(800)
    expect(threadMax(1200)).toBe(600)
  })
})

describe('clampSidebarWidth', () => {
  test('stays within its own [180, 480] band on a roomy window', () => {
    expect(clampSidebarWidth(300, 1600, 0)).toBe(300)
    expect(clampSidebarWidth(50, 1600, 0)).toBe(SIDEBAR_MIN)
    expect(clampSidebarWidth(900, 1600, 0)).toBe(SIDEBAR_MAX)
  })
  test('shrinks below its own max to leave the feed at least 360px, thread panel open', () => {
    // window 900: rail 64 + feed 360 + thread 420 = 844, leaving only 56 for the sidebar.
    const w = clampSidebarWidth(300, 900, 420)
    expect(w).toBe(SIDEBAR_MIN) // clamps to its floor rather than going below it
  })
  test('accepts an optional railWidth override, passed through to sidebarBounds', () => {
    expect(clampSidebarWidth(2000, 760, 0)).toBe(336)
    expect(clampSidebarWidth(2000, 760, 0, 0)).toBe(336 + RAIL_WIDTH)
  })
})

// Sidebar-menu-brief addendum (2026-09-29): the server rail disappears with
// exactly one server, so App.tsx passes railWidth 0 into these instead of
// the default RAIL_WIDTH — the sidebar/thread panel get that 64px back.
describe('sidebarBounds/threadBounds: optional railWidth (default RAIL_WIDTH, App.tsx passes 0 when the rail is hidden)', () => {
  test('sidebarBounds: a hidden rail (railWidth 0) gives exactly RAIL_WIDTH more room', () => {
    expect(sidebarBounds(760, 0).max).toBe(336)
    expect(sidebarBounds(760, 0, 0).max).toBe(336 + RAIL_WIDTH)
  })
  test('threadBounds: a hidden rail (railWidth 0) gives exactly RAIL_WIDTH more room', () => {
    expect(threadBounds(1200, 400).max).toBe(376)
    expect(threadBounds(1200, 400, 0).max).toBe(376 + RAIL_WIDTH)
  })
})

describe('clampThreadWidth', () => {
  test('stays within [320, min(800, window/2)] on a roomy window', () => {
    expect(clampThreadWidth(420, 1600, 256)).toBe(420)
    expect(clampThreadWidth(100, 1600, 256)).toBe(THREAD_MIN)
    expect(clampThreadWidth(2000, 1600, 256)).toBe(800)
  })
  test('shrinks to leave the feed at least 360px', () => {
    // window 900: rail 64 + sidebar 256 + feed 360 = 680, leaving 220 for the thread panel.
    const w = clampThreadWidth(420, 900, 256)
    expect(w).toBe(THREAD_MIN) // floors rather than going below it
  })
  test('accepts an optional railWidth override, passed through to threadBounds', () => {
    expect(clampThreadWidth(2000, 1200, 400)).toBe(376)
    expect(clampThreadWidth(2000, 1200, 400, 0)).toBe(376 + RAIL_WIDTH)
  })
})

describe('stepValue', () => {
  test('ArrowRight/ArrowLeft move by 16px, clamped', () => {
    expect(stepValue(300, 'ArrowRight', 180, 480)).toBe(316)
    expect(stepValue(300, 'ArrowLeft', 180, 480)).toBe(284)
    expect(stepValue(475, 'ArrowRight', 180, 480)).toBe(480)
    expect(stepValue(185, 'ArrowLeft', 180, 480)).toBe(180)
  })
  test('Home/End jump to min/max', () => {
    expect(stepValue(300, 'Home', 180, 480)).toBe(180)
    expect(stepValue(300, 'End', 180, 480)).toBe(480)
  })
})

describe('dragValue', () => {
  test('sign +1: dragging right grows the pane', () => {
    expect(dragValue(300, 500, 560, 1, 180, 480)).toBe(360)
    expect(dragValue(300, 500, 440, 1, 180, 480)).toBe(240)
  })
  test('sign -1: dragging right shrinks the pane (thread panel, handle on its left edge)', () => {
    expect(dragValue(420, 500, 560, -1, 320, 800)).toBe(360)
    expect(dragValue(420, 500, 440, -1, 320, 800)).toBe(480)
  })
  test('clamps mid-drag', () => {
    expect(dragValue(300, 500, 5000, 1, 180, 480)).toBe(480)
    expect(dragValue(300, 500, -5000, 1, 180, 480)).toBe(180)
  })
})

test('exported constants match the brief', () => {
  expect(RAIL_WIDTH).toBe(64)
  expect(MIN_FEED_WIDTH).toBe(360)
  expect(SIDEBAR_MIN).toBe(180)
  expect(SIDEBAR_MAX).toBe(480)
  expect(THREAD_MIN).toBe(320)
  expect(THREAD_DEFAULT).toBe(420)
  expect(SIDEBAR_DEFAULT).toBe(256)
})
