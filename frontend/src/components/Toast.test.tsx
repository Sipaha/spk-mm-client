import { act, fireEvent, render, screen } from '@testing-library/react'
import { vi } from 'vitest'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { Toast, TOAST_MS } from './Toast'

beforeEach(() => {
  setLocale('en')
  useStore.setState({ toast: null })
})
afterEach(() => vi.useRealTimers())

test('a floating, polite live region that is out of the layout flow even when empty', () => {
  render(<Toast />)
  const region = screen.getByTestId('toast-region')
  expect(region).toHaveAttribute('aria-live', 'polite')
  expect(region.className).toMatch(/\bfixed\b/)
  expect(region).toBeEmptyDOMElement()
})

test('shows the message, dismisses on its button, and on its own after TOAST_MS', () => {
  vi.useFakeTimers()
  render(<Toast />)
  act(() => useStore.getState().showToast('Could not download a.zip: boom'))
  expect(screen.getByText('Could not download a.zip: boom')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
  expect(screen.queryByText(/Could not download/)).not.toBeInTheDocument()

  act(() => useStore.getState().showToast('second'))
  act(() => void vi.advanceTimersByTime(TOAST_MS - 100))
  expect(screen.getByText('second')).toBeInTheDocument()
  act(() => void vi.advanceTimersByTime(200))
  expect(screen.queryByText('second')).not.toBeInTheDocument()
})

test('a newer toast gets its own full time', () => {
  vi.useFakeTimers()
  render(<Toast />)
  act(() => useStore.getState().showToast('one'))
  act(() => void vi.advanceTimersByTime(TOAST_MS - 1000))
  act(() => useStore.getState().showToast('two'))
  act(() => void vi.advanceTimersByTime(2000))
  expect(screen.getByText('two')).toBeInTheDocument()
})
