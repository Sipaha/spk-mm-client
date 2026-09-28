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
// While the user scrolls (wheel, touch, scroll keys — until scrolling has
// been idle for SCROLL_IDLE_MS) the compensation goes into `shift` instead: the rows' container
// is translated up by it (after the commit that moved the rows — see apply),
// so nothing moves on screen and scrollTop is left to the animation. The
// virtualizer works in row (layout) coordinates = scrollTop + shift. Once
// scrolling has been idle for SCROLL_IDLE_MS, the shift lands in scrollTop in
// one step together with clearing the transform: no visible move, and no
// animation left to cancel. Any programmatic scroll (scrollToFn) lands it
// first, as does coming within `shift` of the top (rows there would be out
// of reach otherwise). Scrolls not driven by the user (history anchoring,
// scroll-to-end, script) keep the virtualizer's immediate compensation:
// nothing animates there, and a shift pending at the top would push a
// scroll to 0 back down on landing, stalling history loading.
export class ScrollShift {
  shift = 0
  private gesture = false // user input since scrolling was last idle
  private timer: ReturnType<typeof setTimeout> | undefined
  private scroller: () => HTMLElement | null
  private sizer: () => HTMLElement | null
  private rerender: () => void

  // rerender must commit the virtualizer's new row positions before the next
  // paint (the shift is applied in the same commit, see apply).
  constructor(scroller: () => HTMLElement | null, sizer: () => HTMLElement | null, rerender: () => void) {
    this.scroller = scroller
    this.sizer = sizer
    this.rerender = rerender
  }

  // For the virtualizer's observeElementOffset option.
  observeOffset = (instance: V, cb: (offset: number, isScrolling: boolean) => void) =>
    observeElementOffset(instance, (offset, isScrolling) => cb(offset + this.shift, isScrolling))

  // For the virtualizer's scrollToFn option: offsets are in row coordinates,
  // equal to scrollTop once the shift has landed.
  scrollTo = (offset: number, opts: { adjustments?: number; behavior?: ScrollBehavior }, instance: V) => {
    this.flush()
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

  // Writes the shift to the rows' container. Call after every commit: the
  // rows' positions and the shift must reach the screen together.
  apply() {
    const s = this.sizer()
    if (s) s.style.transform = this.shift ? `translateY(${-this.shift}px)` : ''
  }

  // Call on wheel, touchmove and scroll-key input on the feed.
  onUserInput() {
    this.gesture = true
    this.landWhenIdle()
  }

  // Call on every scroll event of the feed.
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
      this.gesture = false
      this.flush()
    }, SCROLL_IDLE_MS)
  }
}
