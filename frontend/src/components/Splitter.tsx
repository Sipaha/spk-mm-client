import { useEffect, useRef, useState } from 'react'
import { dragValue, stepValue, type StepKey } from './splitter'

interface Props {
  value: number
  min: number
  max: number
  defaultValue: number
  /** +1: the handle is the pane's right edge (sidebar) — dragging right grows it.
   *  -1: the handle is the pane's left edge (thread panel) — dragging right shrinks it. */
  sign: 1 | -1
  /** CSS custom property this splitter drives live (set on document.documentElement). */
  cssVar: string
  label: string
  /** Called once per commit: pointerup, a keyboard step, or a double-click reset — never per drag frame. */
  onCommit(width: number): void
}

const STEP_KEYS: StepKey[] = ['ArrowLeft', 'ArrowRight', 'Home', 'End']

// Splitter: a draggable, keyboard-resizable divider (theme brief 2026-09-28,
// scope 3a). During a drag it writes only a CSS variable (via
// requestAnimationFrame, not React state) so dragging never re-renders the
// panes it resizes; the committed width (state + persistence) lands once,
// on pointerup. Pointer capture keeps the drag alive even if the cursor
// leaves the window.
export function Splitter({ value, min, max, defaultValue, sign, cssVar, label, onCommit }: Props) {
  const [dragging, setDragging] = useState(false)
  const drag = useRef<{ startX: number; startWidth: number } | null>(null)
  const raf = useRef<number | null>(null)
  const pending = useRef<number | null>(null)
  const valueRef = useRef(value)
  valueRef.current = value

  const setLive = (w: number) => {
    document.documentElement.style.setProperty(cssVar, `${w}px`)
  }

  const schedule = (w: number) => {
    pending.current = w
    if (raf.current != null) return
    raf.current = requestAnimationFrame(() => {
      raf.current = null
      if (pending.current != null) setLive(pending.current)
    })
  }

  const cancelFrame = () => {
    if (raf.current != null) cancelAnimationFrame(raf.current)
    raf.current = null
    pending.current = null
  }

  // prevUserSelect: document.documentElement's user-select before the drag
  // started, restored on pointerup/cancel/unmount instead of just clearing
  // it outright — in case something outside this component ever sets its
  // own value there too.
  const prevUserSelect = useRef<string | null>(null)

  // disableSelection/restoreSelection: pointer capture only affects which
  // element pointer *events* target, not the browser's own text-selection
  // behaviour, which tracks the cursor over whatever it's really over — a
  // drag that crosses the feed was selecting its messages (coordinator
  // report 2026-09-29, seen in the fix's own after-screenshot). Applying
  // user-select:none document-wide for the duration of the drag is the
  // standard fix for exactly this class of bug in resizable-pane widgets.
  const disableSelection = () => {
    prevUserSelect.current = document.documentElement.style.userSelect
    document.documentElement.style.userSelect = 'none'
  }
  const restoreSelection = () => {
    if (prevUserSelect.current != null) {
      document.documentElement.style.userSelect = prevUserSelect.current
      prevUserSelect.current = null
    }
    // preventDefault on pointerdown (below) stops a *new* selection from
    // starting, but a selection already in progress from an earlier event
    // is cleared explicitly — belt and braces, and it's what the
    // coordinator asked to verify.
    window.getSelection()?.removeAllRanges()
  }

  // Unmounting mid-drag (e.g. the thread panel closes, or the server/channel
  // switches under it) must not leave a scheduled frame behind — it would
  // fire after this Splitter instance is gone and write a stale width into
  // the (still-live, document-level) CSS var, with no pointerup left to
  // correct it (review follow-up 2026-09-29) — nor leave user-select:none
  // stuck on the whole document with no pointerup left to lift it.
  useEffect(
    () => () => {
      cancelFrame()
      if (drag.current) restoreSelection()
    },
    [],
  )

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    // Stops the browser from starting its own text selection right from
    // this event, on top of the document-wide user-select:none below (which
    // covers the rest of the drag, since preventDefault here only concerns
    // this one event). Side effect: preventDefault on pointerdown also
    // suppresses the implicit mousedown focus a click would otherwise give
    // this (tabIndex=0) element, so it's restored explicitly right after —
    // a drag must keep the usual keyboard-focus behaviour (arrow-key nudges
    // right after releasing the pointer, same as before this fix).
    e.preventDefault()
    e.currentTarget.focus()
    drag.current = { startX: e.clientX, startWidth: valueRef.current }
    setDragging(true)
    disableSelection()
    try {
      e.currentTarget.setPointerCapture(e.pointerId)
    } catch {
      // unsupported (e.g. jsdom in tests) — the drag still works via the
      // document-level listeners the pointer already targets.
    }
  }

  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!drag.current) return
    schedule(dragValue(drag.current.startWidth, drag.current.startX, e.clientX, sign, min, max))
  }

  const endDrag = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!drag.current) return
    const w = dragValue(drag.current.startWidth, drag.current.startX, e.clientX, sign, min, max)
    drag.current = null
    setDragging(false)
    cancelFrame()
    restoreSelection()
    setLive(w)
    onCommit(w)
    try {
      e.currentTarget.releasePointerCapture(e.pointerId)
    } catch {
      // see onPointerDown
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!STEP_KEYS.includes(e.key as StepKey)) return
    e.preventDefault()
    const w = stepValue(valueRef.current, e.key as StepKey, min, max)
    setLive(w)
    onCommit(w)
  }

  const onDoubleClick = () => {
    setLive(defaultValue)
    onCommit(defaultValue)
  }

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={Math.round(value)}
      aria-valuemin={min}
      aria-valuemax={max}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onKeyDown={onKeyDown}
      onDoubleClick={onDoubleClick}
      className="group relative z-10 w-[5px] shrink-0 cursor-col-resize touch-none select-none focus:outline-none"
    >
      <div
        aria-hidden="true"
        className={`absolute inset-y-0 left-1/2 w-px -translate-x-1/2 ${
          dragging ? 'bg-accent' : 'bg-line group-hover:bg-accent group-focus-visible:bg-accent'
        }`}
      />
    </div>
  )
}
