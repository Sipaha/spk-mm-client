import { elementScroll, observeElementOffset, type Virtualizer, type VirtualItem } from '@tanstack/react-virtual'

type V = Virtualizer<HTMLDivElement, Element>

// How long the feed must not scroll before a pending shift lands in
// scrollTop. Matches the virtualizer's own isScrolling reset delay.
export const SCROLL_IDLE_MS = 150

// ScrollShift keeps the feed's scroll compensation out of scrollTop while the
// feed is scrolling.
//
// A row measured above the fold for the first time (a picture post is
// estimated at 28–64 px but is ~300 px tall) pushes every row below it down;
// the virtualizer compensates by writing scrollTop += delta. WebKitGTK
// animates every wheel notch and cancels that animation on any scrollTop
// written by script, so each such write cut the notch short — uneven, jerky
// wheel steps exactly in channels with pictures and file cards.
//
// While the user scrolls (wheel or touch, until scrolling has been idle for
// SCROLL_IDLE_MS) the compensation goes into `shift` instead: the rows'
// container gets `margin-top: -shift` (after the commit that moved the rows —
// see apply), so nothing moves on screen and scrollTop is left to the
// animation. A margin, not a transform: it is layout, so the scroll range
// shrinks by `shift` with it and the last row stays at the very bottom of the
// range (a transform leaves scrollHeight as it was — blank space past the
// last row, and the at-bottom check off by `shift`). The virtualizer works in
// row (layout) coordinates = scrollTop + shift. Once scrolling has been idle
// for SCROLL_IDLE_MS, the shift lands in scrollTop in one step together with
// clearing the margin: no visible move, and no animation left to cancel.
// Coming within `shift` of the top lands it too (rows there would be out of
// reach otherwise).
//
// Any programmatic scroll (scrollToFn: history anchoring, scroll-to-end, a
// new post followed) lands the shift first and ends the gesture: WebKit has
// cancelled the wheel animation anyway, and from then on the virtualizer's
// immediate compensation applies until the next wheel/touch input — a shift
// pending at the top would otherwise push a scroll to 0 back down on landing
// and stall history loading. Keyboard scrolling is not covered (the feed is
// not focusable; keys reach it from wherever focus is) and keeps the
// immediate compensation.
export class ScrollShift {
  shift = 0
  private gesture = false // user input since scrolling was last idle
  private timer: ReturnType<typeof setTimeout> | undefined
  private scroller: () => HTMLElement | null
  private sizer: () => HTMLElement | null
  private rerender: () => void
  private idle: () => void

  // rerender must commit the virtualizer's new row positions before the next
  // paint (the shift is applied in the same commit, see apply). idle runs
  // once a user's gesture has ended (scrolling idle for SCROLL_IDLE_MS, the
  // shift landed) — the feed re-pins its bottom there if a resize came
  // mid-gesture.
  constructor(scroller: () => HTMLElement | null, sizer: () => HTMLElement | null, rerender: () => void, idle: () => void = () => {}) {
    this.scroller = scroller
    this.sizer = sizer
    this.rerender = rerender
    this.idle = idle
  }

  // A user's wheel/touch gesture is in progress: no script write to
  // scrollTop now (it would cancel WebKitGTK's wheel animation).
  get gesturing() {
    return this.gesture
  }

  // For the virtualizer's observeElementOffset option.
  observeOffset = (instance: V, cb: (offset: number, isScrolling: boolean) => void) =>
    observeElementOffset(instance, (offset, isScrolling) => cb(offset + this.shift, isScrolling))

  // For the virtualizer's scrollToFn option: offsets are in row coordinates,
  // equal to scrollTop once the shift has landed.
  scrollTo = (offset: number, opts: { adjustments?: number; behavior?: ScrollBehavior }, instance: V) => {
    this.flush()
    this.gesture = false
    elementScroll(offset, opts, instance)
  }

  // For virtualizer.shouldAdjustScrollPositionOnItemSizeChange. Same rule as
  // the virtualizer's default (@tanstack/virtual-core resizeItem): a first
  // measurement is compensated when the row starts above the fold, a
  // re-measurement only when the row is entirely above it and the feed is
  // not scrolling up. During a user's scroll it is taken into the shift.
  shouldAdjust = (item: VirtualItem, delta: number, instance: V): boolean => {
    const fold = (instance.scrollOffset ?? 0) + instance.scrollAdjustments
    const above = instance.itemSizeCache.has(item.key)
      ? item.end <= fold && instance.scrollDirection !== 'backward'
      : item.start < fold
    if (!above) return false
    if (!this.gesture || !instance.isScrolling) return true
    this.shift += delta
    // What the virtualizer does after its own compensating write: its offset
    // follows the rows at once, not at the next scroll event.
    if (instance.scrollOffset !== null) instance.scrollOffset += delta
    this.rerender()
    this.landWhenIdle()
    return false
  }

  // Moves the content by delta px (as scrollTop += delta would) without
  // writing scrollTop: for a correction that must happen mid-gesture (the
  // feed's pending history anchor held across a rows update). It lands with
  // the rest of the shift once scrolling is idle.
  absorb(delta: number) {
    this.shift += delta
    this.apply()
    this.landWhenIdle()
  }

  // Writes the shift to the rows' container. Call after every commit: the
  // rows' positions and the shift must reach the screen together.
  apply() {
    const s = this.sizer()
    if (s) s.style.marginTop = this.shift ? `${-this.shift}px` : ''
  }

  // Call on wheel and touchmove input on the feed.
  onUserInput() {
    this.gesture = true
    this.landWhenIdle()
  }

  // Call on every scroll event of the feed. Scroll events extend only a
  // gesture the user started (a programmatic scroll has ended it).
  onScroll() {
    if (!this.shift) {
      if (this.gesture) this.landWhenIdle()
      return
    }
    const el = this.scroller()
    if (el && el.scrollTop < Math.abs(this.shift)) this.flush()
    else this.landWhenIdle()
  }

  // Lands the shift in scrollTop now; nothing moves on screen.
  // The idle timer keeps running: it also ends the gesture.
  flush() {
    const s = this.shift
    if (!s) return
    this.shift = 0
    this.apply()
    const el = this.scroller()
    if (el) el.scrollTop += s
  }

  dispose() {
    clearTimeout(this.timer)
    this.timer = undefined
  }

  private landWhenIdle() {
    clearTimeout(this.timer)
    this.timer = setTimeout(() => {
      const ended = this.gesture
      this.gesture = false
      this.flush()
      if (ended) this.idle()
    }, SCROLL_IDLE_MS)
  }
}
