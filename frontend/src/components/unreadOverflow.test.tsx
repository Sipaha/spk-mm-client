import { act, renderHook } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { useUnreadOverflow } from './unreadOverflow'

// A minimal, controllable stand-in for the real IntersectionObserver (not
// implemented by jsdom): captures the callback so tests can feed it
// synthetic entries, and records observe/unobserve/disconnect calls so
// cleanup can be asserted.
class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = []
  callback: IntersectionObserverCallback
  observed: Element[] = []
  disconnected = false
  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback
    FakeIntersectionObserver.instances.push(this)
  }
  observe(el: Element) {
    this.observed.push(el)
  }
  unobserve(el: Element) {
    this.observed = this.observed.filter((e) => e !== el)
  }
  disconnect() {
    this.disconnected = true
    this.observed = []
  }
  fire(entries: Partial<IntersectionObserverEntry>[]) {
    act(() => this.callback(entries as IntersectionObserverEntry[], this as unknown as IntersectionObserver))
  }
}

let realIO: typeof IntersectionObserver | undefined

beforeEach(() => {
  FakeIntersectionObserver.instances = []
  realIO = globalThis.IntersectionObserver
  globalThis.IntersectionObserver = FakeIntersectionObserver as unknown as typeof IntersectionObserver
})

afterEach(() => {
  globalThis.IntersectionObserver = realIO as typeof IntersectionObserver
})

const rootBounds = { top: 0, bottom: 300, left: 0, right: 200, width: 200, height: 300 } as DOMRectReadOnly

function above(target: Element): Partial<IntersectionObserverEntry> {
  return { target, isIntersecting: false, rootBounds, boundingClientRect: { top: -50, bottom: -10 } as DOMRectReadOnly }
}
function below(target: Element): Partial<IntersectionObserverEntry> {
  return { target, isIntersecting: false, rootBounds, boundingClientRect: { top: 320, bottom: 350 } as DOMRectReadOnly }
}
function visible(target: Element): Partial<IntersectionObserverEntry> {
  return { target, isIntersecting: true, rootBounds, boundingClientRect: { top: 10, bottom: 40 } as DOMRectReadOnly }
}

// Renders the hook against a real scroller div and three row buttons (a, b,
// c, in that order), attaching each row's ref via the hook's rowRef so the
// fake observer actually receives them.
function setup(rows: { id: string; mentions: number }[]) {
  const scroller = document.createElement('div')
  document.body.appendChild(scroller)
  const els = new Map<string, HTMLElement>()
  for (const r of rows) {
    const el = document.createElement('button')
    scroller.appendChild(el)
    els.set(r.id, el)
  }
  const view = renderHook(() => {
    const [node] = useState(scroller)
    return useUnreadOverflow(node, rows)
  })
  act(() => {
    for (const r of rows) view.result.current.rowRef(r.id)(els.get(r.id)!)
  })
  const observer = FakeIntersectionObserver.instances.at(-1)!
  return { view, els, observer, scroller }
}

test('no rows hidden: above/below stay false', () => {
  const { view } = setup([{ id: 'a', mentions: 0 }])
  expect(view.result.current.above).toBe(false)
  expect(view.result.current.below).toBe(false)
})

test('a row scrolled above the root becomes "above"; a mention on it flags aboveMentions', () => {
  const { view, els, observer } = setup([{ id: 'a', mentions: 2 }])
  observer.fire([above(els.get('a')!)])
  expect(view.result.current.above).toBe(true)
  expect(view.result.current.below).toBe(false)
  expect(view.result.current.aboveMentions).toBe(true)
})

test('a row scrolled below the root becomes "below", without mentions if it has none', () => {
  const { view, els, observer } = setup([{ id: 'a', mentions: 0 }])
  observer.fire([below(els.get('a')!)])
  expect(view.result.current.below).toBe(true)
  expect(view.result.current.belowMentions).toBe(false)
})

