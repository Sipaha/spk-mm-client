import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { ApiError } from '../api/client'
import type { AttachmentView, ChannelDTO } from '../api/types'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { Composer } from './Composer'

vi.mock('../api/client', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/client')>()
  return {
    ...real,
    isDesktop: vi.fn(() => false),
    client: {
      ...real.client,
      getFormattingBarHidden: vi.fn().mockResolvedValue(false),
      setFormattingBarHidden: vi.fn().mockResolvedValue(undefined),
    },
  }
})
vi.mock('../chat', () => ({
  pickAttachments: vi.fn(),
  attachFromClipboard: vi.fn(),
  uploadAttachments: vi.fn(),
  removeAttachment: vi.fn(),
  retryAttachment: vi.fn(),
}))

const { isDesktop, client } = await import('../api/client')
const { attachFromClipboard, pickAttachments, removeAttachment, retryAttachment, uploadAttachments } = await import('../chat')

const channel = (o: Partial<ChannelDTO> = {}): ChannelDTO => ({
  id: 'c1', name: 'Off-Topic', type: 'O', header: '', purpose: '', team_id: 't', team_name: 'team', posts: [],
  new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'me', crt: false, muted: false, ...o,
})

// cf: the Composer props a ChannelDTO now maps to (channelId/channelName/
// draft — see Composer's Props, generalized in Task 6 to also serve a
// thread's reply composer).
const cf = (ch: ChannelDTO) => ({ channelId: ch.id, channelName: ch.name, draft: ch.draft })

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

// stubResizeObserver / setOffsetWidth: jsdom has no ResizeObserver and never
// computes real layout (same gotcha Feed.test.tsx documents) — the toolbar's
// fit-to-width logic is driven by hand: install a stub that records which
// elements are observed, fake the toolbar row's offsetWidth, then fire its
// observer to trigger a recompute.
type Observed = { cb: ResizeObserverCallback; targets: Set<Element> }
function stubResizeObserver() {
  const all: Observed[] = []
  const saved = (globalThis as { ResizeObserver?: unknown }).ResizeObserver
  class Stub {
    rec: Observed = { cb: () => {}, targets: new Set() }
    constructor(cb: ResizeObserverCallback) {
      this.rec.cb = cb
      all.push(this.rec)
    }
    observe(el: Element) {
      this.rec.targets.add(el)
    }
    unobserve(el: Element) {
      this.rec.targets.delete(el)
    }
    disconnect() {
      this.rec.targets.clear()
    }
  }
  ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = Stub
  const fire = (el: Element) =>
    act(() => {
      for (const o of all) if (o.targets.has(el)) o.cb([{ target: el } as unknown as ResizeObserverEntry], o as unknown as ResizeObserver)
    })
  return { fire, restore: () => void ((globalThis as { ResizeObserver?: unknown }).ResizeObserver = saved) }
}

function setOffsetWidth(el: Element, width: number) {
  Object.defineProperty(el, 'offsetWidth', { configurable: true, value: width })
}

beforeEach(() => {
  setLocale('en')
  vi.mocked(isDesktop).mockReset().mockReturnValue(false)
  vi.mocked(pickAttachments).mockReset()
  vi.mocked(attachFromClipboard).mockReset()
  vi.mocked(uploadAttachments).mockReset()
  vi.mocked(removeAttachment).mockReset()
  vi.mocked(retryAttachment).mockReset()
  vi.mocked(client.getFormattingBarHidden).mockReset().mockResolvedValue(false)
  vi.mocked(client.setFormattingBarHidden).mockReset().mockResolvedValue(undefined)
  // formattingBarLoaded: true skips Composer's one-time fetch effect so
  // tests are deterministic without waiting on it — see composer_prefs's
  // toggle tests below for direct coverage of that fetch/persist path.
  useStore.setState({ attachError: null, threadAttachError: null, formattingBarHidden: false, formattingBarLoaded: true })
})
afterEach(() => vi.useRealTimers())

