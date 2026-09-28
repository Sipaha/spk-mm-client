import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { ApiError } from '../api/client'
import type { AttachmentView, ChannelDTO } from '../api/types'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { Composer } from './Composer'

vi.mock('../api/client', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/client')>()
  return { ...real, isDesktop: vi.fn(() => false) }
})
vi.mock('../chat', () => ({
  pickAttachments: vi.fn(),
  attachFromClipboard: vi.fn(),
  uploadAttachments: vi.fn(),
  removeAttachment: vi.fn(),
  retryAttachment: vi.fn(),
}))

const { isDesktop } = await import('../api/client')
const { attachFromClipboard, pickAttachments, removeAttachment, retryAttachment, uploadAttachments } = await import('../chat')

const channel = (o: Partial<ChannelDTO> = {}): ChannelDTO => ({
  id: 'c1', name: 'Off-Topic', type: 'O', header: '', purpose: '', team_id: 't', team_name: 'team', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'me', crt: false, muted: false, ...o,
})

const av = (over: Partial<AttachmentView> = {}): AttachmentView => ({
  id: 'a1', name: 'photo.png', size: 3, mime: 'image/png', state: 'staged', sent: 0, error: '', ...over,
})

// paste simulates a ClipboardEvent with the shape the desktop/browser paste
// handlers switch on: text/uri-list (hidden or with a path list), a bare
// image (no text/plain among types), or real File objects (browser mode).
function paste(box: HTMLElement, opts: { types: string[]; uriList?: string; files?: File[] }) {
  const clipboardData = {
    types: opts.types,
    getData: (fmt: string) => (fmt === 'text/uri-list' ? (opts.uriList ?? '') : ''),
    files: opts.files ?? [],
  }
  return fireEvent.paste(box, { clipboardData })
}

beforeEach(() => {
  setLocale('en')
  vi.mocked(isDesktop).mockReset().mockReturnValue(false)
  vi.mocked(pickAttachments).mockReset()
  vi.mocked(attachFromClipboard).mockReset()
  vi.mocked(uploadAttachments).mockReset()
  vi.mocked(removeAttachment).mockReset()
  vi.mocked(retryAttachment).mockReset()
  useStore.setState({ attachError: null })
})
afterEach(() => vi.useRealTimers())

