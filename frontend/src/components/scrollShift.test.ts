import type { Virtualizer, VirtualItem } from '@tanstack/react-virtual'
import { vi } from 'vitest'
import { ScrollShift, SCROLL_IDLE_MS } from './scrollShift'

type V = Virtualizer<HTMLDivElement, Element>

function setup() {
  const scroller = document.createElement('div')
  const sizer = document.createElement('div')
  scroller.appendChild(sizer)
  let top = 1000
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, get: () => top, set: (v: number) => { top = v } })
  const writes: number[] = []
  scroller.scrollTo = ((o: ScrollToOptions) => { writes.push(o.top!); top = o.top! }) as typeof scroller.scrollTo
  const rerender = vi.fn()
  const shift = new ScrollShift(() => scroller, () => sizer, rerender)
  const v = {
    scrollElement: scroller,
    scrollOffset: 1000 as number | null,
    scrollAdjustments: 0,
    scrollDirection: 'backward' as const,
    isScrolling: true,
    itemSizeCache: new Map<string, number>(),
    targetWindow: window,
    options: { horizontal: false, useScrollendEvent: false, isScrollingResetDelay: 150 },
  } as unknown as V
  shift.onUserInput()
  const item = (start: number, size: number, key = 'k'): VirtualItem => ({ key, index: 0, start, size, end: start + size, lane: 0 })
  return { scroller, sizer, shift, v, item, writes, rerender, top: () => top }
}

afterEach(() => vi.useRealTimers())

test('while idle, a row measured above the fold is compensated by the virtualizer as usual', () => {
  const { shift, v, item } = setup()
  v.isScrolling = false
  expect(shift.shouldAdjust(item(500, 64), 200, v)).toBe(true)
  expect(shift.shift).toBe(0)
})

test('mid-scroll, a row measured above the fold is compensated by shifting the rows, not by writing scrollTop', () => {
  const { shift, v, item, sizer, scroller, writes, rerender } = setup()
  expect(shift.shouldAdjust(item(500, 64), 200, v)).toBe(false)
  expect(shift.shouldAdjust(item(300, 28, 'k2'), 100, v)).toBe(false)
  expect(shift.shift).toBe(300)
  expect(v.scrollOffset).toBe(1300) // the virtualizer's offset is in row (layout) coordinates
  expect(rerender).toHaveBeenCalled()
  expect(writes).toEqual([])
  expect(scroller.scrollTop).toBe(1000)
  shift.apply() // after the commit that moved the rows below it
  expect(sizer.style.marginTop).toBe('-300px') // layout, not a transform: the scroll range shrinks with it
})

test('a scroll not driven by the user (script, history anchoring) is compensated at once, as usual', () => {
  const { v, item, rerender } = setup()
  const idle = new ScrollShift(() => v.scrollElement, () => null, rerender)
  expect(idle.shouldAdjust(item(500, 64), 200, v)).toBe(true) // isScrolling, but no wheel/touch/key
  expect(idle.shift).toBe(0)
})

test('the user gesture ends once scrolling has been idle', () => {
  vi.useFakeTimers()
  const { shift, v, item } = setup()
  shift.onScroll()
  vi.advanceTimersByTime(SCROLL_IDLE_MS)
  expect(shift.shouldAdjust(item(500, 64), 200, v)).toBe(true)
})

test('rows below the fold are never compensated', () => {
  const { shift, v, item } = setup()
  expect(shift.shouldAdjust(item(1200, 64), 200, v)).toBe(false)
  v.isScrolling = false
  expect(shift.shouldAdjust(item(1200, 64), 200, v)).toBe(false)
  expect(shift.shift).toBe(0)
})

test('an already-measured row is compensated only when entirely above the fold and not while scrolling up', () => {
  const { shift, v, item } = setup()
  v.itemSizeCache.set('k', 64)
  v.isScrolling = false
  expect(shift.shouldAdjust(item(950, 64), 10, v)).toBe(false) // spans the fold
  expect(shift.shouldAdjust(item(500, 64), 10, v)).toBe(false) // backward
  v.scrollDirection = 'forward'
  expect(shift.shouldAdjust(item(500, 64), 10, v)).toBe(true)
})

test('the shift lands in scrollTop once scrolling has been idle, in one step with the rows', () => {
  vi.useFakeTimers()
  const { shift, v, item, sizer, top } = setup()
  shift.shouldAdjust(item(500, 64), 200, v)
  shift.apply()
  shift.onScroll() // the gesture goes on: the flush waits
  vi.advanceTimersByTime(SCROLL_IDLE_MS - 1)
  expect(top()).toBe(1000)
  shift.onScroll()
  vi.advanceTimersByTime(SCROLL_IDLE_MS - 1)
  expect(top()).toBe(1000)
  vi.advanceTimersByTime(1)
  expect(top()).toBe(1200)
  expect(sizer.style.marginTop).toBe('')
  expect(shift.shift).toBe(0)
})

