import { act, fireEvent, render, screen } from '@testing-library/react'
import { useRef } from 'react'
import { vi } from 'vitest'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { Announcer, Toast, TOAST_HOST, TOAST_MS, useToastHost, type HostPlace } from './Toast'

beforeEach(() => {
  setLocale('en')
  useStore.setState({ toast: null })
})
afterEach(() => vi.useRealTimers())

// Hosts: the toast is one component (App) rendered into the best host there
// is — the open file viewer, else the rightmost feed (thread panel over
// channel), else a fixed box over the window (nothing mounted yet).
function Host({ id, priority, place }: { id: string; priority: number; place: HostPlace }) {
  const ref = useRef<HTMLDivElement>(null)
  useToastHost(ref, priority, place)
  return <div data-testid={id} ref={ref} />
}

test('no host mounted (the add-server screen, a channel still loading): a fixed box over the window', () => {
  render(<Toast />)
  const region = screen.getByTestId('toast-region')
  expect(region).toHaveAttribute('aria-live', 'polite')
  expect(region).toHaveClass('fixed')
  expect(region).toBeEmptyDOMElement()
})

test('in the feed: absolute in its box, above the composer and left of the jump-to-latest button', () => {
  render(
    <>
      <Host id="feed" priority={TOAST_HOST.channel} place="feed" />
      <Toast />
    </>,
  )
  const region = screen.getByTestId('toast-region')
  expect(screen.getByTestId('feed')).toContainElement(region)
  // right-16 clears the jump button (right-5, 36 px wide)
  expect(region).toHaveClass('absolute', 'bottom-4', 'right-16')
})

test('the open viewer wins over the feeds, the thread panel over the channel; always exactly one live region', () => {
  const { rerender } = render(
    <>
      <Host id="channel" priority={TOAST_HOST.channel} place="feed" />
      <Host id="thread" priority={TOAST_HOST.thread} place="feed" />
      <Host id="viewer" priority={TOAST_HOST.viewer} place="viewer" />
      <Toast />
    </>,
  )
  act(() => useStore.getState().showToast('Could not download a.png: boom'))
  expect(screen.getAllByTestId('toast-region')).toHaveLength(1)
  expect(screen.getByTestId('viewer')).toContainElement(screen.getByText(/Could not download/))
  rerender(
    <>
      <Host id="channel" priority={TOAST_HOST.channel} place="feed" />
      <Host id="thread" priority={TOAST_HOST.thread} place="feed" />
      <Toast />
    </>,
  )
  expect(screen.getByTestId('thread')).toContainElement(screen.getByText(/Could not download/))
  rerender(
    <>
      <Host id="channel" priority={TOAST_HOST.channel} place="feed" />
      <Toast />
    </>,
  )
  expect(screen.getAllByTestId('toast-region')).toHaveLength(1)
  expect(screen.getByTestId('channel')).toContainElement(screen.getByText(/Could not download/))
})

test('moving to another host keeps the remaining time (no fresh 6 s)', () => {
  vi.useFakeTimers()
  const { rerender } = render(
    <>
      <Host id="channel" priority={TOAST_HOST.channel} place="feed" />
      <Toast />
    </>,
  )
  act(() => useStore.getState().showToast('moving'))
  act(() => void vi.advanceTimersByTime(TOAST_MS - 1000))
  rerender(
    <>
      <Host id="other" priority={TOAST_HOST.channel} place="feed" />
      <Toast />
    </>,
  ) // a channel switch: the old feed goes, a new one comes
  expect(screen.getByTestId('other')).toContainElement(screen.getByText('moving'))
  act(() => void vi.advanceTimersByTime(1100))
  expect(screen.queryByText('moving')).not.toBeInTheDocument()
})

test('over a feed the toast lets the pointer through (the last post\'s toolbar under it stays usable), except its close button', () => {
  render(
    <>
      <Host id="feed" priority={TOAST_HOST.channel} place="feed" />
      <Toast />
    </>,
  )
  act(() => useStore.getState().showToast('x'))
  const card = screen.getByText('x').closest('[data-tone]')!
  expect(card).toHaveClass('pointer-events-none')
  expect(card).not.toHaveClass('pointer-events-auto')
  expect(screen.getByRole('button', { name: 'Dismiss' })).toHaveClass('pointer-events-auto')
})

test('a finished download is announced politely in a visually hidden region', () => {
  render(<Announcer />)
  const region = screen.getByTestId('announcer')
  expect(region).toHaveAttribute('aria-live', 'polite')
  expect(region).toHaveClass('sr-only')
  act(() => useStore.getState().announce('File saved: a.zip'))
  expect(region).toHaveTextContent('File saved: a.zip')
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
