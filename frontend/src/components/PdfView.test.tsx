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
// Per-page size at scale 1 — overridden per test to mock a document with
// mixed portrait/landscape pages (fix round 1: pages used to all share page
// 1's box regardless of their own size).
let sizeForPage: (i: number) => { w: number; h: number } = () => ({ w: 100, h: 100 })
// M5 (final review): a page whose first render's task.promise rejects
// (cancelled, or a broken page) — its canvas must not leak past that.
let renderShouldReject: (i: number) => boolean = () => false

function makePage(i: number) {
  return {
    getViewport: ({ scale }: { scale: number }) => {
      const { w, h } = sizeForPage(i)
      return { width: w * scale, height: h * scale }
    },
    render: vi.fn(() => ({
      promise: renderShouldReject(i) ? Promise.reject(new Error('render cancelled')) : Promise.resolve(),
      cancel: vi.fn(),
    })),
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
const { getDocument } = await import('pdfjs-dist')

const file: FileView = { id: 'f-doc', name: 'doc.pdf', ext: 'pdf', size: 4096, mime: 'application/pdf' }

// The ResizeObserver constructor's callback is captured so a test can fire
// it manually (fix round 2's "resize re-fits" test) — jsdom never actually
// observes real layout changes.
let roCallback: (() => void) | null = null
class ResizeObserverStub {
  constructor(cb: () => void) {
    roCallback = cb
  }
  observe() {}
  disconnect() {}
}

// jsdom never computes real layout (clientWidth is 0 by default — the same
// gotcha as Feed.test.tsx's offsetHeight/offsetWidth). Mutable (not a fixed
// 148) so the resize test can change it and re-trigger the ResizeObserver
// callback. Default 148 so fit-to-width lands on a round scale (page 1 is
// 100 CSS px wide at scale 1 in the mock below: (148 - 48) / 100 = 1.0 =
// "100%").
let stubClientWidth = 148
const savedClientWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth')
// jsdom doesn't implement Element.scrollTo (same gotcha as Feed.test.tsx's
// scrollTo stub) — PdfView's scroll-anchor effect (fix round 2) calls it
// whenever the on-screen scale changes, to stay on the same page.
const scrollToSpy = vi.fn()
const savedScrollTo = HTMLElement.prototype.scrollTo
beforeAll(() => {
  ;(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, get: () => stubClientWidth })
  HTMLElement.prototype.scrollTo = scrollToSpy as unknown as typeof HTMLElement.prototype.scrollTo
})
afterAll(() => {
  if (savedClientWidth) Object.defineProperty(HTMLElement.prototype, 'clientWidth', savedClientWidth)
  HTMLElement.prototype.scrollTo = savedScrollTo
})
beforeEach(() => {
  setLocale('en')
  numPages = 5
  sizeForPage = () => ({ w: 100, h: 100 })
  renderShouldReject = () => false
  stubClientWidth = 148
  roCallback = null
  scrollToSpy.mockClear()
  getPage.mockClear()
  destroy.mockClear()
  vi.mocked(getDocument).mockClear()
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

test('getDocument is called with the optional wasm/cmap/font asset URLs, not just the bytes (final review I3)', async () => {
  render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  expect(getDocument).toHaveBeenCalledWith(
    expect.objectContaining({
      wasmUrl: '/pdfjs/wasm/',
      cMapUrl: '/pdfjs/cmaps/',
      cMapPacked: true,
      standardFontDataUrl: '/pdfjs/standard_fonts/',
      enableXfa: false,
    }),
  )
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

test('Ctrl+= / Ctrl+- / Ctrl+0 zoom in, out and back to the default zoom', async () => {
  render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  await screen.findByText('100%') // the default zoom, capped at fit-width by the narrow stubbed clientWidth above

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

test('a canvas whose first render is cancelled (task.promise rejects) is zeroed, not leaked (M5, final review)', async () => {
  numPages = 3
  renderShouldReject = (i) => i === 2
  const canvases: HTMLCanvasElement[] = []
  const nativeCreateElement = document.createElement.bind(document)
  const spy = vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
    const el = nativeCreateElement(tag)
    if (tag === 'canvas') canvases.push(el as HTMLCanvasElement)
    return el
  }) as typeof document.createElement)
  try {
    render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
    await screen.findByText('1 / 3')
    await waitFor(() => expect(getPage).toHaveBeenCalledWith(2))
    // Page 1 renders fine (canvases[0]); page 2's render rejects, but a
    // canvas was already taken from the pool and sized for it before that —
    // canvases[1]. Before the fix, nothing zeroed it on the reject path.
    await waitFor(() => expect(canvases.length).toBeGreaterThanOrEqual(2))
    const page2Canvas = canvases[1]
    await waitFor(() => {
      expect(page2Canvas.width).toBe(0)
      expect(page2Canvas.height).toBe(0)
    })
  } finally {
    spy.mockRestore()
  }
})

test("fit mode: a landscape page fits its own width too, same as every other page — no page ever needs horizontal scroll (fix round 2, review of round 1's 11bc003)", async () => {
  numPages = 4
  sizeForPage = (i) => (i === 3 ? { w: 200, h: 50 } : { w: 100, h: 100 })
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 4')

  // Not yet measured pages (and page 1 itself) use page 1's square box, fit
  // to the container width (148 - 48 = 100).
  const page1Box = container.querySelector('[data-page="1"]') as HTMLElement
  expect(page1Box.style.width).toBe('100px')
  expect(page1Box.style.height).toBe('100px')

  // Scroll far enough that page 3 (landscape, 200x50 natively) enters the
  // visible window and gets measured.
  const scroller = container.querySelector('.overflow-auto') as HTMLElement
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, value: 230 })
  await act(async () => {
    fireEvent.scroll(scroller)
  })
  await waitFor(() => expect(getPage).toHaveBeenCalledWith(3))

  // Fix round 1 fixed the box's *aspect ratio* (no longer stretched into
  // page 1's box) but round 1's global scale still sized it at its own
  // native dimensions (200x50) — 2x wider than the 100px container, forcing
  // a horizontal scrollbar onto the whole scroller (review's regression).
  // Fix round 2: fit to *its own* width (200 native -> scale 0.5), same as
  // every other page, so its box width also matches the container exactly.
  await waitFor(() => {
    const box3 = container.querySelector('[data-page="3"]') as HTMLElement
    expect(box3.style.width).toBe('100px')
    expect(box3.style.height).toBe('25px') // 50 * 0.5, its own aspect preserved
  })
  const canvas3 = container.querySelector('[data-page="3"] canvas') as HTMLCanvasElement
  expect(canvas3.width).toBe(100)
  expect(canvas3.height).toBe(25)

  // The current-page counter is still correct with mixed sizes.
  expect(await screen.findByText('3 / 4')).toBeInTheDocument()

  // Page 1's own box is untouched by another page's size.
  expect(page1Box.style.width).toBe('100px')
  expect(page1Box.style.height).toBe('100px')
})

test('manual zoom applies one uniform scale to every page, unlike fit mode (fix round 2)', async () => {
  numPages = 4
  sizeForPage = (i) => (i === 3 ? { w: 200, h: 50 } : { w: 100, h: 100 })
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 4')

  // Measure page 3 (landscape) while still in fit mode.
  const scroller = container.querySelector('.overflow-auto') as HTMLElement
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, value: 230 })
  await act(async () => {
    fireEvent.scroll(scroller)
  })
  await waitFor(() => expect(getPage).toHaveBeenCalledWith(3))
  await waitFor(() => expect((container.querySelector('[data-page="3"]') as HTMLElement).style.width).toBe('100px'))

  // Back to page 1 (a known 100% fit scale) before switching to manual zoom.
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, value: 0 })
  await act(async () => {
    fireEvent.scroll(scroller)
  })
  await screen.findByText('1 / 4')

  fireEvent.keyDown(window, { code: 'Equal', ctrlKey: true })
  await screen.findByText('125%')

  // Manual zoom: the SAME 1.25 multiplier applies to each page's own native
  // size — page 3 (native 200 wide) is now twice as wide as page 1 (native
  // 100), not re-fit to match the container the way fit mode did.
  const page1Box = container.querySelector('[data-page="1"]') as HTMLElement
  const page3Box = container.querySelector('[data-page="3"]') as HTMLElement
  expect(page1Box.style.width).toBe('125px') // 100 * 1.25
  expect(page3Box.style.width).toBe('250px') // 200 * 1.25
})

