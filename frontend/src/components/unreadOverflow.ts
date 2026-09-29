import { useEffect, useRef, useState } from 'react'

export interface OverflowRow {
  id: string
  mentions: number
}

interface OverflowState {
  above: boolean
  below: boolean
  aboveMentions: boolean
  belowMentions: boolean
}

const initialState: OverflowState = { above: false, below: false, aboveMentions: false, belowMentions: false }

// useUnreadOverflow (sidebar-unread-brief.md ruling 2): tracks whether a
// countable unread/mention channel row is currently scrolled out of view
// above or below `scroller`, so Sidebar.tsx can show a "More unreads"/"More
// mentions" pill. `rows` is the *countable* set (unread or mentioned; muted
// channels excluded unless they carry a mention — ruling 1) in the order
// they're rendered — that order is also how scrollToAbove/scrollToBelow
// find the "nearest hidden row in that direction" without re-reading
// layout: among the rows currently flagged above, the last one in the array
// is the closest to the top of the viewport; among those flagged below, the
// first is the closest to the bottom.
//
// One IntersectionObserver rooted at `scroller` drives it. React state is
// only touched when the aggregated above/below/aboveMentions/belowMentions
// booleans actually change — a scroll that doesn't cross any row's boundary
// produces no re-render. jsdom does not implement IntersectionObserver, so
// this silently does nothing (pills never show) where it's undefined,
// rather than throwing — every other Sidebar test that doesn't stub it
// keeps working unmodified.
export function useUnreadOverflow(scroller: HTMLElement | null, rows: OverflowRow[]) {
  const [state, setState] = useState<OverflowState>(initialState)
  const elsRef = useRef(new Map<string, HTMLElement>())
  const infoRef = useRef(new Map<string, { above: boolean; below: boolean }>())
  const observerRef = useRef<IntersectionObserver | null>(null)
  const callbacksRef = useRef(new Map<string, (el: HTMLElement | null) => void>())
  const rowsRef = useRef(rows)
  rowsRef.current = rows

  const recompute = () => {
    let above = false
    let below = false
    let aboveMentions = false
    let belowMentions = false
    for (const r of rowsRef.current) {
      const info = infoRef.current.get(r.id)
      if (!info) continue
      if (info.above) {
        above = true
        if (r.mentions > 0) aboveMentions = true
      }
      if (info.below) {
        below = true
        if (r.mentions > 0) belowMentions = true
      }
    }
    setState((prev) =>
      prev.above === above && prev.below === below && prev.aboveMentions === aboveMentions && prev.belowMentions === belowMentions
        ? prev
        : { above, below, aboveMentions, belowMentions },
    )
  }

  useEffect(() => {
    if (!scroller || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          const id = elIdOf(entry.target, elsRef.current)
          if (!id) continue
          if (entry.isIntersecting) {
            infoRef.current.set(id, { above: false, below: false })
            continue
          }
          const root = entry.rootBounds
          const rect = entry.boundingClientRect
          infoRef.current.set(id, {
            above: !!root && rect.bottom <= root.top,
            below: !!root && rect.top >= root.bottom,
          })
        }
        recompute()
      },
      { root: scroller, threshold: 0 },
    )
    observerRef.current = observer
    for (const el of elsRef.current.values()) observer.observe(el)
    return () => {
      observer.disconnect()
      observerRef.current = null
    }
  }, [scroller])

  // Rows no longer countable (channel read/removed, its category collapsed)
  // must stop holding a pill open even if their element is never unmounted.
  useEffect(() => {
    const ids = new Set(rows.map((r) => r.id))
    let changed = false
    for (const id of infoRef.current.keys()) {
      if (!ids.has(id)) {
        infoRef.current.delete(id)
        changed = true
      }
    }
    if (changed) recompute()
  }, [rows])

  // Returns the *same* callback-ref function for a given id across renders
  // (cached in callbacksRef). Sidebar.tsx calls `overflow.rowRef(c.id)`
  // inline in JSX on every render — if that returned a fresh closure each
  // time, React would see the `ref` prop's identity change and run a
  // null-then-reattach cycle on the real DOM element on *every* render,
  // which deletes and re-adds the row's infoRef entry each time (wiping out
  // an above/below flag this same render just computed, since the observer
  // callback and the render that reads its result can interleave within one
  // `act()`/commit). Caching by id keeps the ref stable so React only calls
  // it when the row actually mounts/unmounts.
  function rowRef(id: string) {
    let cb = callbacksRef.current.get(id)
    if (!cb) {
      cb = (el: HTMLElement | null) => {
        const observer = observerRef.current
        const prev = elsRef.current.get(id)
        if (prev && prev !== el) {
          observer?.unobserve(prev)
          elsRef.current.delete(id)
          infoRef.current.delete(id)
        }
        if (el) {
          elsRef.current.set(id, el)
          observer?.observe(el)
        }
      }
      callbacksRef.current.set(id, cb)
    }
    return cb
  }

  function scrollToNearest(direction: 'above' | 'below') {
    const candidates = rowsRef.current.filter((r) => infoRef.current.get(r.id)?.[direction])
    const target = direction === 'above' ? candidates[candidates.length - 1] : candidates[0]
    if (!target) return
    elsRef.current.get(target.id)?.scrollIntoView?.({ behavior: 'smooth', block: 'nearest' })
  }

  return {
    ...state,
    rowRef,
    scrollToAbove: () => scrollToNearest('above'),
    scrollToBelow: () => scrollToNearest('below'),
  }
}

function elIdOf(target: EventTarget, els: Map<string, HTMLElement>): string | undefined {
  for (const [id, el] of els) {
    if (el === target) return id
  }
  return undefined
}
