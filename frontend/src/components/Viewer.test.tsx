import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { Viewer } from './Viewer'

const img: FileView = { id: 'f-build', name: 'build.png', ext: 'png', size: 5000, mime: 'image/png', width: 1280, height: 720, has_preview: true }
const log: FileView = { id: 'f-log', name: 'server.log', ext: 'log', size: 70000, mime: 'text/plain' }
const readme: FileView = { id: 'f-readme', name: 'README.md', ext: 'md', size: 400, mime: 'text/markdown' }
const noop = () => {}

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

test('clicking the backdrop closes the viewer around an image, while loading and after load; clicking the image itself does not', async () => {
  const onClose = vi.fn()
  render(<Viewer serverId={1} files={[img]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />)
  const dialog = screen.getByRole('dialog', { name: 'File viewer' })
  // The content div wrapping the image and its nav buttons — this is the
  // "backdrop" a user clicks around the image to dismiss the viewer.
  const backdrop = dialog.children[1] as HTMLElement
  const el = screen.getByRole('img', { name: 'build.png' })
  expect(screen.getByText('Loading…')).toBeInTheDocument() // still loading

  await userEvent.click(el)
  expect(onClose).not.toHaveBeenCalled()
  fireEvent.click(backdrop)
  expect(onClose).toHaveBeenCalledTimes(1)

  onClose.mockClear()
  fireEvent.load(el)
  expect(screen.queryByText('Loading…')).toBeNull() // loaded now

  await userEvent.click(el)
  expect(onClose).not.toHaveBeenCalled()
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

test('a plain text file has no Source/Rendered switch', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('hello log')))
  render(<Viewer serverId={1} files={[log]} index={0} me="alice" onLink={noop} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  await screen.findByText('hello log')
  expect(screen.queryByRole('button', { name: 'Source' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Rendered' })).toBeNull()
})
