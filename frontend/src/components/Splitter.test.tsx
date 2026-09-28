import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { Splitter } from './Splitter'

const CSS_VAR = '--spk-test-width'

function renderSplitter(over: Partial<Parameters<typeof Splitter>[0]> = {}) {
  const onCommit = vi.fn()
  const props = { value: 300, min: 180, max: 480, defaultValue: 256, sign: 1 as const, cssVar: CSS_VAR, label: 'Resize sidebar', onCommit, ...over }
  const r = render(<Splitter {...props} />)
  return { onCommit, props, rerender: (p: Partial<Parameters<typeof Splitter>[0]>) => r.rerender(<Splitter {...props} {...p} />) }
}

beforeEach(() => {
  document.documentElement.style.removeProperty(CSS_VAR)
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
