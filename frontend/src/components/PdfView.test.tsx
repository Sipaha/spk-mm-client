import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'

// pdf.js is entirely mocked — these tests are about PdfView's own
// windowing/zoom/release logic, not pdf.js's rendering. `getPage` is a spy
// so tests can assert which page numbers were actually asked for.
const getPage = vi.fn(async (i: number) => makePage(i))
const destroy = vi.fn(async () => {})
let numPages = 5

function makePage(i: number) {
  return {
    getViewport: ({ scale }: { scale: number }) => ({ width: 100 * scale, height: 100 * scale }),
    render: vi.fn(() => ({ promise: Promise.resolve(), cancel: vi.fn() })),
    streamTextContent: vi.fn(() => ({})),
    cleanup: vi.fn(),
    _page: i,
  }
}

vi.mock('pdfjs-dist', () => ({
  GlobalWorkerOptions: { workerSrc: '' },
  getDocument: vi.fn(() => ({
    promise: Promise.resolve({ numPages, getPage, destroy }),
    destroy,
  })),
  TextLayer: vi.fn().mockImplementation(() => ({ render: vi.fn(async () => {}), cancel: vi.fn() })),
}))
vi.mock('pdfjs-dist/build/pdf.worker.min.mjs?url', () => ({ default: 'worker.js' }))

const { default: PdfView } = await import('./PdfView')

const file: FileView = { id: 'f-doc', name: 'doc.pdf', ext: 'pdf', size: 4096, mime: 'application/pdf' }

class ResizeObserverStub {
  observe() {}
  disconnect() {}
}

// jsdom never computes real layout (clientWidth is 0 by default — the same
// gotcha as Feed.test.tsx's offsetHeight/offsetWidth). Fixed at 148 so
// fit-to-width lands on a round scale (page 1 is 100 CSS px wide at scale 1
// in the mock below: (148 - 48) / 100 = 1.0 = "100%").
const savedClientWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth')
beforeAll(() => {
  ;(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, get: () => 148 })
})
afterAll(() => {
  if (savedClientWidth) Object.defineProperty(HTMLElement.prototype, 'clientWidth', savedClientWidth)
})
beforeEach(() => {
  setLocale('en')
  numPages = 5
  getPage.mockClear()
  destroy.mockClear()
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(new Uint8Array([1, 2, 3]).buffer)),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
})

test('loads the document and renders only the visible pages (±1), not the whole document', async () => {
  const onFail = vi.fn()
  render(<PdfView serverId={1} file={file} onFail={onFail} />)
  expect(await screen.findByText('1 / 5')).toBeInTheDocument()
  await waitFor(() => expect(getPage).toHaveBeenCalledWith(2))
  expect(getPage).toHaveBeenCalledWith(1)
  // KEEP=1: only one neighbour on each side of the visible page — page 3
  // (two away) must not be touched, let alone 4 or 5.
  expect(getPage).not.toHaveBeenCalledWith(3)
  expect(getPage).not.toHaveBeenCalledWith(4)
  expect(getPage).not.toHaveBeenCalledWith(5)
  expect(onFail).not.toHaveBeenCalled()
})

test('a load failure (network error, bad magic, over the size cap) calls onFail', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('nope', { status: 415 })),
  )
  const onFail = vi.fn()
  render(<PdfView serverId={1} file={file} onFail={onFail} />)
  await waitFor(() => expect(onFail).toHaveBeenCalled())
})

test('Ctrl+= / Ctrl+- / Ctrl+0 zoom in, out and back to fit-width', async () => {
  render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  await screen.findByText('100%') // fit-width, per the stubbed clientWidth above

  fireEvent.keyDown(window, { code: 'Equal', ctrlKey: true })
  expect(await screen.findByText('125%')).toBeInTheDocument()

  fireEvent.keyDown(window, { code: 'Minus', ctrlKey: true })
  expect(await screen.findByText('100%')).toBeInTheDocument()

  fireEvent.keyDown(window, { code: 'Equal', ctrlKey: true })
  await screen.findByText('125%')
  fireEvent.keyDown(window, { code: 'Digit0', ctrlKey: true })
  expect(await screen.findByText('100%')).toBeInTheDocument()
})

test('the +/− and Fit width buttons work the same as the keyboard shortcuts', async () => {
  render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  await fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }))
  expect(await screen.findByText('125%')).toBeInTheDocument()
  await fireEvent.click(screen.getByRole('button', { name: 'Zoom out' }))
  expect(await screen.findByText('100%')).toBeInTheDocument()
  await fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }))
  await screen.findByText('125%')
  await fireEvent.click(screen.getByRole('button', { name: 'Fit width' }))
  expect(await screen.findByText('100%')).toBeInTheDocument()
})

test('unmount destroys the loading task (terminates the worker) and zeroes every canvas', async () => {
  const { unmount, container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  await waitFor(() => expect(container.querySelectorAll('canvas').length).toBeGreaterThan(0))
  const canvases = Array.from(container.querySelectorAll('canvas'))
  unmount()
  expect(destroy).toHaveBeenCalled()
  for (const c of canvases) {
    expect(c.width).toBe(0)
    expect(c.height).toBe(0)
  }
})

test('scrolling out of a page returns its canvas to the pool instead of allocating a new one', async () => {
  numPages = 6
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 6')
  await waitFor(() => expect(getPage).toHaveBeenCalledWith(2))
  const firstCanvas = container.querySelector('[data-page="1"] canvas')
  expect(firstCanvas).not.toBeNull()

  const scroller = container.querySelector('.overflow-auto') as HTMLElement
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, value: 336 })
  await act(async () => {
    fireEvent.scroll(scroller)
  })
  await waitFor(() => expect(getPage).toHaveBeenCalledWith(5))

  const laterCanvas = container.querySelector('[data-page="4"] canvas') ?? container.querySelector('[data-page="3"] canvas')
  expect(laterCanvas).not.toBeNull()
  expect(laterCanvas).toBe(firstCanvas) // same DOM node, reused — not a fresh one
})
