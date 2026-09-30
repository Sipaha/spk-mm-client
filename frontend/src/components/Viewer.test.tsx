import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { client } from '../api/client'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { Toast } from './Toast'
import { Viewer } from './Viewer'

// PdfView pulls in the real pdfjs-dist chunk; Viewer.tsx only needs to prove
// it lazy-loads *something* into a Suspense boundary and reacts to onFail —
// pdf.js's own rendering is PdfView.test.tsx's job.
let pdfShouldFail = false
vi.mock('./PdfView', () => ({
  default: ({ file, onFail }: { file: FileView; onFail(): void }) => {
    if (pdfShouldFail) {
      onFail()
      return null
    }
    // PdfView's own shape, as far as Viewer's empty-area click cares: an
    // empty-marked root and scroller, a toolbar, and a page box (unmarked)
    // with text in it.
    return (
      <div data-testid="pdf-view" data-viewer-empty="true">
        <div data-testid="pdf-toolbar">
          <span>{file.name}</span>
          <button type="button">Zoom in</button>
        </div>
        <div data-testid="pdf-scroller" data-viewer-empty="true">
          <div data-page="1">
            <span>Receipt text</span>
          </div>
        </div>
      </div>
    )
  },
}))

const img: FileView = { id: 'f-build', name: 'build.png', ext: 'png', size: 5000, mime: 'image/png', width: 1280, height: 720, has_preview: true }
const log: FileView = { id: 'f-log', name: 'server.log', ext: 'log', size: 70000, mime: 'text/plain' }
const readme: FileView = { id: 'f-readme', name: 'README.md', ext: 'md', size: 400, mime: 'text/markdown' }
const clip: FileView = { id: 'f-clip', name: 'clip.webm', ext: 'webm', size: 43538, mime: 'video/webm' }
const tone: FileView = { id: 'f-tone', name: 'tone.ogg', ext: 'ogg', size: 9736, mime: 'audio/ogg' }
const pdf: FileView = { id: 'f-pdf', name: 'spec.pdf', ext: 'pdf', size: 20480, mime: 'application/pdf' }
const noop = () => {}

beforeEach(() => {
  pdfShouldFail = false
})

beforeEach(() => setLocale('en'))
afterEach(() => vi.unstubAllGlobals())

test('full image, keyboard navigation around the post, Escape closes', async () => {
  const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () => new Response('hello log', { headers: { 'X-Truncated': '1' } }))
  vi.stubGlobal('fetch', fetchMock)
  const onIndex = vi.fn()
  const onClose = vi.fn()
  const { rerender, container } = render(<Viewer serverId={1} files={[img, log]} index={0} me="alice" onLink={noop} onIndex={onIndex} onClose={onClose} onDownload={noop} onOpen={noop} />)
  const dialog = screen.getByRole('dialog', { name: 'File viewer' })
  expect(screen.getByRole('img', { name: 'build.png' })).toHaveAttribute('src', '/media/1/full/f-build?src=preview')
  expect(dialog).toHaveTextContent('1 of 2')
  expect(screen.getByRole('button', { name: 'Close' })).toHaveFocus()
  await userEvent.keyboard('{ArrowRight}')
  expect(onIndex).toHaveBeenLastCalledWith(1)
  await userEvent.keyboard('{ArrowLeft}')
  expect(onIndex).toHaveBeenLastCalledWith(1) // wraps around
  rerender(<Viewer serverId={1} files={[img, log]} index={1} me="alice" onLink={noop} onIndex={onIndex} onClose={onClose} onDownload={noop} onOpen={noop} />)
  expect(await screen.findByText('hello log')).toBeInTheDocument()
  // full=1: the viewer asks for up to 1 MiB, not the feed's 64 KiB fragment.
  expect(fetchMock.mock.calls.at(-1)?.[0]).toBe('/media/1/text/f-log?full=1')
  expect(screen.getByText('Showing the first 1 MB')).toBeInTheDocument()
  expect(screen.queryByText(/64 KB/)).toBeNull()
  // Full width: the text panel does not have the old max-w-5xl cap.
  expect(container.querySelector('.max-w-5xl')).toBeNull()
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalled()
})