test('a row scrolling back into view clears its flag', () => {
  const { view, els, observer } = setup([{ id: 'a', mentions: 0 }])
  observer.fire([above(els.get('a')!)])
  expect(view.result.current.above).toBe(true)
  observer.fire([visible(els.get('a')!)])
  expect(view.result.current.above).toBe(false)
})

test('does not re-render when the aggregated booleans do not change', () => {
  const { view, els, observer } = setup([
    { id: 'a', mentions: 0 },
    { id: 'b', mentions: 0 },
  ])
  observer.fire([above(els.get('a')!)])
  const renders = view.result.current
  observer.fire([above(els.get('b')!)]) // still "above" overall — a was already above
  expect(view.result.current).toBe(renders)
})

test('scrollToAbove scrolls the nearest hidden-above row (last in document order among the hidden-above set)', () => {
  const { view, els, observer } = setup([
    { id: 'a', mentions: 0 },
    { id: 'b', mentions: 0 },
    { id: 'c', mentions: 0 },
  ])
  els.forEach((el) => {
    el.scrollIntoView = vi.fn()
  })
  observer.fire([above(els.get('a')!), above(els.get('b')!)])
  act(() => view.result.current.scrollToAbove())
  expect(els.get('b')!.scrollIntoView).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'smooth' }))
  expect(els.get('a')!.scrollIntoView).not.toHaveBeenCalled()
})

test('scrollToBelow scrolls the nearest hidden-below row (first in document order among the hidden-below set)', () => {
  const { view, els, observer } = setup([
    { id: 'a', mentions: 0 },
    { id: 'b', mentions: 0 },
    { id: 'c', mentions: 0 },
  ])
  els.forEach((el) => {
    el.scrollIntoView = vi.fn()
  })
  observer.fire([below(els.get('b')!), below(els.get('c')!)])
  act(() => view.result.current.scrollToBelow())
  expect(els.get('b')!.scrollIntoView).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'smooth' }))
  expect(els.get('c')!.scrollIntoView).not.toHaveBeenCalled()
})

test('scrollToAbove/Below is a no-op with nothing hidden in that direction', () => {
  const { view, els } = setup([{ id: 'a', mentions: 0 }])
  els.get('a')!.scrollIntoView = vi.fn()
  act(() => {
    view.result.current.scrollToAbove()
    view.result.current.scrollToBelow()
  })
  expect(els.get('a')!.scrollIntoView).not.toHaveBeenCalled()
})

test('rowRef(id) returns the same function across calls, so a consumer that calls it fresh every render does not cause React to null-then-reattach the row on each re-render', () => {
  const { view, els } = setup([{ id: 'a', mentions: 0 }])
  const first = view.result.current.rowRef('a')
  act(() => {
    /* a re-render, e.g. from an unrelated state change */
  })
  const second = view.result.current.rowRef('a')
  expect(second).toBe(first)
  expect(els.get('a')).toBeDefined()
})

test('unmount disconnects the observer', () => {
  const { view, observer } = setup([{ id: 'a', mentions: 0 }])
  view.unmount()
  expect(observer.disconnected).toBe(true)
})

test('a row dropped from `rows` (e.g. its category collapsed) can no longer hold a pill open', () => {
  const scroller = document.createElement('div')
  document.body.appendChild(scroller)
  const elA = document.createElement('button')
  scroller.appendChild(elA)
  const view = renderHook(({ rows }: { rows: { id: string; mentions: number }[] }) => useUnreadOverflow(scroller, rows), {
    initialProps: { rows: [{ id: 'a', mentions: 0 }] },
  })
  act(() => view.result.current.rowRef('a')(elA))
  const observer = FakeIntersectionObserver.instances.at(-1)!
  observer.fire([above(elA)])
  expect(view.result.current.above).toBe(true)
  view.rerender({ rows: [] })
  expect(view.result.current.above).toBe(false)
})
