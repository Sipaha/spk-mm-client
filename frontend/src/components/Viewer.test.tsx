import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { Viewer } from './Viewer'

const img: FileView = { id: 'f-build', name: 'build.png', ext: 'png', size: 5000, mime: 'image/png', width: 1280, height: 720, has_preview: true }
const log: FileView = { id: 'f-log', name: 'server.log', ext: 'log', size: 70000, mime: 'text/plain' }
const noop = () => {}

beforeEach(() => setLocale('en'))
afterEach(() => vi.unstubAllGlobals())

test('full image, keyboard navigation around the post, Escape closes', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('hello log', { headers: { 'X-Truncated': '1' } })))
  const onIndex = vi.fn()
  const onClose = vi.fn()
  const { rerender } = render(<Viewer serverId={1} files={[img, log]} index={0} onIndex={onIndex} onClose={onClose} onDownload={noop} onOpen={noop} />)
  const dialog = screen.getByRole('dialog', { name: 'File viewer' })
  expect(screen.getByRole('img', { name: 'build.png' })).toHaveAttribute('src', '/media/1/full/f-build?src=preview')
  expect(dialog).toHaveTextContent('1 of 2')
  expect(screen.getByRole('button', { name: 'Close' })).toHaveFocus()
  await userEvent.keyboard('{ArrowRight}')
  expect(onIndex).toHaveBeenLastCalledWith(1)
  await userEvent.keyboard('{ArrowLeft}')
  expect(onIndex).toHaveBeenLastCalledWith(1) // wraps around
  rerender(<Viewer serverId={1} files={[img, log]} index={1} onIndex={onIndex} onClose={onClose} onDownload={noop} onOpen={noop} />)
  expect(await screen.findByText('hello log')).toBeInTheDocument()
  expect(screen.getByText('Showing the first 64 KB')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalled()
})

test('download and open act on the shown file; focus returns to the opener', async () => {
  const onDownload = vi.fn()
  function Host({ open }: { open: boolean }) {
    return (
      <>
        <button>opener</button>
        {open && <Viewer serverId={1} files={[img]} index={0} onIndex={noop} onClose={noop} onDownload={onDownload} onOpen={noop} />}
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
  render(<Viewer serverId={1} files={[img]} index={0} onIndex={noop} onClose={noop} onDownload={noop} onOpen={noop} />)
  const el = screen.getByRole('img', { name: 'build.png' })
  expect(screen.getByText('Loading…')).toBeInTheDocument()
  fireEvent.load(el)
  expect(screen.queryByText('Loading…')).toBeNull()
  expect(screen.getByRole('img', { name: 'build.png' })).toBeInTheDocument()
})

test('clicking the backdrop closes the viewer around an image, while loading and after load; clicking the image itself does not', async () => {
  const onClose = vi.fn()
  render(<Viewer serverId={1} files={[img]} index={0} onIndex={noop} onClose={onClose} onDownload={noop} onOpen={noop} />)
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
  render(<Viewer serverId={1} files={[img]} index={0} onIndex={noop} onClose={noop} onDownload={onDownload} onOpen={noop} />)
  const el = screen.getByRole('img', { name: 'build.png' })
  fireEvent.error(el)
  expect(screen.queryByRole('img', { name: 'build.png' })).toBeNull()
  await userEvent.click(screen.getByRole('button', { name: 'Download build.png' }))
  expect(onDownload).toHaveBeenCalledWith(img)
  expect(screen.getByRole('button', { name: 'Open build.png' })).toBeInTheDocument()
})
