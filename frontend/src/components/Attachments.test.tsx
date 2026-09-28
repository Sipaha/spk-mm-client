import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView, ServerState } from '../api/types'
import { client } from '../api/client'
import { useStore } from '../store'
import { setLocale } from '../i18n'
import { Attachments } from './Attachments'

const handlers = () => ({ onView: vi.fn(), onDownload: vi.fn(), onOpen: vi.fn(), me: 'alice', onLink: vi.fn() })
const png = (o: Partial<FileView> = {}): FileView => ({
  id: 'f-build', name: 'build.png', ext: 'png', size: 5000, mime: 'image/png', width: 1280, height: 720, has_preview: true, ...o,
})

beforeEach(() => setLocale('en'))
afterEach(() => vi.unstubAllGlobals())

test('the image box has its final size before the image loads', async () => {
  const h = handlers()
  const { container } = render(<Attachments serverId={1} files={[png()]} {...h} />)
  const img = container.querySelector('img')!
  expect(img).toHaveAttribute('src', '/media/1/feed/f-build?src=preview')
  expect(img).toHaveAttribute('width', '480')
  expect(img).toHaveAttribute('height', '270')
  // Not loading="lazy": feed rows are virtualized already, and a hidden window
  // never runs WebKit's lazy-load check, which keeps every removed not-yet-loaded
  // image — with its whole detached row — alive until the next paint.
  expect(img).not.toHaveAttribute('loading')
  expect(img).toHaveAttribute('alt', 'build.png')
  const box = screen.getByRole('button', { name: 'View build.png' })
  expect(box).toHaveStyle({ width: '480px', height: '270px' })
  await userEvent.click(box)
  expect(h.onView).toHaveBeenCalledWith(png())
})

test('a single image that fails to load (404/413/415 from /media/) becomes a card', async () => {
  const h = handlers()
  const { container } = render(<Attachments serverId={1} files={[png()]} {...h} />)
  const img = container.querySelector('img')!
  fireEvent.error(img)
  expect(container.querySelector('img')).toBeNull()
  expect(screen.getByText('build.png')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Download build.png' }))
  expect(h.onDownload).toHaveBeenCalledWith(expect.objectContaining({ id: 'f-build' }))
  expect(screen.getByRole('button', { name: 'Open build.png' })).toBeInTheDocument()
})

test('several images are thumbnails; a failed one becomes a card', async () => {
  const h = handlers()
  const { container } = render(
    <Attachments serverId={1} files={[png({ id: 'a', name: 'a.png' }), png({ id: 'b', name: 'b.png', width: undefined, height: undefined })]} {...h} />,
  )
  const imgs = container.querySelectorAll('img')
  expect([...imgs].map((i) => i.getAttribute('src'))).toEqual(['/media/1/thumb/a', '/media/1/thumb/b'])
  expect(imgs[1]).toHaveAttribute('width', '120')
  expect(imgs[1]).toHaveAttribute('height', '100')
  fireEvent.error(imgs[1])
  await userEvent.click(screen.getByRole('button', { name: 'Download b.png' }))
  expect(h.onDownload).toHaveBeenCalledWith(expect.objectContaining({ id: 'b' }))
})

test('a staged (pending, not sent yet) image previews from /media/<srv>/staged/<id>, not feed/thumb, and is not a clickable viewer button', () => {
  const h = handlers()
  const staged = png({ id: 'a1', staged: true, state: 'uploading', sent: 200, has_preview: false })
  const { container } = render(<Attachments serverId={1} files={[staged]} {...h} />)
  const img = container.querySelector('img')!
  expect(img).toHaveAttribute('src', '/media/1/staged/a1')
  expect(screen.queryByRole('button', { name: 'View build.png' })).toBeNull()
})

test('a staged image mid-upload shows a progress bar; uploaded shows none; failed shows a localized error', () => {
  const h = handlers()
  const { container, rerender } = render(
    <Attachments serverId={1} files={[png({ id: 'a1', staged: true, state: 'uploading', sent: 250, size: 1000, has_preview: false })]} {...h} />,
  )
  const bar = container.querySelector('[aria-hidden] > div') as HTMLElement
  expect(bar.style.width).toBe('25%')

  rerender(<Attachments serverId={1} files={[png({ id: 'a1', staged: true, state: 'uploaded', sent: 1000, size: 1000, has_preview: false })]} {...h} />)
  expect(container.querySelector('[aria-hidden] > div')).toBeNull()

  rerender(<Attachments serverId={1} files={[png({ id: 'a1', staged: true, state: 'failed', error: 'too_large', has_preview: false })]} {...h} />)
  expect(screen.getByRole('alert')).toHaveTextContent('Error: The file is too large')
})

test('a staged non-image file (a generic card) shows the same progress/error, and no download/open buttons', () => {
  const h = handlers()
  const doc: FileView = { id: 'a2', name: 'notes.docx', size: 1000, mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', staged: true, state: 'uploading', sent: 400 }
  render(<Attachments serverId={1} files={[doc]} {...h} />)
  expect(screen.getByText('notes.docx')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Download notes.docx' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Open notes.docx' })).toBeNull()
})

