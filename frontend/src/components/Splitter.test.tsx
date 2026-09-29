import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { Splitter } from './Splitter'

const CSS_VAR = '--spk-test-width'

function renderSplitter(over: Partial<Parameters<typeof Splitter>[0]> = {}) {
  const onCommit = vi.fn()
  const props = { value: 300, min: 180, max: 480, defaultValue: 256, sign: 1 as const, cssVar: CSS_VAR, label: 'Resize sidebar', onCommit, ...over }
  const r = render(<Splitter {...props} />)
  return {
    onCommit,
    props,
    unmount: r.unmount,
    rerender: (p: Partial<Parameters<typeof Splitter>[0]>) => r.rerender(<Splitter {...props} {...p} />),
  }
}

beforeEach(() => {
  document.documentElement.style.removeProperty(CSS_VAR)
  document.documentElement.style.removeProperty('user-select')
  window.getSelection()?.removeAllRanges()
  // Run rAF callbacks synchronously so a pointermove's live update is
  // observable without a real frame tick.
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
    cb(0)
    return 0
  })
})

afterEach(() => vi.restoreAllMocks())

test('exposes the separator role and aria attributes', () => {
  renderSplitter()
  const sep = screen.getByRole('separator', { name: 'Resize sidebar' })
  expect(sep).toHaveAttribute('aria-orientation', 'vertical')
  expect(sep).toHaveAttribute('aria-valuenow', '300')
  expect(sep).toHaveAttribute('aria-valuemin', '180')
  expect(sep).toHaveAttribute('aria-valuemax', '480')
  expect(sep).toHaveAttribute('tabindex', '0')
})

test('ArrowRight/ArrowLeft step by 16px and commit at once', async () => {
  const { onCommit, rerender } = renderSplitter()
  const sep = screen.getByRole('separator')
  sep.focus()
  await userEvent.keyboard('{ArrowRight}')
  expect(onCommit).toHaveBeenLastCalledWith(316)
  expect(document.documentElement.style.getPropertyValue(CSS_VAR)).toBe('316px')
  rerender({ value: 316 }) // a real caller re-renders with the committed value
  await userEvent.keyboard('{ArrowLeft}')
  expect(onCommit).toHaveBeenLastCalledWith(300)
})

test('Home/End jump to min/max', async () => {
  const { onCommit } = renderSplitter()
  screen.getByRole('separator').focus()
  await userEvent.keyboard('{Home}')
  expect(onCommit).toHaveBeenLastCalledWith(180)
  await userEvent.keyboard('{End}')
  expect(onCommit).toHaveBeenLastCalledWith(480)
})

test('keyboard step clamps at the bounds', async () => {
  const { onCommit } = renderSplitter({ value: 470 })
  screen.getByRole('separator').focus()
  await userEvent.keyboard('{ArrowRight}')
  expect(onCommit).toHaveBeenLastCalledWith(480)
})

test('double-click resets to the default width', async () => {
  const { onCommit } = renderSplitter({ value: 400 })
  await userEvent.dblClick(screen.getByRole('separator'))
  expect(onCommit).toHaveBeenCalledWith(256)
  expect(document.documentElement.style.getPropertyValue(CSS_VAR)).toBe('256px')
})

test('drag: sign +1 grows the pane to the right, writes the CSS var live, commits once on pointerup', () => {
  const { onCommit } = renderSplitter({ value: 300, sign: 1 })
  const sep = screen.getByRole('separator')
  fireEvent.pointerDown(sep, { clientX: 500, button: 0, pointerId: 1 })
  fireEvent.pointerMove(sep, { clientX: 540, pointerId: 1 })
  // Live update happened without any commit yet.
  expect(document.documentElement.style.getPropertyValue(CSS_VAR)).toBe('340px')
  expect(onCommit).not.toHaveBeenCalled()
  fireEvent.pointerUp(sep, { clientX: 560, pointerId: 1 })
  expect(onCommit).toHaveBeenCalledTimes(1)
  expect(onCommit).toHaveBeenCalledWith(360)
  expect(document.documentElement.style.getPropertyValue(CSS_VAR)).toBe('360px')
})

test('drag: sign -1 (thread panel) shrinks the pane when dragging right', () => {
  const { onCommit } = renderSplitter({ value: 420, min: 320, max: 800, sign: -1 })
  const sep = screen.getByRole('separator')
  fireEvent.pointerDown(sep, { clientX: 500, button: 0, pointerId: 1 })
  fireEvent.pointerUp(sep, { clientX: 560, pointerId: 1 })
  expect(onCommit).toHaveBeenCalledWith(360)
})

test('drag clamps at the bounds', () => {
  const { onCommit } = renderSplitter({ value: 300, min: 180, max: 480 })
  const sep = screen.getByRole('separator')
  fireEvent.pointerDown(sep, { clientX: 0, button: 0, pointerId: 1 })
  fireEvent.pointerUp(sep, { clientX: 5000, pointerId: 1 })
  expect(onCommit).toHaveBeenCalledWith(480)
})