test('resizing the pane re-fits every page and keeps the same current page (fix round 2)', async () => {
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  const page1Box = container.querySelector('[data-page="1"]') as HTMLElement
  expect(page1Box.style.width).toBe('100px') // (148 - 48) / 100

  stubClientWidth = 248 // the pane grew
  await act(async () => {
    roCallback?.()
  })

  // The default zoom grows with the pane only up to its 175% cap…
  await waitFor(() => expect(page1Box.style.width).toBe('175px'))
  // …Fit width follows the pane all the way: (248 - 48) / 100.
  fireEvent.click(screen.getByRole('button', { name: 'Fit width' }))
  await waitFor(() => expect(page1Box.style.width).toBe('200px'))
  expect(screen.getByText('1 / 5')).toBeInTheDocument() // stayed on the same page
})

// pdf-lag report (2026-09-30): fitting every page to the pane by default
// opened a Letter receipt at 301% on a 1920 px window (huge text, a
// page-sized canvas to repaint on every scroll step). The default is the
// webapp's zoom (ZoomSettings.DEFAULT_SCALE = 1.75: a Letter page ~1071 px
// wide), capped so it never exceeds the pane; Fit width is an explicit mode
// and Ctrl+0 returns to the default.
test('default zoom: the webapp\'s 175% on a wide pane, not fit-to-width; Fit width and Ctrl+0 switch modes', async () => {
  stubClientWidth = 348 // fit-to-width would be (348 - 48) / 100 = 300%
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  expect(await screen.findByText('175%')).toBeInTheDocument()
  const page1Box = container.querySelector('[data-page="1"]') as HTMLElement
  expect(page1Box.style.width).toBe('175px')
  expect(page1Box.className).toMatch(/(^|\s)mx-auto(\s|$)/) // centred in the pane
  const fitButton = screen.getByRole('button', { name: 'Fit width' })
  expect(fitButton).toHaveAttribute('aria-pressed', 'false')

  fireEvent.click(fitButton)
  expect(await screen.findByText('300%')).toBeInTheDocument()
  expect(page1Box.style.width).toBe('300px')
  expect(fitButton).toHaveAttribute('aria-pressed', 'true')

  fireEvent.keyDown(window, { code: 'Digit0', ctrlKey: true })
  expect(await screen.findByText('175%')).toBeInTheDocument()
  expect(page1Box.style.width).toBe('175px')
  expect(fitButton).toHaveAttribute('aria-pressed', 'false')
})

