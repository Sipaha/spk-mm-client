import { fitCount, TOOLBAR_ITEM_WIDTH, TOOLBAR_MORE_WIDTH, TOOLBAR_SEPARATOR_WIDTH, widthForCount } from './composerToolbarFit'

describe('widthForCount', () => {
  test('no separators: count times the item width', () => {
    expect(widthForCount(3, [])).toBe(3 * TOOLBAR_ITEM_WIDTH)
  })

  test('a separator before an index only counts once that button is shown', () => {
    expect(widthForCount(4, [4])).toBe(4 * TOOLBAR_ITEM_WIDTH) // button 4 not shown yet
    expect(widthForCount(5, [4])).toBe(5 * TOOLBAR_ITEM_WIDTH + TOOLBAR_SEPARATOR_WIDTH) // button 4 now shown
  })

  test('two separators, matching the composer toolbar (breaks before index 4 and 7)', () => {
    expect(widthForCount(9, [4, 7])).toBe(9 * TOOLBAR_ITEM_WIDTH + 2 * TOOLBAR_SEPARATOR_WIDTH)
  })
})

describe('fitCount', () => {
  const BREAKS = [4, 7]
  const FULL = widthForCount(9, BREAKS) // 288

  test('everything fits: all 9, no reserved "more" button needed', () => {
    expect(fitCount(FULL, 9, BREAKS)).toBe(9)
    expect(fitCount(FULL + 100, 9, BREAKS)).toBe(9)
  })

  test('just short of the full width: falls back to however many fit alongside the "more" button', () => {
    // 8 buttons + 2 separators + the "more" button no longer fits either
    // (needs FULL, i.e. as much as all 9) — so it drops to 7.
    expect(fitCount(FULL - 1, 9, BREAKS)).toBe(7)
  })

  test('exactly enough for 7 plus the "more" button', () => {
    const w7 = widthForCount(7, BREAKS) + TOOLBAR_MORE_WIDTH
    expect(fitCount(w7, 9, BREAKS)).toBe(7)
    expect(fitCount(w7 - 1, 9, BREAKS)).toBeLessThan(7)
  })

  test('too small for anything: 0', () => {
    expect(fitCount(0, 9, BREAKS)).toBe(0)
    expect(fitCount(TOOLBAR_ITEM_WIDTH - 1, 9, BREAKS)).toBe(0)
  })

  test('negative/zero available never throws or returns negative', () => {
    expect(fitCount(-50, 9, BREAKS)).toBe(0)
  })
})
