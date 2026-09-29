import { fireEvent, render, screen } from '@testing-library/react'
import { vi } from 'vitest'
import type { ChannelDTO, ServerDTO } from '../api/types'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { ChannelPane } from './ChannelPane'

// jsdom has no layout: the Feed's virtualizer needs sizes to render at all
// (same stubs as Feed.test.tsx).
const saved = {
  h: Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight'),
  w: Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth'),
  scrollTo: HTMLElement.prototype.scrollTo,
}
beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get() {
      return (this as HTMLElement).getAttribute('role') === 'log' ? 600 : 40
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get: () => 800 })
  HTMLElement.prototype.scrollTo = vi.fn() as unknown as typeof HTMLElement.prototype.scrollTo
})
afterAll(() => {
  if (saved.h) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', saved.h)
  if (saved.w) Object.defineProperty(HTMLElement.prototype, 'offsetWidth', saved.w)
  HTMLElement.prototype.scrollTo = saved.scrollTo
})

vi.mock('../chat', () => ({
  clearDownloads: vi.fn(), closeDownloadsPanel: vi.fn(), copyLink: vi.fn(), deletePost: vi.fn(),
  discardPost: vi.fn(), downloadFile: vi.fn(), downloadPrimaryAction: vi.fn(() => null),
  editLastOwn: vi.fn(), editPost: vi.fn(), emojiInfo: vi.fn().mockResolvedValue({ recent: [], custom: [], custom_enabled: false }),
  loadOlder: vi.fn(), markUnread: vi.fn(), setPostSaved: vi.fn(), openDownload: vi.fn(), openDownloadsPanel: vi.fn(),
  openFile: vi.fn(), openLink: vi.fn(), openThread: vi.fn(), react: vi.fn(), removeDownload: vi.fn(), retryPost: vi.fn(),
  revealDownload: vi.fn(), revealSavedFile: vi.fn(), fileKey: (s: number, f: string) => `${s}/${f}`, saveDraft: vi.fn(), sendPost: vi.fn(), uploadAttachments: vi.fn().mockResolvedValue(undefined),
  attachFromClipboard: vi.fn(), pickAttachments: vi.fn(), removeAttachment: vi.fn(), retryAttachment: vi.fn(),
}))

const { uploadAttachments } = await import('../chat')

const server = (o: Partial<ServerDTO> = {}): ServerDTO => ({
  id: 5, name: 'Acme', url: 'https://mm', signed_in: true, username: 'alice', gitlab: false,
  state: 'live', unread: false, mentions: 0, ...o,
})
const channel = (o: Partial<ChannelDTO> = {}): ChannelDTO => ({
  id: 'c-town', name: 'Town Square', type: 'O', header: '', purpose: '', team_id: 't', team_name: 'team', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'u-alice', crt: false, muted: false,
  ...o,
})

beforeEach(() => {
  setLocale('en')
  vi.mocked(uploadAttachments).mockReset().mockResolvedValue(undefined)
  useStore.setState({ editingId: null, downloads: [], downloadsOpen: false, attachments: [], attachError: null })
})

// dataTransfer for a file drag: jsdom's DataTransfer has no real drag data,
// so a plain object with the fields the handlers read is enough.
function fileDrag(files: File[]) {
  return { types: files.length > 0 ? ['Files'] : [], files }
}

test('the channel area (feed + composer) is the drop target, with data-srv/data-channel Go reads', () => {
  const { container } = render(<ChannelPane server={server()} channel={channel()} onReauth={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  expect(target).toHaveAttribute('data-srv', '5')
  expect(target).toHaveAttribute('data-channel', 'c-town')
  // '' (the channel's own composer, not a thread reply's) — Go's dropGate
  // reads it as the target root (internal/desktop/drop.go, Task 4).
  expect(target).toHaveAttribute('data-root', '')
  // Both the feed and the composer are inside it.
  expect(target.querySelector('[role="log"]')).not.toBeNull()
  expect(target.querySelector('textarea')).not.toBeNull()
})

test('dragging files over the drop target shows the overlay class; leaving it removes it', () => {
  const { container } = render(<ChannelPane server={server()} channel={channel()} onReauth={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  const file = new File(['x'], 'x.png', { type: 'image/png' })
  fireEvent.dragEnter(target, { dataTransfer: fileDrag([file]) })
  expect(target).toHaveClass('file-drop-target-active')
  fireEvent.dragLeave(target, { dataTransfer: fileDrag([file]) })
  expect(target).not.toHaveClass('file-drop-target-active')
})

test('dragging something without files does not activate the overlay', () => {
  const { container } = render(<ChannelPane server={server()} channel={channel()} onReauth={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  fireEvent.dragEnter(target, { dataTransfer: { types: ['text/plain'], files: [] } })
  expect(target).not.toHaveClass('file-drop-target-active')
})

test('dropping files uploads them (browser mode) and clears the overlay', () => {
  const { container } = render(<ChannelPane server={server()} channel={channel()} onReauth={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  const a = new File(['a'], 'a.png', { type: 'image/png' })
  const b = new File(['b'], 'b.txt', { type: 'text/plain' })
  fireEvent.dragEnter(target, { dataTransfer: fileDrag([a, b]) })
  expect(target).toHaveClass('file-drop-target-active')
  fireEvent.drop(target, { dataTransfer: fileDrag([a, b]) })
  expect(target).not.toHaveClass('file-drop-target-active')
  expect(uploadAttachments).toHaveBeenCalledWith(5, 'c-town', [a, b], '')
})

test('a drop that carries no files is a no-op', () => {
  const { container } = render(<ChannelPane server={server()} channel={channel()} onReauth={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  fireEvent.drop(target, { dataTransfer: fileDrag([]) })
  expect(uploadAttachments).not.toHaveBeenCalled()
})

test('the composer tray reflects the store\'s attachments for this channel', () => {
  useStore.setState({
    attachments: [{ id: 'a1', name: 'shot.png', size: 3, mime: 'image/png', state: 'staged', sent: 0, error: '' }],
  })
  render(<ChannelPane server={server()} channel={channel()} onReauth={() => {}} />)
  expect(screen.getByText('shot.png')).toBeInTheDocument()
})
