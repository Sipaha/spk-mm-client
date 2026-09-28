import { useRef, useState } from 'react'
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

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    drag.current = { startX: e.clientX, startWidth: valueRef.current }
    setDragging(true)
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