test('a shift taken after the last scroll event still lands when idle', () => {
  vi.useFakeTimers()
  const { shift, v, item, top } = setup()
  shift.shouldAdjust(item(500, 64), 200, v)
  vi.advanceTimersByTime(SCROLL_IDLE_MS)
  expect(top()).toBe(1200)
})

test('near the top the shift lands at once, so the first rows stay reachable', () => {
  const { shift, v, item, scroller, top } = setup()
  shift.shouldAdjust(item(500, 64), 200, v)
  scroller.scrollTop = 150
  shift.onScroll()
  expect(top()).toBe(350)
  expect(shift.shift).toBe(0)
})

test('a programmatic scroll lands the shift first, then scrolls in row coordinates', () => {
  const { shift, v, item, writes, sizer } = setup()
  shift.shouldAdjust(item(500, 64), 200, v)
  shift.apply()
  shift.scrollTo(5000, { adjustments: 0 }, v)
  expect(sizer.style.marginTop).toBe('')
  expect(shift.shift).toBe(0)
  expect(writes).toEqual([5000])
})

test('a programmatic scroll ends the gesture: later measurements are compensated at once', () => {
  const { shift, v, item } = setup()
  shift.shouldAdjust(item(500, 64), 200, v)
  shift.scrollTo(5000, { adjustments: 0 }, v)
  shift.onScroll() // its own scroll event does not start the gesture again
  expect(shift.shouldAdjust(item(400, 64, 'k2'), 100, v)).toBe(true)
  shift.onUserInput() // the next wheel does
  expect(shift.shouldAdjust(item(300, 64, 'k3'), 100, v)).toBe(false)
})

test('dispose before idle: the pending landing never runs on the gone feed', () => {
  vi.useFakeTimers()
  const { shift, v, item, top, writes } = setup()
  shift.shouldAdjust(item(500, 64), 200, v)
  shift.dispose()
  vi.advanceTimersByTime(SCROLL_IDLE_MS * 2)
  expect(top()).toBe(1000)
  expect(writes).toEqual([])
})

test('a programmatic scroll mid-gesture does not keep the gesture going past idle', () => {
  vi.useFakeTimers()
  const { shift, v, item } = setup()
  shift.shouldAdjust(item(500, 64), 200, v)
  shift.scrollTo(5000, { adjustments: 0 }, v)
  vi.advanceTimersByTime(SCROLL_IDLE_MS)
  expect(shift.shouldAdjust(item(500, 64), 200, v)).toBe(true)
})

test('the reported offset includes the pending shift', () => {
  const { shift, v, item, scroller } = setup()
  const seen: number[] = []
  const stop = shift.observeOffset(v, (o) => seen.push(o))
  shift.shouldAdjust(item(500, 64), 200, v)
  scroller.dispatchEvent(new Event('scroll'))
  expect(seen.at(-1)).toBe(1200)
  if (typeof stop === 'function') stop()
  shift.dispose()
})

test('the idle hook runs once a user gesture has gone idle, not after a programmatic scroll ended it', () => {
  vi.useFakeTimers()
  const scroller = document.createElement('div')
  const idle = vi.fn()
  const shift = new ScrollShift(() => scroller, () => null, vi.fn(), idle)
  shift.onUserInput()
  expect(shift.gesturing).toBe(true)
  vi.advanceTimersByTime(SCROLL_IDLE_MS)
  expect(shift.gesturing).toBe(false)
  expect(idle).toHaveBeenCalledTimes(1)
  shift.onScroll() // a scroll with no gesture: no second call
  vi.advanceTimersByTime(SCROLL_IDLE_MS)
  expect(idle).toHaveBeenCalledTimes(1)
})

test('absorb moves the content without writing scrollTop and lands once scrolling is idle', () => {
  vi.useFakeTimers()
  const { shift, sizer, scroller } = setup()
  shift.absorb(40)
  expect(sizer.style.marginTop).toBe('-40px')
  expect(scroller.scrollTop).toBe(1000)
  vi.advanceTimersByTime(SCROLL_IDLE_MS)
  expect(sizer.style.marginTop).toBe('')
  expect(scroller.scrollTop).toBe(1040)
  expect(shift.gesturing).toBe(false)
})
