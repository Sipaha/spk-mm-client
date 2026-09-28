import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { TextView } from './TextView'

const log: FileView = { id: 'f-log', name: 'server.log', ext: 'log', size: 70000, mime: 'text/plain' }

beforeEach(() => setLocale('en'))
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

function stubFetch(text: string, truncated = false) {
  const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () => new Response(text, { headers: truncated ? { 'X-Truncated': '1' } : {} }))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

test('requests the text with ?full=1, not the feed-fragment URL', async () => {
  const fetchMock = stubFetch('hello world')
  render(<TextView serverId={1} file={log} />)
  await screen.findByText('hello world')
  expect(fetchMock).toHaveBeenCalledTimes(1)
  const url = fetchMock.mock.calls[0][0]
  expect(url).toBe('/media/1/text/f-log?full=1')
})

test('the panel spans the full width: no max-w-5xl anywhere in it', async () => {
  stubFetch('hello world')
  const { container } = render(<TextView serverId={1} file={log} />)
  await screen.findByText('hello world')
  expect(container.querySelector('.max-w-5xl')).toBeNull()
})

test('the truncation notice uses the 1 MB i18n key, not a hard-coded 64 KB', async () => {
  stubFetch('hello world', true)
  render(<TextView serverId={1} file={log} />)
  expect(await screen.findByText('Showing the first 1 MB')).toBeInTheDocument()
  expect(screen.queryByText(/64 KB/)).toBeNull()
})

test('search: counter, Enter/Shift+Enter cycle through matches, current match is marked', async () => {
  vi.useFakeTimers()
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  stubFetch('one two one three one')
  render(<TextView serverId={1} file={log} />)
  await act(async () => vi.advanceTimersByTime(0))
  const box = screen.getByRole('textbox', { name: 'Search in file' })
  await user.type(box, 'one')
  await act(async () => vi.advanceTimersByTime(150))
  expect(await screen.findByText('1 of 3')).toBeInTheDocument()
  let marks = document.querySelectorAll('mark')
  expect(marks).toHaveLength(3)
  expect(marks[0]).toHaveAttribute('data-current', 'true')

  await user.type(box, '{Enter}')
  expect(screen.getByText('2 of 3')).toBeInTheDocument()
  marks = document.querySelectorAll('mark')
  expect(marks[1]).toHaveAttribute('data-current', 'true')

  await user.type(box, '{Shift>}{Enter}{/Shift}')
  expect(screen.getByText('1 of 3')).toBeInTheDocument() // back to the first

  // next from the first wraps forward, previous from the first wraps to the last
  await user.type(box, '{Shift>}{Enter}{/Shift}')
  expect(screen.getByText('3 of 3')).toBeInTheDocument()
})

test('search is case-insensitive substring, matching is honest about a 5000+ cap', async () => {
  vi.useFakeTimers()
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  stubFetch('a'.repeat(6000))
  render(<TextView serverId={1} file={log} />)
  await act(async () => vi.advanceTimersByTime(0))
  const box = screen.getByRole('textbox', { name: 'Search in file' })
  await user.type(box, 'A')
  await act(async () => vi.advanceTimersByTime(150))
  expect(await screen.findByText('1 of 5000+')).toBeInTheDocument()
  expect(document.querySelectorAll('mark')).toHaveLength(5000)
})

test('Escape with text in the search field clears it instead of bubbling up, right away (no 150 ms wait)', async () => {
  vi.useFakeTimers()
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  stubFetch('one two one')
  const onWindowEscape = vi.fn()
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') onWindowEscape()
  })
  render(<TextView serverId={1} file={log} />)
  await act(async () => vi.advanceTimersByTime(0))
  const box = screen.getByRole('textbox', { name: 'Search in file' })
  await user.type(box, 'one')
  await act(async () => vi.advanceTimersByTime(150))
  await screen.findByText('1 of 2')
  await user.type(box, '{Escape}')
  expect(box).toHaveValue('')
  expect(onWindowEscape).not.toHaveBeenCalled()
  // Cleared immediately: no lingering highlights/counter without advancing
  // the debounce timer at all.
  expect(screen.queryByText('1 of 2')).toBeNull()
  expect(document.querySelectorAll('mark')).toHaveLength(0)
})

test('a length-expanding case fold (İ → i + combining dot) does not misalign later matches', async () => {
  vi.useFakeTimers()
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  // 'İ'.toLowerCase() is two UTF-16 units ('i' + U+0307); a naive
  // text.toLowerCase() used only for scanning would shift every offset
  // after it by one, misaligning the slice taken from the original text.
  const text = 'İ one two one'
  stubFetch(text)
  render(<TextView serverId={1} file={log} />)
  await act(async () => vi.advanceTimersByTime(0))
  const box = screen.getByRole('textbox', { name: 'Search in file' })
  await user.type(box, 'one')
  await act(async () => vi.advanceTimersByTime(150))
  expect(await screen.findByText('1 of 2')).toBeInTheDocument()
  const marks = document.querySelectorAll('mark')
  expect(marks).toHaveLength(2)
  for (const m of marks) expect(m.textContent).toBe('one')
})

test('Escape in an empty search field is not intercepted (lets the viewer close as before)', async () => {
  stubFetch('one two one')
  const onWindowEscape = vi.fn()
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') onWindowEscape()
  })
  render(<TextView serverId={1} file={log} />)
  const box = screen.getByRole('textbox', { name: 'Search in file' })
  box.focus()
  await userEvent.keyboard('{Escape}')
  expect(onWindowEscape).toHaveBeenCalledTimes(1)
})

test('arrow keys typed in the search field do not bubble up to page files', async () => {
  stubFetch('one two one')
  const onWindowArrow = vi.fn()
  window.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') onWindowArrow()
  })
  render(<TextView serverId={1} file={log} />)
  const box = screen.getByRole('textbox', { name: 'Search in file' })
  box.focus()
  await userEvent.keyboard('{ArrowRight}{ArrowLeft}')
  expect(onWindowArrow).not.toHaveBeenCalled()
})

test('Ctrl+F focuses the search field from anywhere in the viewer', async () => {
  stubFetch('hello world')
  render(
    <>
      <button>elsewhere</button>
      <TextView serverId={1} file={log} />
    </>,
  )
  screen.getByRole('button', { name: 'elsewhere' }).focus()
  const box = screen.getByRole('textbox', { name: 'Search in file' })
  expect(box).not.toHaveFocus()
  await userEvent.keyboard('{Control>}{f}{/Control}')
  expect(box).toHaveFocus()
})

test('Ctrl+F works on a Russian keyboard layout (key is "а", not "f" — matched by the physical key instead)', async () => {
  stubFetch('hello world')
  render(
    <>
      <button>elsewhere</button>
      <TextView serverId={1} file={log} />
    </>,
  )
  screen.getByRole('button', { name: 'elsewhere' }).focus()
  const box = screen.getByRole('textbox', { name: 'Search in file' })
  expect(box).not.toHaveFocus()
  fireEvent.keyDown(window, { key: 'а', code: 'KeyF', ctrlKey: true })
  expect(box).toHaveFocus()
})