test('Enter sends and clears, Shift+Enter makes a new line, blank is not sent', async () => {
  const onSend = vi.fn().mockResolvedValue(undefined)
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
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
  render(<Composer {...cf(channel())} serverId={1} attachments={[av({ id: 'a1' }), av({ id: 'a2' })]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
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
  render(<Composer {...cf(channel())} serverId={1} attachments={[av({ id: 'a1' })]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'first{Enter}')
  expect(onSend).toHaveBeenNthCalledWith(1, 'first', ['a1'])
  // `attachments` prop is unchanged (as if the event hasn't arrived yet).
  await userEvent.type(box, 'second{Enter}')
  expect(onSend).toHaveBeenNthCalledWith(2, 'second', [])
})

test('Enter twice before the event: an empty second Enter (nothing but the already-sent attachment) sends nothing', async () => {
  const onSend = vi.fn().mockResolvedValue(undefined)
  render(<Composer {...cf(channel())} serverId={1} attachments={[av({ id: 'a1' })]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '{Enter}')
  expect(onSend).toHaveBeenCalledTimes(1)
  await userEvent.type(box, '{Enter}')
  expect(onSend).toHaveBeenCalledTimes(1) // no new text, and a1 is already in flight
})

test('Enter twice before the event: a failed send makes its attachment sendable again on the next Enter', async () => {
  const onSend = vi.fn().mockRejectedValueOnce(new ApiError('not_found', '')).mockResolvedValue(undefined)
  render(<Composer {...cf(channel())} serverId={1} attachments={[av({ id: 'a1' })]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '{Enter}')
  await screen.findByRole('alert')
  expect(onSend).toHaveBeenNthCalledWith(1, '', ['a1'])
  await userEvent.type(box, '{Enter}')
  expect(onSend).toHaveBeenNthCalledWith(2, '', ['a1'])
})

test('a refused send puts the text back with the error', async () => {
  const onSend = vi.fn().mockRejectedValue(new ApiError('session_expired', ''))
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'hello{Enter}')
  expect(await screen.findByRole('alert')).toHaveTextContent('Session expired — sign in again')
  expect(box).toHaveValue('hello')
})

test('a refused send persists the restored text as the draft', async () => {
  const onSend = vi.fn().mockRejectedValue(new ApiError('session_expired', ''))
  const onDraft = vi.fn()
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={onSend} onDraft={onDraft} onEditLast={() => {}} />)
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
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={onEditLast} />)
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
    <Composer {...cf(channel({ draft: 'saved' }))} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={onDraft} onEditLast={() => {}} />,
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
    <Composer {...cf(channel())} serverId={7} attachments={[av({ id: 'a1', name: 'x.png' })]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />,
  )
  expect(screen.getByText('x.png')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Remove x.png' }))
  expect(removeAttachment).toHaveBeenCalledWith(7, 'a1')
})

test('📎 in desktop mode calls pickAttachments; in browser mode it opens the hidden file input', async () => {
  const user = userEvent.setup()
  vi.mocked(isDesktop).mockReturnValue(true)
  const { unmount } = render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  await user.click(screen.getByRole('button', { name: 'Attach files' }))
  expect(pickAttachments).toHaveBeenCalledWith(3, 'c1', '')
  unmount()

  vi.mocked(isDesktop).mockReturnValue(false)
  render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const input = document.querySelector('input[type="file"]') as HTMLInputElement
  const clickSpy = vi.spyOn(input, 'click')
  await user.click(screen.getByRole('button', { name: 'Attach files' }))
  expect(clickSpy).toHaveBeenCalled()
})

test('browser mode: choosing files in the hidden input uploads them', () => {
  render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const input = document.querySelector('input[type="file"]') as HTMLInputElement
  const file = new File(['x'], 'x.png', { type: 'image/png' })
  vi.mocked(uploadAttachments).mockResolvedValue(undefined)
  fireEvent.change(input, { target: { files: [file] } })
  expect(uploadAttachments).toHaveBeenCalledWith(3, 'c1', [file], '')
})

test('desktop paste: a hidden file list (text/uri-list present but empty) attaches from the clipboard and prevents default', async () => {
  vi.mocked(isDesktop).mockReturnValue(true)
  vi.mocked(attachFromClipboard).mockResolvedValue(1)
  render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  const notPrevented = paste(box, { types: ['text/uri-list'], uriList: '' })
  expect(notPrevented).toBe(false) // preventDefault was called
  expect(attachFromClipboard).toHaveBeenCalledWith(3, 'c1', '')
})

test('desktop paste: a bare image (no text/plain) attaches from the clipboard without preventing default', () => {
  vi.mocked(isDesktop).mockReturnValue(true)
  vi.mocked(attachFromClipboard).mockResolvedValue(1)
  render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  const notPrevented = paste(box, { types: ['image/png'] })
  expect(notPrevented).toBe(true) // default paste still allowed to proceed
  expect(attachFromClipboard).toHaveBeenCalledWith(3, 'c1', '')
})

test('desktop paste: normal text/link paste is not intercepted', () => {
  vi.mocked(isDesktop).mockReturnValue(true)
  render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  paste(box, { types: ['text/plain'] })
  expect(attachFromClipboard).not.toHaveBeenCalled()
})

test('browser mode: pasted files upload each one and prevent default', () => {
  vi.mocked(isDesktop).mockReturnValue(false)
  vi.mocked(uploadAttachments).mockResolvedValue(undefined)
  render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
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
  render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
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
  render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  expect(document.querySelector('input[type="file"]')).toBeNull()
})

test('desktop mode ignores clipboardData.files even if a paste event carried them (only attachFromClipboard is ever called, never a byte-carrying upload)', () => {
  vi.mocked(isDesktop).mockReturnValue(true)
  render(<Composer {...cf(channel())} serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  const file = new File(['x'], 'x.png', { type: 'image/png' })
  // A normal text paste that (implausibly) also carries files: desktop's
  // branch returns before ever looking at clipboardData.files.
  paste(box, { types: ['text/plain'], files: [file] })
  expect(uploadAttachments).not.toHaveBeenCalled()
  expect(attachFromClipboard).not.toHaveBeenCalled()
})

// Task 6: a reply composer (rootId set) — its own placeholder, forwards
// rootId to every attachment source, and reads/writes threadAttachError
// instead of the channel's attachError.
test('a reply composer (rootId set) uses its own placeholder and forwards rootId to attachment sources', async () => {
  const user = userEvent.setup()
  vi.mocked(isDesktop).mockReturnValue(true)
  render(
    <Composer {...cf(channel())} rootId="root1" serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />,
  )
  const box = screen.getByRole('textbox', { name: 'Message' })
  expect(box).toHaveAttribute('placeholder', 'Reply')
  await user.click(screen.getByRole('button', { name: 'Attach files' }))
  expect(pickAttachments).toHaveBeenCalledWith(3, 'c1', 'root1')
})

test('a reply composer shows and clears threadAttachError, not the channel attachError', async () => {
  const user = userEvent.setup()
  vi.mocked(isDesktop).mockReturnValue(true)
  vi.mocked(pickAttachments).mockRejectedValue(new ApiError('too_many', ''))
  render(
    <Composer {...cf(channel())} rootId="root1" serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />,
  )
  await user.click(screen.getByRole('button', { name: 'Attach files' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Too many attachments (10 at most)')
  expect(useStore.getState().threadAttachError).not.toBeNull()
  expect(useStore.getState().attachError).toBeNull()
})

// Task 6: disabled (the thread's root was deleted) — shown, but inert.
test('disabled: the textarea and attach button are disabled, and Enter sends nothing', () => {
  const onSend = vi.fn()
  render(
    <Composer {...cf(channel())} rootId="root1" disabled serverId={3} attachments={[]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />,
  )
  const box = screen.getByRole('textbox', { name: 'Message' })
  expect(box).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Attach files' })).toBeDisabled()
  fireEvent.keyDown(box, { key: 'Enter' })
  expect(onSend).not.toHaveBeenCalled()
})

// Composer brief 2026-09-29: formatting toolbar, Aa toggle, emoji insert,
// send-button states.

function select(box: HTMLTextAreaElement, start: number, end: number) {
  fireEvent.select(box, {})
  box.setSelectionRange(start, end)
}

test('the formatting toolbar renders bold/italic/strike/heading/link/code/quote/list buttons by default', () => {
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const toolbar = screen.getByRole('toolbar', { name: 'Composer toolbar' })
  for (const name of ['Bold (Ctrl+B)', 'Italic (Ctrl+I)', 'Strikethrough', 'Heading', 'Link (Ctrl+Alt+K)', 'Code', 'Quote', 'Bulleted list', 'Numbered list']) {
    expect(within(toolbar).getByRole('button', { name })).toBeInTheDocument()
  }
})

test('clicking Bold wraps the current selection with **, and toggles it off when clicked again', async () => {
  const user = userEvent.setup()
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement
  await user.type(box, 'hello world')
  select(box, 6, 11) // "world"
  await user.click(screen.getByRole('button', { name: 'Bold (Ctrl+B)' }))
  expect(box).toHaveValue('hello **world**')
  select(box, 8, 13) // the now-bolded "world", between the ** markers
  await user.click(screen.getByRole('button', { name: 'Bold (Ctrl+B)' }))
  expect(box).toHaveValue('hello world')
})

test('Ctrl+B/Ctrl+I/Ctrl+Alt+K apply bold/italic/link from the keyboard (physical key, works on any layout)', async () => {
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement
  fireEvent.change(box, { target: { value: 'word' } })
  select(box, 0, 4)
  fireEvent.keyDown(box, { code: 'KeyB', ctrlKey: true })
  expect(box).toHaveValue('**word**')
  fireEvent.change(box, { target: { value: 'word' } })
  select(box, 0, 4)
  fireEvent.keyDown(box, { code: 'KeyI', ctrlKey: true })
  expect(box).toHaveValue('*word*')
  fireEvent.change(box, { target: { value: '' } })
  select(box, 0, 0)
  fireEvent.keyDown(box, { code: 'KeyK', ctrlKey: true, altKey: true })
  expect(box).toHaveValue('[text](url)')
})

test('Aa hides the formatting buttons, persists the choice, and shows them again', async () => {
  const user = userEvent.setup()
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  expect(screen.getByRole('button', { name: 'Bold (Ctrl+B)' })).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Hide formatting' }))
  expect(screen.queryByRole('button', { name: 'Bold (Ctrl+B)' })).toBeNull()
  expect(client.setFormattingBarHidden).toHaveBeenCalledWith(true)
  expect(useStore.getState().formattingBarHidden).toBe(true)
  await user.click(screen.getByRole('button', { name: 'Show formatting' }))
  expect(screen.getByRole('button', { name: 'Bold (Ctrl+B)' })).toBeInTheDocument()
  expect(client.setFormattingBarHidden).toHaveBeenCalledWith(false)
})

test('a fresh app session reads the saved Aa state once', async () => {
  useStore.setState({ formattingBarHidden: false, formattingBarLoaded: false })
  vi.mocked(client.getFormattingBarHidden).mockResolvedValue(true)
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  await vi.waitFor(() => expect(screen.queryByRole('button', { name: 'Bold (Ctrl+B)' })).toBeNull())
  expect(client.getFormattingBarHidden).toHaveBeenCalled()
})

test('the send button is dim/disabled when empty, active once there is text or an attachment, and sends on click', async () => {
  const user = userEvent.setup()
  const onSend = vi.fn().mockResolvedValue(undefined)
  const { rerender } = render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  const sendBtn = screen.getByRole('button', { name: 'Send message' })
  expect(sendBtn).toBeDisabled()
  const box = screen.getByRole('textbox', { name: 'Message' })
  await user.type(box, 'hi')
  expect(sendBtn).toBeEnabled()
  await user.click(sendBtn)
  expect(onSend).toHaveBeenCalledWith('hi', [])

  rerender(<Composer {...cf(channel())} serverId={1} attachments={[av({ id: 'a1' })]} onSend={onSend} onDraft={() => {}} onEditLast={() => {}} />)
  expect(screen.getByRole('button', { name: 'Send message' })).toBeEnabled()
})

test('the emoji button opens the picker, and picking one inserts :name: at the caret and refocuses the textarea', async () => {
  const user = userEvent.setup()
  const info = { recent: [], custom: [], custom_enabled: false }
  const emojiInfo = vi.fn().mockResolvedValue(info)
  render(<Composer {...cf(channel())} serverId={1} attachments={[]} emojiInfo={emojiInfo} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement
  await user.type(box, 'say hi')
  await user.click(screen.getByRole('button', { name: 'Emoji' }))
  const dialog = await screen.findByRole('dialog', { name: 'Emoji picker' })
  await user.type(within(dialog).getByRole('textbox', { name: 'Search emoji' }), 'grinning')
  await user.keyboard('{ArrowDown}{Enter}')
  expect(screen.queryByRole('dialog', { name: 'Emoji picker' })).toBeNull()
  expect(box.value).toContain(':grinning:')
  expect(box).toHaveFocus()
})

// Task 6/brief parity: the thread (reply) composer gets the same toolbar.
test('a reply composer (rootId set) also has the formatting toolbar', () => {
  render(
    <Composer {...cf(channel())} rootId="root1" serverId={3} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />,
  )
  expect(screen.getByRole('button', { name: 'Bold (Ctrl+B)' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Send message' })).toBeInTheDocument()
})

// Coordinator ruling 2026-09-29 (composer brief follow-up): the thread
// panel is too narrow for all 9 formatting buttons — they must collapse
// into a "more formatting" popover, not spill into a horizontal scrollbar.
test('a narrow toolbar row collapses buttons into "More formatting options", with no scrollbar wrapper', () => {
  const ro = stubResizeObserver()
  try {
    render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
    const toolbar = screen.getByRole('toolbar', { name: 'Composer toolbar' })
    setOffsetWidth(toolbar, 200) // narrow: only "Bold" fits alongside the always-visible right group
    ro.fire(toolbar)
    expect(within(toolbar).queryByRole('button', { name: 'Numbered list' })).toBeNull()
    expect(within(toolbar).getByRole('button', { name: 'More formatting options' })).toBeInTheDocument()
    expect(toolbar.querySelector('.overflow-x-auto')).toBeNull()
    // The right group is never the one that gives way.
    expect(within(toolbar).getByRole('button', { name: 'Send message' })).toBeInTheDocument()
  } finally {
    ro.restore()
  }
})

test('a wide toolbar row shows every button with no "more" button', () => {
  const ro = stubResizeObserver()
  try {
    render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
    const toolbar = screen.getByRole('toolbar', { name: 'Composer toolbar' })
    setOffsetWidth(toolbar, 1000)
    ro.fire(toolbar)
    expect(screen.queryByRole('button', { name: 'More formatting options' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Numbered list' })).toBeInTheDocument()
  } finally {
    ro.restore()
  }
})

test('the "more formatting" popover lists the collapsed buttons; picking one applies it, closes the popover and refocuses the textarea', async () => {
  const user = userEvent.setup()
  const ro = stubResizeObserver()
  try {
    render(<Composer {...cf(channel())} serverId={1} attachments={[]} onSend={vi.fn()} onDraft={() => {}} onEditLast={() => {}} />)
    const toolbar = screen.getByRole('toolbar', { name: 'Composer toolbar' })
    setOffsetWidth(toolbar, 200)
    ro.fire(toolbar)
    const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement
    await user.type(box, 'word')
    box.setSelectionRange(0, 4)
    await user.click(screen.getByRole('button', { name: 'More formatting options' }))
    const menu = await screen.findByRole('menu', { name: 'More formatting options' })
    expect(within(menu).getAllByRole('menuitem').map((b) => b.textContent)).toEqual([
      'Italic (Ctrl+I)', 'Strikethrough', 'Heading', 'Link (Ctrl+Alt+K)', 'Code', 'Quote', 'Bulleted list', 'Numbered list',
    ])
    await user.click(within(menu).getByRole('menuitem', { name: 'Quote' }))
    expect(screen.queryByRole('menu')).toBeNull()
    expect(box).toHaveValue('> word')
    expect(box).toHaveFocus()
  } finally {
    ro.restore()
  }
})