test('text files: a fixed-height snippet, expand, view; unreadable text falls back to a card', async () => {
  const fetchMock = vi.fn(async (url: string) =>
    url.endsWith('/text/f-log')
      ? new Response('line 1\nline 2\n', { headers: { 'X-Truncated': '1' } })
      : new Response('', { status: 415 }),
  )
  vi.stubGlobal('fetch', fetchMock)
  const h = handlers()
  const log: FileView = { id: 'f-log', name: 'server.log', ext: 'log', size: 3000, mime: 'text/plain' }
  const bin: FileView = { id: 'f-bin', name: 'weird.txt', ext: 'txt', size: 10, mime: 'text/plain' }
  const { container } = render(<Attachments serverId={1} files={[log, bin]} {...h} />)
  expect(container.querySelector('pre')).toHaveClass('h-40')
  expect(await screen.findByText(/line 1/)).toBeInTheDocument()
  expect(container.querySelector('pre')).toHaveClass('h-40') // the height does not follow the content
  const expand = screen.getByRole('button', { name: 'Expand' })
  await userEvent.click(expand)
  expect(expand).toHaveAttribute('aria-expanded', 'true')
  await userEvent.click(screen.getByRole('button', { name: 'View server.log' }))
  expect(h.onView).toHaveBeenCalledWith(log)
  expect(await screen.findByRole('button', { name: 'Open weird.txt' })).toBeInTheDocument()
  expect(fetchMock).toHaveBeenCalledWith('/media/1/text/f-log', expect.objectContaining({ credentials: 'same-origin' }))
})

test('markdown files: rendered snippet, not a plain-text <pre>', async () => {
  const fetchMock = vi.fn(async () => new Response('# Title\n\n- one\n- two\n'))
  vi.stubGlobal('fetch', fetchMock)
  const h = handlers()
  const readme: FileView = { id: 'f-readme', name: 'README.md', ext: 'md', size: 400, mime: 'text/markdown' }
  render(<Attachments serverId={1} files={[readme]} {...h} />)
  expect(await screen.findByRole('heading', { name: 'Title' })).toBeInTheDocument()
  expect(screen.getAllByRole('listitem')).toHaveLength(2)
  expect(fetchMock).toHaveBeenCalledWith('/media/1/text/f-readme', expect.objectContaining({ credentials: 'same-origin' }))
})

test('video and audio: fixed-box poster (video) and a compact row (audio), preload="none"', async () => {
  vi.spyOn(client, 'mediaStreamBase').mockResolvedValue('/media')
  const h = handlers()
  const clip: FileView = { id: 'f-clip', name: 'clip.webm', ext: 'webm', size: 43538, mime: 'video/webm' }
  const tone: FileView = { id: 'f-tone', name: 'tone.ogg', ext: 'ogg', size: 9736, mime: 'audio/ogg' }
  const { container } = render(<Attachments serverId={1} files={[clip, tone]} {...h} />)
  const videoEl = container.querySelector('video')!
  expect(videoEl).toHaveAttribute('preload', 'none')
  expect(videoEl.parentElement).toHaveStyle({ width: '480px', height: '270px' })
  expect(screen.getByRole('button', { name: 'Play clip.webm' })).toBeInTheDocument()
  const audioEl = container.querySelector('audio')!
  expect(audioEl).toHaveAttribute('preload', 'none')
  expect(audioEl).toHaveAttribute('controls')
  expect(screen.getByText('tone.ogg')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'View clip.webm' }))
  expect(h.onView).toHaveBeenCalledWith(clip)
  vi.restoreAllMocks()
})

test('other files are cards with download and open, and no Preview action', async () => {
  const h = handlers()
  const zip: FileView = { id: 'f-arc', name: 'archive.zip', ext: 'zip', size: 2048, mime: 'application/zip' }
  render(<Attachments serverId={1} files={[zip, { id: 's', name: 'logo.svg', ext: 'svg', size: 10, mime: 'image/svg+xml' }]} {...h} />)
  expect(screen.getByText('2.0 KB')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Open archive.zip' }))
  expect(h.onOpen).toHaveBeenCalledWith(zip)
  expect(screen.getByRole('button', { name: 'Download logo.svg' })).toBeInTheDocument()
  expect(document.querySelector('img')).toBeNull()
  expect(screen.queryByRole('button', { name: /View/ })).toBeNull()
})

test('a pdf file is a card with a Preview action, alongside download and open', async () => {
  const h = handlers()
  const pdf: FileView = { id: 'f-spec', name: 'spec.pdf', ext: 'pdf', size: 2048, mime: 'application/pdf' }
  render(<Attachments serverId={1} files={[pdf]} {...h} />)
  expect(screen.getByText('spec.pdf')).toBeInTheDocument()
  expect(screen.getByText('2.0 KB')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'View spec.pdf' }))
  expect(h.onView).toHaveBeenCalledWith(pdf)
  expect(screen.getByRole('button', { name: 'Download spec.pdf' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Open spec.pdf' })).toBeInTheDocument()
  expect(document.querySelector('img')).toBeNull()
})

const goes = (state: ServerState) =>
  act(() => useStore.getState().setServers([{ id: 1, name: 'A', url: 'https://a', signed_in: true, username: 'a', gitlab: false, state, unread: false, mentions: 0 }]))

test('a failed image and a failed snippet are tried again once the server goes live again', async () => {
  let ok = false
  const fetchMock = vi.fn(async () => (ok ? new Response('line 1\n') : new Response('', { status: 404 })))
  vi.stubGlobal('fetch', fetchMock)
  goes('reconnecting')
  const log: FileView = { id: 'f-log', name: 'server.log', ext: 'log', size: 3000, mime: 'text/plain' }
  const { container } = render(<Attachments serverId={1} files={[png(), log]} {...handlers()} />)
  fireEvent.error(container.querySelector('img')!) // offline: /media/ answered 404
  await vi.waitFor(() => expect(container.querySelector('pre')).toBeNull()) // the snippet became a card
  expect(container.querySelector('img')).toBeNull()
  ok = true
  goes('live')
  expect(container.querySelector('img')).toHaveAttribute('src', '/media/1/feed/f-build?src=preview')
  expect(await screen.findByText(/line 1/)).toBeInTheDocument()
  expect(fetchMock).toHaveBeenCalledTimes(2)
})