test('Enter sends and clears, Shift+Enter makes a new line, blank is not sent', async () => {
  const onSend = vi.fn().mockResolvedValue(undefined)
  render(<Composer channel={channel()} serverId={1} attachments={[]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  expect(box).toHaveAttribute('placeholder', 'Write to Off-Topic')
  await userEvent.type(box, '   {Enter}')
  expect(onSend).not.toHaveBeenCalled()
  await userEvent.clear(box)
  await userEvent.type(box, 'line one{Shift>}{Enter}{/Shift}line two{Enter}')
  expect(onSend).toHaveBeenCalledWith('line one\nline two', [])
  expect(box).toHaveValue('')
})

test('Enter with attachments but no text still sends (files only)', async () => {
  const onSend = vi.fn().mockResolvedValue(undefined)
  render(<Composer channel={channel()} serverId={1} attachments={[av({ id: 'a1' }), av({ id: 'a2' })]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '{Enter}')
  expect(onSend).toHaveBeenCalledWith('', ['a1', 'a2'])
})

// Enter twice before the event: attachments_changed (confirming a send took
// the chips) is coalesced up to ~100ms, so a second Enter can land while
// `attachments` (props, from the store) still lists the ones the first
// Enter just sent. Composer must not resend them — Go's Take rejects the
// whole call with not_found if it does, bouncing the new text back too.
test('Enter twice before the event: a second Enter with new text sends the text only, not the already-sent attachment', async () => {
  const onSend = vi.fn().mockResolvedValue(undefined)
  render(<Composer channel={channel()} serverId={1} attachments={[av({ id: 'a1' })]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'first{Enter}')
  expect(onSend).toHaveBeenNthCalledWith(1, 'first', ['a1'])
  // `attachments` prop is unchanged (as if the event hasn't arrived yet).
  await userEvent.type(box, 'second{Enter}')
  expect(onSend).toHaveBeenNthCalledWith(2, 'second', [])
})

test('Enter twice before the event: an empty second Enter (nothing but the already-sent attachment) sends nothing', async () => {
  const onSend = vi.fn().mockResolvedValue(undefined)
  render(<Composer channel={channel()} serverId={1} attachments={[av({ id: 'a1' })]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '{Enter}')
  expect(onSend).toHaveBeenCalledTimes(1)
  await userEvent.type(box, '{Enter}')
  expect(onSend).toHaveBeenCalledTimes(1) // no new text, and a1 is already in flight
})

test('Enter twice before the event: a failed send makes its attachment sendable again on the next Enter', async () => {
  const onSend = vi.fn().mockRejectedValueOnce(new ApiError('not_found', '')).mockResolvedValue(undefined)
  render(<Composer channel={channel()} serverId={1} attachments={[av({ id: 'a1' })]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '{Enter}')
  await screen.findByRole('alert')
  expect(onSend).toHaveBeenNthCalledWith(1, '', ['a1'])
  await userEvent.type(box, '{Enter}')
  expect(onSend).toHaveBeenNthCalledWith(2, '', ['a1'])
})

test('a refused send puts the text back with the error', async () => {
  const onSend = vi.fn().mockRejectedValue(new ApiError('session_expired', ''))
  render(<Composer channel={channel()} serverId={1} attachments={[]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'hello{Enter}')
  expect(await screen.findByRole('alert')).toHaveTextContent('Session expired — sign in again')
  expect(box).toHaveValue('hello')
})

test('a refused send persists the restored text as the draft', async () => {
  const onSend = vi.fn().mockRejectedValue(new ApiError('session_expired', ''))
  const onDraft = vi.fn()
  render(<Composer channel={channel()} serverId={1} attachments={[]} onSend={onSend} onDraft={onDraft} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'hello{Enter}')
  await screen.findByRole('alert')
  // The failed send already cleared the server-side draft (empty) before
  // trying to send; the restored text must be re-persisted right away, not
  // left to the next keystroke's debounce or to leaving the channel.
  expect(onDraft).toHaveBeenLastCalledWith('hello')
})

test('ArrowUp in an empty box edits the last own post', async () => {
  const onEditLast = vi.fn()
  render(<Composer channel={channel()} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={onEditLast} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'x{ArrowUp}')
  expect(onEditLast).not.toHaveBeenCalled()
  await userEvent.clear(box)
  await userEvent.type(box, '{ArrowUp}')
  expect(onEditLast).toHaveBeenCalledTimes(1)
})

test('draft: restored, saved 500 ms after typing, flushed when leaving the channel', async () => {
  vi.useFakeTimers()
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const onDraft = vi.fn()
  const { unmount } = render(
    <Composer channel={channel({ draft: 'saved' })} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={onDraft} onEditLast={() => {}} />,
  )
  const box = screen.getByRole('textbox', { name: 'Message' })
  expect(box).toHaveValue('saved')
  await user.type(box, '!')
  expect(onDraft).not.toHaveBeenCalled()
  await act(async () => vi.advanceTimersByTime(500))
  expect(onDraft).toHaveBeenLastCalledWith('saved!')
  await user.type(box, '?')
  unmount()
  expect(onDraft).toHaveBeenLastCalledWith('saved!?')
})

test('the tray renders the composer\'s attachments and wires remove/retry to serverId', async () => {
  const user = userEvent.setup()
  render(
    <Composer channel={channel()} serverId={7} attachments={[av({ id: 'a1', name: 'x.png' })]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />,
  )
  expect(screen.getByText('x.png')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Remove x.png' }))
  expect(removeAttachment).toHaveBeenCalledWith(7, 'a1')
})

test('📎 in desktop mode calls pickAttachments; in browser mode it opens the hidden file input', async () => {
  const user = userEvent.setup()
  vi.mocked(isDesktop).mockReturnValue(true)
  const { unmount } = render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  await user.click(screen.getByRole('button', { name: 'Attach files' }))
  expect(pickAttachments).toHaveBeenCalledWith(3, 'c1', '')
  unmount()

  vi.mocked(isDesktop).mockReturnValue(false)
  render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const input = document.querySelector('input[type="file"]') as HTMLInputElement
  const clickSpy = vi.spyOn(input, 'click')
  await user.click(screen.getByRole('button', { name: 'Attach files' }))
  expect(clickSpy).toHaveBeenCalled()
})

test('browser mode: choosing files in the hidden input uploads them', () => {
  render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const input = document.querySelector('input[type="file"]') as HTMLInputElement
  const file = new File(['x'], 'x.png', { type: 'image/png' })
  vi.mocked(uploadAttachments).mockResolvedValue(undefined)
  fireEvent.change(input, { target: { files: [file] } })
  expect(uploadAttachments).toHaveBeenCalledWith(3, 'c1', [file], '')
})

test('desktop paste: a hidden file list (text/uri-list present but empty) attaches from the clipboard and prevents default', async () => {
  vi.mocked(isDesktop).mockReturnValue(true)
  vi.mocked(attachFromClipboard).mockResolvedValue(1)
  render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  const notPrevented = paste(box, { types: ['text/uri-list'], uriList: '' })
  expect(notPrevented).toBe(false) // preventDefault was called
  expect(attachFromClipboard).toHaveBeenCalledWith(3, 'c1', '')
})

test('desktop paste: a bare image (no text/plain) attaches from the clipboard without preventing default', () => {
  vi.mocked(isDesktop).mockReturnValue(true)
  vi.mocked(attachFromClipboard).mockResolvedValue(1)
  render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  const notPrevented = paste(box, { types: ['image/png'] })
  expect(notPrevented).toBe(true) // default paste still allowed to proceed
  expect(attachFromClipboard).toHaveBeenCalledWith(3, 'c1', '')
})

test('desktop paste: normal text/link paste is not intercepted', () => {
  vi.mocked(isDesktop).mockReturnValue(true)
  render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  paste(box, { types: ['text/plain'] })
  expect(attachFromClipboard).not.toHaveBeenCalled()
})

test('browser mode: pasted files upload each one and prevent default', () => {
  vi.mocked(isDesktop).mockReturnValue(false)
  vi.mocked(uploadAttachments).mockResolvedValue(undefined)
  render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  const file = new File(['x'], 'x.png', { type: 'image/png' })
  const notPrevented = paste(box, { types: ['Files'], files: [file] })
  expect(notPrevented).toBe(false)
  expect(uploadAttachments).toHaveBeenCalledWith(3, 'c1', [file], '')
})

test('an attach error is shown like a send error, quietly ignored for no_paste_gesture', async () => {
  const user = userEvent.setup()
  vi.mocked(isDesktop).mockReturnValue(true)
  vi.mocked(pickAttachments).mockRejectedValue(new ApiError('too_many', ''))
  render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  await user.click(screen.getByRole('button', { name: 'Attach files' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Too many attachments (10 at most)')

  vi.mocked(pickAttachments).mockRejectedValue(new ApiError('no_paste_gesture', ''))
  await user.click(screen.getByRole('button', { name: 'Attach files' }))
  await vi.waitFor(() => expect(useStore.getState().attachError).toBeNull())
})

// Desktop never sends attachment bytes at all — Go reads the clipboard,
// the dialog and the drop itself (AGENTS.md "Вложения"/"Things that bite":
// a Blob/File/FormData body in a fetch to wails:// crashes the whole app).
// These two tests prove the UI-side half of that rule structurally, not by
// trusting that WebKitGTK never hands the page real files.
test('desktop mode never renders the file input, so 📎 cannot reach a File-based upload path', () => {
  vi.mocked(isDesktop).mockReturnValue(true)
  render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  expect(document.querySelector('input[type="file"]')).toBeNull()
})

test('desktop mode ignores clipboardData.files even if a paste event carried them (only attachFromClipboard is ever called, never a byte-carrying upload)', () => {
  vi.mocked(isDesktop).mockReturnValue(true)
  render(<Composer channel={channel()} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  const file = new File(['x'], 'x.png', { type: 'image/png' })
  // A normal text paste that (implausibly) also carries files: desktop's
  // branch returns before ever looking at clipboardData.files.
  paste(box, { types: ['text/plain'], files: [file] })
  expect(uploadAttachments).not.toHaveBeenCalled()
  expect(attachFromClipboard).not.toHaveBeenCalled()
})
