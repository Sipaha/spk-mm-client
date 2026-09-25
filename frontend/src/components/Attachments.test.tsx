import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { Attachments } from './Attachments'

const handlers = () => ({ onView: vi.fn(), onDownload: vi.fn(), onOpen: vi.fn() })
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
  expect(img).toHaveAttribute('loading', 'lazy')
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

test('other files are cards with download and open', async () => {
  const h = handlers()
  const pdf: FileView = { id: 'f-spec', name: 'spec.pdf', ext: 'pdf', size: 2048, mime: 'application/pdf' }
  render(<Attachments serverId={1} files={[pdf, { id: 's', name: 'logo.svg', ext: 'svg', size: 10, mime: 'image/svg+xml' }]} {...h} />)
  expect(screen.getByText('2.0 KB')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Open spec.pdf' }))
  expect(h.onOpen).toHaveBeenCalledWith(pdf)
  expect(screen.getByRole('button', { name: 'Download logo.svg' })).toBeInTheDocument()
  expect(document.querySelector('img')).toBeNull()
})