test('default zoom never makes a page wider than the pane: a landscape page is capped at its own fit width', async () => {
  numPages = 2
  stubClientWidth = 348 // fit: portrait 100 wide -> 300%, landscape 200 wide -> 150%
  sizeForPage = (i) => (i === 2 ? { w: 200, h: 50 } : { w: 100, h: 100 })
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 2')
  await waitFor(() => expect(getPage).toHaveBeenCalledWith(2))
  await waitFor(() => expect((container.querySelector('[data-page="2"]') as HTMLElement).style.width).toBe('300px')) // 200 * 1.5, not 200 * 1.75
  expect((container.querySelector('[data-page="1"]') as HTMLElement).style.width).toBe('175px')
})

test('switching between fit and manual zoom keeps the scroll anchored to the same page (fix round 2)', async () => {
  numPages = 6
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 6')

  // Scroll to page 4 (uniform 100px pages + 12px gap => offset[4] = 3*112 = 336).
  const scroller = container.querySelector('.overflow-auto') as HTMLElement
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, value: 336 })
  await act(async () => {
    fireEvent.scroll(scroller)
  })
  await screen.findByText('4 / 6')

  scrollToSpy.mockClear() // ignore the mount-time/width-hydration call(s)
  fireEvent.keyDown(window, { code: 'Equal', ctrlKey: true }) // switch to manual zoom (125%)
  await screen.findByText('125%')

  // Page 4's own offset at the new uniform 125% scale: 3 pages of
  // (100*1.25 + 12) = 137 each = 411 — the anchor snaps there, not leaving
  // scrollTop at its old (336) pixel value, which would now land elsewhere.
  expect(scrollToSpy).toHaveBeenCalledWith({ top: 411 })
})