test('a non-primary pointer button does not start a drag', () => {
  const { onCommit } = renderSplitter()
  const sep = screen.getByRole('separator')
  fireEvent.pointerDown(sep, { clientX: 500, button: 2, pointerId: 1 })
  fireEvent.pointerMove(sep, { clientX: 600, pointerId: 1 })
  fireEvent.pointerUp(sep, { clientX: 600, pointerId: 1 })
  expect(onCommit).not.toHaveBeenCalled()
})

test('a drag disables text selection document-wide and restores it on pointerup (coordinator report 2026-09-29)', () => {
  renderSplitter()
  const sep = screen.getByRole('separator')
  expect(document.documentElement.style.userSelect).toBe('')

  fireEvent.pointerDown(sep, { clientX: 500, button: 0, pointerId: 1 })
  expect(document.documentElement.style.userSelect).toBe('none')

  fireEvent.pointerMove(sep, { clientX: 540, pointerId: 1 })
  expect(document.documentElement.style.userSelect).toBe('none') // still disabled mid-drag

  fireEvent.pointerUp(sep, { clientX: 560, pointerId: 1 })
  expect(document.documentElement.style.userSelect).toBe('')
})

test('a drag that started a text selection anyway clears it on pointerup', () => {
  renderSplitter()
  const sep = screen.getByRole('separator')
  const text = document.createElement('p')
  text.textContent = 'some feed message text'
  document.body.appendChild(text)

  fireEvent.pointerDown(sep, { clientX: 500, button: 0, pointerId: 1 })
  // A drag can still leave a stray selection behind even with user-select:
  // none applied after the fact (e.g. a selection that started a frame
  // earlier) -- the drag must clear it regardless, not just prevent new ones.
  const range = document.createRange()
  range.selectNodeContents(text)
  window.getSelection()!.removeAllRanges()
  window.getSelection()!.addRange(range)
  expect(window.getSelection()!.toString()).not.toBe('')

  fireEvent.pointerMove(sep, { clientX: 540, pointerId: 1 })
  fireEvent.pointerUp(sep, { clientX: 560, pointerId: 1 })

  expect(window.getSelection()!.toString()).toBe('')
  text.remove()
})

test('pointerdown on the separator prevents the default action (so a drag never starts a text selection in the first place)', () => {
  renderSplitter()
  const sep = screen.getByRole('separator')
  // dispatchEvent (which fireEvent wraps) returns false when the event was
  // canceled via preventDefault — a listener added directly on `sep` would
  // run in the DOM's target phase, before React's own handler further up
  // the tree even fires, so it can't observe the eventual defaultPrevented
  // flag; the dispatch's own return value can.
  const notCanceled = fireEvent.pointerDown(sep, { clientX: 500, button: 0, pointerId: 1, cancelable: true })
  expect(notCanceled).toBe(false)
})

test('a drag still focuses the separator, same as a plain click would (preventDefault on pointerdown would otherwise silently suppress it)', () => {
  renderSplitter()
  const sep = screen.getByRole('separator')
  expect(document.activeElement).not.toBe(sep)

  fireEvent.pointerDown(sep, { clientX: 500, button: 0, pointerId: 1 })
  expect(document.activeElement).toBe(sep)

  fireEvent.pointerMove(sep, { clientX: 540, pointerId: 1 })
  fireEvent.pointerUp(sep, { clientX: 560, pointerId: 1 })
  expect(document.activeElement).toBe(sep) // still focused once the drag ends
})

test('a pending rAF frame from an in-progress drag is canceled on unmount (review follow-up)', () => {
  // Override the module-wide synchronous rAF mock: this test needs the frame
  // to stay pending (never auto-invoked) so there's still something to cancel.
  vi.spyOn(window, 'requestAnimationFrame').mockReturnValue(42)
  const cancelSpy = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {})

  const { unmount } = renderSplitter()
  const sep = screen.getByRole('separator')
  fireEvent.pointerDown(sep, { clientX: 500, button: 0, pointerId: 1 })
  fireEvent.pointerMove(sep, { clientX: 540, pointerId: 1 }) // schedules the (never-run) frame 42

  unmount()

  expect(cancelSpy).toHaveBeenCalledWith(42)
})

test('unmounting mid-drag restores text selection too, not just the rAF frame', () => {
  const { unmount } = renderSplitter()
  const sep = screen.getByRole('separator')
  fireEvent.pointerDown(sep, { clientX: 500, button: 0, pointerId: 1 })
  expect(document.documentElement.style.userSelect).toBe('none')

  unmount()

  expect(document.documentElement.style.userSelect).toBe('')
})
