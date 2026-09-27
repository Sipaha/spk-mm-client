import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AttachmentView } from '../api/types'
import { setLocale } from '../i18n'
import { AttachmentsTray } from './AttachmentsTray'

const av = (over: Partial<AttachmentView> = {}): AttachmentView => ({
  id: 'a1', name: 'photo.png', size: 1000, mime: 'image/png', state: 'staged', sent: 0, error: '', ...over,
})

beforeEach(() => setLocale('en'))

test('renders nothing with an empty tray', () => {
  const { container } = render(<AttachmentsTray serverId={1} items={[]} onRemove={() => {}} onRetry={() => {}} />)
  expect(container).toBeEmptyDOMElement()
})

test('a raster attachment shows its staged thumbnail; a non-raster one shows a type icon', () => {
  const { container } = render(
    <AttachmentsTray
      serverId={1}
      items={[av({ id: 'pic', mime: 'image/png' }), av({ id: 'doc', name: 'notes.txt', mime: 'text/plain' })]}
      onRemove={() => {}}
      onRetry={() => {}}
    />,
  )
  const imgs = container.querySelectorAll('img')
  expect(imgs).toHaveLength(1) // the non-raster one is an icon, not an <img>
  expect(imgs[0].src).toContain('/media/1/staged/pic')
  expect(screen.getByText('notes.txt')).toBeInTheDocument()
})

test('an image that fails to load falls back to the icon', () => {
  const { container } = render(<AttachmentsTray serverId={1} items={[av({ id: 'pic' })]} onRemove={() => {}} onRetry={() => {}} />)
  fireEvent.error(container.querySelector('img')!)
  expect(container.querySelector('img')).toBeNull()
})

test('uploading shows a progress bar sized from sent/size; uploaded shows none', () => {
  const { rerender, container } = render(
    <AttachmentsTray serverId={1} items={[av({ state: 'uploading', sent: 250, size: 1000 })]} onRemove={() => {}} onRetry={() => {}} />,
  )
  const bar = container.querySelector('[style*="width"]') as HTMLElement
  expect(bar.style.width).toBe('25%')

  rerender(<AttachmentsTray serverId={1} items={[av({ state: 'uploaded', sent: 1000, size: 1000 })]} onRemove={() => {}} onRetry={() => {}} />)
  expect(container.querySelector('[style*="width"]')).toBeNull()
})

test('a failed upload shows a localized error and Retry, which calls onRetry with its id', async () => {
  const user = userEvent.setup()
  const onRetry = vi.fn()
  render(<AttachmentsTray serverId={1} items={[av({ id: 'a9', state: 'failed', error: 'too_large' })]} onRemove={() => {}} onRetry={onRetry} />)
  expect(screen.getByRole('alert')).toHaveTextContent('Error: The file is too large')
  await user.click(screen.getByRole('button', { name: 'Retry uploading photo.png' }))
  expect(onRetry).toHaveBeenCalledWith('a9')
})

test('the × button calls onRemove with its id; it has an accessible name', async () => {
  const user = userEvent.setup()
  const onRemove = vi.fn()
  render(<AttachmentsTray serverId={1} items={[av({ id: 'a2' })]} onRemove={onRemove} onRetry={() => {}} />)
  await user.click(screen.getByRole('button', { name: 'Remove photo.png' }))
  expect(onRemove).toHaveBeenCalledWith('a2')
})

test('Delete/Backspace removes the chip while it has focus', async () => {
  const user = userEvent.setup()
  const onRemove = vi.fn()
  render(<AttachmentsTray serverId={1} items={[av({ id: 'a3' })]} onRemove={onRemove} onRetry={() => {}} />)
  screen.getByRole('listitem').querySelector<HTMLElement>('[tabindex="0"]')!.focus()
  await user.keyboard('{Delete}')
  expect(onRemove).toHaveBeenCalledWith('a3')
  await user.keyboard('{Backspace}')
  expect(onRemove).toHaveBeenCalledTimes(2)
})