// pdf-lag report (2026-09-30): WebKitGTK repaints the page box on every
// wheel-scroll step, and a blurred box-shadow on a page-sized box (Tailwind
// `shadow`, 1840x2381 px at the old default zoom) made each step cost
// 125–200 ms instead of ~32 ms — janky scrolling. Page boxes carry no shadow
// or other blur/filter effect; the dark backdrop already frames the page.
test('page boxes have no box-shadow or filter (WebKitGTK repaints them on every scroll step)', async () => {
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  const boxes = Array.from(container.querySelectorAll<HTMLElement>('[data-page]'))
  expect(boxes.length).toBe(5)
  for (const b of boxes) {
    expect(b.className).not.toMatch(/(^|\s)(shadow|drop-shadow|blur|backdrop-blur|filter)(-|\s|$)/)
    expect(b.style.boxShadow).toBe('')
    expect(b.style.filter).toBe('')
  }
})

// Review M2 (pdf-lag fix): a resize that leaves the scale alone — the
// default zoom capped at 175% on a wide pane — must not move the scroll
// position (it used to jump to the top of the current page).
test('default zoom: resizing a wide pane (scale unchanged) keeps the scroll position', async () => {
  stubClientWidth = 548
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  await screen.findByText('175%')
  const scroller = container.querySelector('.overflow-auto') as HTMLElement
  // Page 3 starts at 2 * (175 + 12) = 374; 26 px into it.
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, value: 400 })
  await act(async () => {
    fireEvent.scroll(scroller)
  })
  await screen.findByText('3 / 5')
  scrollToSpy.mockClear()

  stubClientWidth = 748 // the window was maximised: still capped at 175%
  await act(async () => {
    roCallback?.()
  })
  expect(screen.getByText('175%')).toBeInTheDocument()
  expect(scrollToSpy).not.toHaveBeenCalled()
})

test('a resize that does rescale keeps the same place within the current page, not just the page top', async () => {
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  fireEvent.click(screen.getByRole('button', { name: 'Fit width' })) // 100%: 100 px pages
  const scroller = container.querySelector('.overflow-auto') as HTMLElement
  // Page 3 starts at 2 * (100 + 12) = 224; half-way into it.
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, value: 274 })
  await act(async () => {
    fireEvent.scroll(scroller)
  })
  await screen.findByText('3 / 5')
  scrollToSpy.mockClear()

  stubClientWidth = 248 // fit: 200%, 200 px pages; page 3 at 2 * 212 = 424
  await act(async () => {
    roCallback?.()
  })
  await screen.findByText('200%')
  expect(scrollToSpy).toHaveBeenCalledWith({ top: 524 }) // 424 + 0.5 * 200
})

// Review M3: "Fit width" is an aria-pressed toggle — pressed again, it goes
// back to the default zoom (Ctrl+0 alone was undiscoverable).
test('Fit width pressed again returns to the default zoom', async () => {
  stubClientWidth = 348
  render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('175%')
  const fitButton = screen.getByRole('button', { name: 'Fit width' })
  fireEvent.click(fitButton)
  expect(await screen.findByText('300%')).toBeInTheDocument()
  expect(fitButton).toHaveAttribute('aria-pressed', 'true')
  fireEvent.click(fitButton)
  expect(await screen.findByText('175%')).toBeInTheDocument()
  expect(fitButton).toHaveAttribute('aria-pressed', 'false')
})

// The dark area around the pages is the viewer's "empty area" (Viewer.tsx
// closes on a click there); the page boxes themselves are not.
test('root and scroller are marked as the viewer\'s empty area, page boxes are not', async () => {
  const { container } = render(<PdfView serverId={1} file={file} onFail={vi.fn()} />)
  await screen.findByText('1 / 5')
  const scroller = container.querySelector('.overflow-auto') as HTMLElement
  expect(scroller).toHaveAttribute('data-viewer-empty', 'true')
  expect(container.firstElementChild).toHaveAttribute('data-viewer-empty', 'true')
  for (const b of Array.from(container.querySelectorAll('[data-page]'))) expect(b).not.toHaveAttribute('data-viewer-empty')
})