// Final-review RULING #3 (UI pass 2026-09-28, a Task 1 review's
// already-flagged-but-deferred finding): the prev/next buttons' hit area
// must be at least ~40px, not ~32px — jsdom does not compute real layout,
// so this pins the class that actually determines it (py-2 + the 24px
// chevron icon = 40px) rather than a pixel measurement.
test('prev/next buttons have a touch-sized hit area (py-2, not the old py-1)', () => {
  render(<Viewer serverId={1} files={[img, log]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  expect(screen.getByRole('button', { name: 'Previous file' }).className).toContain('py-2')
  expect(screen.getByRole('button', { name: 'Next file' }).className).toContain('py-2')
})

test('download and open act on the shown file; focus returns to the opener', async () => {
  const onDownload = vi.fn()
  function Host({ open }: { open: boolean }) {
    return (
      <>
        <button>opener</button>
        {open && <Viewer serverId={1} files={[img]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={onDownload} onOpen={noop} />}
      </>
    )
  }
  const { rerender } = render(<Host open={false} />)
  screen.getByRole('button', { name: 'opener' }).focus()
  rerender(<Host open />)
  expect(screen.queryByText('1 of 1')).toBeNull()
  await userEvent.click(screen.getByRole('button', { name: 'Download' }))
  expect(onDownload).toHaveBeenCalledWith(img)
  rerender(<Host open={false} />)
  expect(screen.getByRole('button', { name: 'opener' })).toHaveFocus()
})

test('a loading indicator shows until the image loads', async () => {
  render(<Viewer serverId={1} files={[img]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  const el = screen.getByRole('img', { name: 'build.png' })
  expect(screen.getByText('Loading…')).toBeInTheDocument()
  fireEvent.load(el)
  expect(screen.queryByText('Loading…')).toBeNull()
  expect(screen.getByRole('img', { name: 'build.png' })).toBeInTheDocument()
})

test('clicking the empty area around the image closes the viewer, while loading and after load; clicking the image itself does not', async () => {
  const onClose = vi.fn()
  const { container } = render(<Viewer serverId={1} files={[img]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />)
  const el = screen.getByRole('img', { name: 'build.png' })
  // ImageZoom's own root div fills the whole pane (so the image can be
  // centered/panned inside it) — a real mouse aiming at the empty area
  // around the image actually lands on this div, not on the pane div
  // behind it. Firing the click directly on the pane div (as this test used
  // to) is unrealistic: no real mouse can hit it while the image is shown.
  const emptyArea = container.querySelector('[data-viewer-empty]') as HTMLElement
  expect(emptyArea).not.toBeNull()
  expect(screen.getByText('Loading…')).toBeInTheDocument() // still loading

  await userEvent.click(el)
  expect(onClose).not.toHaveBeenCalled()
  await userEvent.click(emptyArea) // a real click: pointerdown + click on the same element
  expect(onClose).toHaveBeenCalledTimes(1)

  onClose.mockClear()
  fireEvent.load(el)
  expect(screen.queryByText('Loading…')).toBeNull() // loaded now

  await userEvent.click(el)
  expect(onClose).not.toHaveBeenCalled()
  await userEvent.click(emptyArea) // a real click: pointerdown + click on the same element
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('at zoom > 1, clicking the visible image still does not close; clicking the uncovered area around it still does', async () => {
  const onClose = vi.fn()
  const { container } = render(<Viewer serverId={1} files={[img]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />)
  const el = screen.getByRole('img', { name: 'build.png' })
  fireEvent.load(el)
  const emptyArea = container.querySelector('[data-viewer-empty]') as HTMLElement
  fireEvent.keyDown(window, { key: '+', code: 'Equal' }) // zoom in past fit

  await userEvent.click(el)
  expect(onClose).not.toHaveBeenCalled()
  await userEvent.click(emptyArea) // a real click: pointerdown + click on the same element
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('a double-click on the empty area around the image closes the viewer on its first click, same as a plain click (real browsers fire click before dblclick — the leading click is what closes it; ImageZoom.test.tsx covers that the double-click itself does not toggle zoom there)', async () => {
  const onClose = vi.fn()
  const { container } = render(<Viewer serverId={1} files={[img]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />)
  const el = screen.getByRole('img', { name: 'build.png' })
  fireEvent.load(el)
  const emptyArea = container.querySelector('[data-viewer-empty]') as HTMLElement
  fireEvent.click(emptyArea)
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('video: clicking the pane around the player closes the viewer; clicking the player itself does not', async () => {
  vi.spyOn(client, 'mediaStreamBase').mockResolvedValue('/media')
  const onClose = vi.fn()
  const { container } = render(
    <Viewer serverId={1} files={[clip]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />,
  )
  const dialog = screen.getByRole('dialog', { name: 'File viewer' })
  const backdrop = dialog.children[1] as HTMLElement
  const video = container.querySelector('video')!
  fireEvent.click(video)
  expect(onClose).not.toHaveBeenCalled()
  fireEvent.click(backdrop)
  expect(onClose).toHaveBeenCalledTimes(1)
  vi.restoreAllMocks()
})

test('markdown/text: clicking inside the rendered content does not close the viewer (it fills the pane by design)', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('# Title\n\nbody text\n')))
  const onClose = vi.fn()
  render(<Viewer serverId={1} files={[readme]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />)
  const heading = await screen.findByRole('heading', { name: 'Title' })
  fireEvent.click(heading)
  expect(onClose).not.toHaveBeenCalled()
  vi.unstubAllGlobals()
})

test('a pan-drag that ends without a following click (e.g. released outside the window) does not swallow a later, unrelated backdrop click', async () => {
  const onClose = vi.fn()
  const { container } = render(
    <Viewer serverId={1} files={[img]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />,
  )
  const dialog = screen.getByRole('dialog', { name: 'File viewer' })
  const backdrop = dialog.children[1] as HTMLElement
  // ImageZoom's own root div — the immediate parent of its <img>s — is
  // where wheel/mousedown are wired (ImageZoom.tsx).
  const zoomRoot = container.querySelector('img[alt="build.png"]')!.parentElement as HTMLElement

  // Zoom in so a drag actually pans (ImageZoom ignores mousedown at "fit").
  fireEvent.wheel(zoomRoot, { deltaY: -100, clientX: 50, clientY: 50 })
  // Drag past the threshold, then release — like the real ImageZoom
  // gesture — but with no `click` DOM event following (as happens when the
  // mouseup lands outside the browser window: no synthetic click fires
  // anywhere in the document, so the backdrop's own click-based reset in
  // `closeOnBackdrop` never runs).
  fireEvent.mouseDown(zoomRoot, { button: 0, clientX: 100, clientY: 100 })
  fireEvent.mouseMove(window, { clientX: 130, clientY: 100 })
  fireEvent.mouseUp(window)

  // No click follows. Give the safety-net timer a chance to run.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 10))
  })

  // A later, unrelated click on the backdrop must still close the viewer —
  // the stale "just dragged" flag must not still be swallowing it.
  fireEvent.click(backdrop)
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('an image that fails to load falls back to a card with download/open, not a broken image', async () => {
  const onDownload = vi.fn()
  const { container } = render(<Viewer serverId={1} files={[img]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={onDownload} onOpen={noop} />)
  // build.png has a preview and is within the size limit, so the viewer
  // starts loading both at once (see Task 3): a failure of one alone isn't
  // fatal while the other might still come through.
  const preview = container.querySelector('img[src$="src=preview"]')!
  const original = container.querySelector('img[src$="src=file"]')!
  fireEvent.error(preview)
  expect(screen.queryByRole('button', { name: 'Download build.png' })).toBeNull() // still waiting on the original
  fireEvent.error(original)
  expect(screen.queryByRole('img', { name: 'build.png' })).toBeNull()
  await userEvent.click(screen.getByRole('button', { name: 'Download build.png' }))
  expect(onDownload).toHaveBeenCalledWith(img)
  expect(screen.getByRole('button', { name: 'Open build.png' })).toBeInTheDocument()
})

test('the original loads immediately alongside the preview; a working original replaces the preview without the preview ever failing', async () => {
  const { container } = render(<Viewer serverId={1} files={[img]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  const preview = container.querySelector('img[src$="src=preview"]')!
  const original = container.querySelector('img[src$="src=file"]')!
  expect(preview).toHaveAttribute('alt', 'build.png') // shown as a placeholder right away
  expect(original).toHaveAttribute('alt', '') // requested immediately, but not shown yet
  fireEvent.load(original)
  expect(screen.getByRole('img', { name: 'build.png' })).toBe(original) // swapped in, no jump
  expect(preview).toHaveAttribute('alt', '')
})

test('an original over the size limit is never requested; only the preview is shown, and its own failure still falls back to a card', async () => {
  const bigWithPreview: FileView = { id: 'f-huge', name: 'huge.png', ext: 'png', size: 40 * 1024 * 1024, mime: 'image/png', has_preview: true }
  const { container } = render(<Viewer serverId={1} files={[bigWithPreview]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  expect(container.querySelector('img[src$="src=file"]')).toBeNull()
  const preview = screen.getByRole('img', { name: 'huge.png' })
  expect(preview).toHaveAttribute('src', '/media/1/full/f-huge?src=preview')
  fireEvent.error(preview)
  expect(await screen.findByRole('button', { name: 'Download huge.png' })).toBeInTheDocument()
})

test('text search: counter and Enter/Shift+Enter cycle matches, current one highlighted', async () => {
  vi.useFakeTimers()
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  vi.stubGlobal('fetch', vi.fn(async () => new Response('one two one three one')))
  render(<Viewer serverId={1} files={[log]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  await act(async () => vi.advanceTimersByTime(0))
  const box = await screen.findByRole('textbox', { name: 'Search in file' })
  await user.type(box, 'one')
  await act(async () => vi.advanceTimersByTime(150))
  expect(screen.getByText('1 of 3')).toBeInTheDocument()
  const dialog = screen.getByRole('dialog', { name: 'File viewer' })
  expect(dialog.querySelectorAll('mark')).toHaveLength(3)
  await user.type(box, '{Enter}')
  expect(screen.getByText('2 of 3')).toBeInTheDocument()
  await user.type(box, '{Shift>}{Enter}{/Shift}')
  expect(screen.getByText('1 of 3')).toBeInTheDocument()
  vi.useRealTimers()
})

test('Escape in the search field clears it first; Escape in an empty field still closes the viewer', async () => {
  vi.useFakeTimers()
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  vi.stubGlobal('fetch', vi.fn(async () => new Response('one two one')))
  const onClose = vi.fn()
  render(<Viewer serverId={1} files={[log]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />)
  await act(async () => vi.advanceTimersByTime(0))
  const box = await screen.findByRole('textbox', { name: 'Search in file' })
  await user.type(box, 'one')
  await act(async () => vi.advanceTimersByTime(150))
  expect(screen.getByText('1 of 2')).toBeInTheDocument()
  await user.type(box, '{Escape}')
  expect(box).toHaveValue('')
  expect(onClose).not.toHaveBeenCalled()
  await user.type(box, '{Escape}')
  expect(onClose).toHaveBeenCalled()
  vi.useRealTimers()
})

test('arrow keys typed in the search field move the caret, not the post’s files', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('one two one')))
  const onIndex = vi.fn()
  render(<Viewer serverId={1} files={[log, img]} index={0} me="alice" onLink={noop} onIndex={onIndex} onClose={noop} onDownload={noop} onOpen={noop} />)
  const box = await screen.findByRole('textbox', { name: 'Search in file' })
  box.focus()
  await userEvent.keyboard('{ArrowRight}{ArrowLeft}')
  expect(onIndex).not.toHaveBeenCalled()
})

test('markdown: opens rendered by default (heading, list, remote image as a link); Source switches to TextView with search', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('# Title\n\n- one\n- two\n\n![pic](https://example.com/x.png)\n')))
  const onLink = vi.fn()
  const { container } = render(<Viewer serverId={1} files={[readme]} index={0} me="alice" onLink={onLink} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  expect(await screen.findByRole('heading', { name: 'Title' })).toBeInTheDocument()
  expect(screen.getAllByRole('listitem')).toHaveLength(2)
  expect(container.querySelector('img')).toBeNull() // the remote image is a link, never fetched
  await userEvent.click(screen.getByRole('link', { name: /pic/ }))
  expect(onLink).toHaveBeenCalledWith('https://example.com/x.png')
  expect(screen.queryByRole('textbox', { name: 'Search in file' })).toBeNull()

  await userEvent.click(screen.getByRole('button', { name: 'Source' }))
  expect(await screen.findByRole('textbox', { name: 'Search in file' })).toBeInTheDocument()
  expect(screen.getByText('# Title', { exact: false })).toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: 'Title' })).toBeNull()

  await userEvent.click(screen.getByRole('button', { name: 'Rendered' }))
  expect(await screen.findByRole('heading', { name: 'Title' })).toBeInTheDocument()
})

test('markdown: paging to another file resets the toggle back to rendered', async () => {
  // The fetch mock returns the same text regardless of URL; the log file
  // (plain 'text' kind) then shows it as raw source, telling apart the two
  // panes without needing a second, unrelated fixture.
  vi.stubGlobal('fetch', vi.fn(async () => new Response('# Title\n')))
  const { rerender } = render(
    <Viewer serverId={1} files={[readme, log]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />,
  )
  await screen.findByRole('heading', { name: 'Title' })
  await userEvent.click(screen.getByRole('button', { name: 'Source' }))
  await screen.findByRole('textbox', { name: 'Search in file' })
  rerender(<Viewer serverId={1} files={[readme, log]} index={1} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  await screen.findByText('# Title', { exact: false }) // log.log rendered raw, via TextView
  rerender(<Viewer serverId={1} files={[readme, log]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  expect(await screen.findByRole('heading', { name: 'Title' })).toBeInTheDocument() // back to rendered, not left on Source
})

test('markdown: the Rendered/Source switch is a labelled group; keyboard (Tab, Enter/Space) works it', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('# Title\n')))
  render(<Viewer serverId={1} files={[readme]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  await screen.findByRole('heading', { name: 'Title' })
  expect(screen.getByRole('group', { name: 'Markdown view' })).toBeInTheDocument()

  screen.getByRole('button', { name: 'Rendered' }).focus()
  await userEvent.tab()
  expect(screen.getByRole('button', { name: 'Source' })).toHaveFocus()
  await userEvent.keyboard('{Enter}')
  expect(await screen.findByRole('textbox', { name: 'Search in file' })).toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: 'Title' })).toBeNull()

  await userEvent.tab({ shift: true })
  expect(screen.getByRole('button', { name: 'Rendered' })).toHaveFocus()
  await userEvent.keyboard(' ')
  expect(await screen.findByRole('heading', { name: 'Title' })).toBeInTheDocument()
})

test('markdown: Ctrl+F in Rendered mode switches to Source and focuses the search field (it does nothing in Rendered otherwise)', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('# Title\n\nsome body text\n')))
  render(<Viewer serverId={1} files={[readme]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  expect(await screen.findByRole('heading', { name: 'Title' })).toBeInTheDocument()
  expect(screen.queryByRole('textbox', { name: 'Search in file' })).toBeNull()

  await userEvent.keyboard('{Control>}f{/Control}')

  expect(await screen.findByRole('textbox', { name: 'Search in file' })).toHaveFocus()
  expect(screen.queryByRole('heading', { name: 'Title' })).toBeNull() // switched to Source
})

test('markdown: Ctrl+F works on a Russian keyboard layout (key is "а", not "f" — matched by the physical key instead)', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('# Title\n\nsome body text\n')))
  render(<Viewer serverId={1} files={[readme]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  expect(await screen.findByRole('heading', { name: 'Title' })).toBeInTheDocument()

  fireEvent.keyDown(window, { key: 'а', code: 'KeyF', ctrlKey: true })

  expect(await screen.findByRole('textbox', { name: 'Search in file' })).toHaveFocus()
  expect(screen.queryByRole('heading', { name: 'Title' })).toBeNull() // switched to Source
})

test('a plain text file has no Source/Rendered switch', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('hello log')))
  render(<Viewer serverId={1} files={[log]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  await screen.findByText('hello log')
  expect(screen.queryByRole('button', { name: 'Source' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Rendered' })).toBeNull()
})

test('video: opens big with native controls; ←/→ focused in the player seek instead of paging, Escape still closes', async () => {
  vi.spyOn(client, 'mediaStreamBase').mockResolvedValue('/media')
  const onIndex = vi.fn()
  const onClose = vi.fn()
  const { container } = render(
    <Viewer serverId={1} files={[clip, img]} index={0} me="alice" onLink={noop} onIndex={onIndex} onClose={onClose} onDownload={noop} onOpen={noop} />,
  )
  const video = container.querySelector('video')!
  expect(video).toHaveAttribute('controls')
  expect(video).toHaveAttribute('preload', 'none')
  video.focus()
  fireEvent.keyDown(video, { key: 'ArrowRight' })
  expect(onIndex).not.toHaveBeenCalled()
  fireEvent.keyDown(video, { key: 'ArrowLeft' })
  expect(onIndex).not.toHaveBeenCalled()
  // Outside the player, the same keys still page through the post's files.
  fireEvent.keyDown(window, { key: 'ArrowRight' })
  expect(onIndex).toHaveBeenLastCalledWith(1)
  fireEvent.keyDown(video, { key: 'Escape' })
  expect(onClose).toHaveBeenCalled()
  vi.restoreAllMocks()
})

test('audio: opens big with native controls, no viewer chrome from the feed row', async () => {
  vi.spyOn(client, 'mediaStreamBase').mockResolvedValue('/media')
  const { container } = render(
    <Viewer serverId={1} files={[tone]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />,
  )
  const audio = container.querySelector('audio')!
  expect(audio).toHaveAttribute('controls')
  expect(audio).toHaveAttribute('preload', 'none')
  expect(screen.getByRole('dialog').querySelector('header')).toHaveTextContent('tone.ogg') // the viewer's own header, not the feed's row
  vi.restoreAllMocks()
})

test('pdf: opens the lazy PdfView; download/open in the header still act on the file', async () => {
  const onDownload = vi.fn()
  const { container } = render(
    <Viewer serverId={1} files={[pdf]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={onDownload} onOpen={noop} />,
  )
  expect(await screen.findByTestId('pdf-view')).toHaveTextContent('spec.pdf')
  await userEvent.click(screen.getByRole('button', { name: 'Download' }))
  expect(onDownload).toHaveBeenCalledWith(pdf)
  // Plan.md ruling: never an iframe/embed/object for a PDF (WebKitGTK's own
  // bundled pdf.js is CVE-2024-4367-class and cannot be configured off).
  expect(container.querySelector('iframe')).toBeNull()
  expect(container.querySelector('embed')).toBeNull()
  expect(container.querySelector('object')).toBeNull()
})

test('pdf: a load failure falls back to a card, still with no iframe/embed/object anywhere', async () => {
  pdfShouldFail = true
  const onDownload = vi.fn()
  const { container } = render(
    <Viewer serverId={1} files={[pdf]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={onDownload} onOpen={noop} />,
  )
  expect(screen.queryByTestId('pdf-view')).toBeNull()
  await userEvent.click(await screen.findByRole('button', { name: 'Download spec.pdf' }))
  expect(onDownload).toHaveBeenCalledWith(pdf)
  expect(container.querySelector('iframe')).toBeNull()
  expect(container.querySelector('embed')).toBeNull()
  expect(container.querySelector('object')).toBeNull()
})

// Review R1 (stick/download fix round 2): the viewer is a fixed z-50 modal,
// so a toast hosted by a feed sat under it — a failed Download in the
// viewer header was never seen. While open, the viewer hosts the toast.
test('a toast shows inside the open viewer, above everything under it', () => {
  const { unmount } = render(
    <>
      <Viewer serverId={1} files={[img, log]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />
      <Toast />
    </>,
  )
  act(() => useStore.getState().showToast('Could not download build.png: boom'))
  expect(screen.getByRole('dialog', { name: 'File viewer' })).toContainElement(screen.getByText(/Could not download build\.png/))
  unmount()
  useStore.setState({ toast: null })
})

// User request 2026-09-30: a plain click on the empty dark area of the PDF
// viewer (around the pages) closes it, like the image viewer — but never a
// click on a page, a text-selection drag (from a page to the empty area, or
// the other way round), a scrollbar press/drag, the wheel or the toolbar.
describe('pdf: clicking the empty area closes the viewer', () => {
  async function openPdf() {
    const onClose = vi.fn()
    render(<Viewer serverId={1} files={[pdf]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />)
    await screen.findByTestId('pdf-view')
    return { onClose, scroller: screen.getByTestId('pdf-scroller'), page: screen.getByText('Receipt text') }
  }
  const at = { clientX: 100, clientY: 100 }

  test('a click on the dark area around the page closes it', async () => {
    const { onClose, scroller } = await openPdf()
    fireEvent.pointerDown(scroller, at)
    fireEvent.click(scroller, at)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  test('a click on the page (its text) does not', async () => {
    const { onClose, page } = await openPdf()
    fireEvent.pointerDown(page, at)
    fireEvent.click(page, at)
    expect(onClose).not.toHaveBeenCalled()
  })

  test('a text-selection drag from the page that ends on the empty area does not (click lands on the scroller)', async () => {
    const { onClose, page, scroller } = await openPdf()
    fireEvent.pointerDown(page, at)
    fireEvent.click(scroller, { clientX: 300, clientY: 120 })
    expect(onClose).not.toHaveBeenCalled()
  })

  test('a drag that starts on the empty area and moves (a selection into the page) does not', async () => {
    const { onClose, scroller } = await openPdf()
    fireEvent.pointerDown(scroller, at)
    fireEvent.click(scroller, { clientX: 160, clientY: 100 })
    expect(onClose).not.toHaveBeenCalled()
  })

  test('a press on the scroller\'s scrollbar (overlay or classic) does not', async () => {
    const { onClose, scroller } = await openPdf()
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 2000 })
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 800 })
    Object.defineProperty(scroller, 'clientWidth', { configurable: true, value: 1000 }) // overlay: no layout width
    scroller.getBoundingClientRect = () => ({ left: 0, top: 0, width: 1000, height: 800, right: 1000, bottom: 800, x: 0, y: 0, toJSON: () => ({}) })
    const bar = { clientX: 993, clientY: 400 }
    fireEvent.pointerDown(scroller, bar)
    fireEvent.click(scroller, bar)
    expect(onClose).not.toHaveBeenCalled()
    // …while a click well inside the same scroller still closes.
    fireEvent.pointerDown(scroller, { clientX: 500, clientY: 400 })
    fireEvent.click(scroller, { clientX: 500, clientY: 400 })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  test('the wheel and toolbar clicks do not', async () => {
    const { onClose, scroller } = await openPdf()
    fireEvent.wheel(scroller, { deltaY: 100 })
    const zoomIn = screen.getByRole('button', { name: 'Zoom in' })
    fireEvent.pointerDown(zoomIn, at)
    fireEvent.click(zoomIn, at)
    fireEvent.pointerDown(screen.getByTestId('pdf-toolbar'), at)
    fireEvent.click(screen.getByTestId('pdf-toolbar'), at)
    expect(onClose).not.toHaveBeenCalled()
  })
})
