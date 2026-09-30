import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { PostView, ServerDTO, ThreadDTO } from '../api/types'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { ThreadPane } from './ThreadPane'

// jsdom has no layout: the Feed's virtualizer needs sizes to render at all
// (same stubs as Feed.test.tsx/ChannelPane.test.tsx).
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
  copyLink: vi.fn(), deletePost: vi.fn(), discardPost: vi.fn(), downloadFile: vi.fn(), editLastOwn: vi.fn(),
  editPost: vi.fn(), emojiInfo: vi.fn().mockResolvedValue({ recent: [], custom: [], custom_enabled: false }),
  loadOlderReplies: vi.fn().mockResolvedValue(true), markUnread: vi.fn(), openFile: vi.fn(), openLink: vi.fn(),
  openThread: vi.fn(), react: vi.fn().mockResolvedValue(undefined), reactionUsers: vi.fn().mockResolvedValue({ users: [], unknown: 0 }),
  retryPost: vi.fn(), saveThreadDraft: vi.fn(), sendReply: vi.fn().mockResolvedValue(undefined), setPostSaved: vi.fn(),
  uploadAttachments: vi.fn().mockResolvedValue(undefined),
}))

const { loadOlderReplies, openThread } = await import('../chat')

const server = (o: Partial<ServerDTO> = {}): ServerDTO => ({
  id: 5, name: 'Acme', url: 'https://mm', signed_in: true, username: 'alice', gitlab: false,
  state: 'live', unread: false, mentions: 0, ...o,
})
const post = (o: Partial<PostView> = {}): PostView => ({
  id: 'root', user_id: 'u-bob', author: 'bob', message: 'root text', create_at: Date.now() - 60_000, ...o,
})
const thread = (o: Partial<ThreadDTO> = {}): ThreadDTO => ({
  root_id: 'root', channel_id: 'c-town', channel_name: 'Town Square', team_name: 'team',
  posts: [post(), post({ id: 'r1', user_id: 'u-carol', author: 'carol', message: 'a reply', root_id: 'root', create_at: Date.now() - 30_000 })],
  has_more: false, capped: false, loaded: true, syncing: false, root_deleted: false, error: '', draft: '',
  me_id: 'u-alice', crt: false, new_since: 0, gap_after: '', focus: null, ...o,
})

beforeEach(() => {
  setLocale('en')
  useStore.setState({ editingId: null, threadAttachments: [], threadAttachError: null })
  vi.mocked(loadOlderReplies).mockReset().mockResolvedValue(true)
  vi.mocked(openThread).mockReset()
  // matchMedia stub for useNarrow — wide by default.
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: false, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn(),
  })) as unknown as typeof window.matchMedia
})

// header-markdown-brief 2026-09-30, fix round 1: the thread panel's header
// must use the same h-8/items-center as ChannelPane.tsx's so the two bars
// line up side by side when the panel is open next to the channel.
test('header: h-8, items-center, no vertical padding — matches ChannelPane.tsx\'s header height', () => {
  const { container } = render(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  const header = container.querySelector('header')!
  expect(header).toHaveClass('items-center', 'h-8')
  expect(header).not.toHaveClass('py-2', 'py-1.5')
})

// Fix round 2 (review): the non-narrow panel must establish its own
// positioning context (`relative`), like ChannelPane's <section> does —
// otherwise the drop-target overlay (`.file-drop-target-active::after`,
// `absolute inset-0`) positions against whatever ancestor App.tsx happens to
// give it (its whole content row) instead of the 420px panel. jsdom has no
// layout, so this only checks for the class that creates the containing
// block, not the resulting geometry — see the real-browser proof in the
// report for the actual bounding box.
test('the panel (non-narrow) is a positioned ancestor for its drop-target overlay', () => {
  const { container } = render(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  expect(target).toHaveClass('relative')
})

// Task 7 reviewer follow-up: the panel's <Feed> had no per-thread key, so
// opening a different thread while the panel stayed open reused the same
// Feed instance — carrying over its scroll position and its spent
// "auto-load more" budget (fillViewportIfShort/ready.current) into a thread
// that has nothing to do with either. Keying it by (channel, root), like the
// Composer already is, forces a fresh mount per thread.
test('switching to a different thread remounts the feed (fresh scroll/load state), not reusing the old instance', () => {
  const { container, rerender } = render(<ThreadPane server={server()} thread={thread({ root_id: 'root' })} onClose={() => {}} />)
  const firstLog = container.querySelector('[data-feed="thread"]')
  expect(firstLog).not.toBeNull()

  rerender(
    <ThreadPane
      server={server()}
      thread={thread({
        root_id: 'root2',
        posts: [post({ id: 'root2', message: 'another thread' })],
      })}
      onClose={() => {}}
    />,
  )
  const secondLog = container.querySelector('[data-feed="thread"]')
  expect(secondLog).not.toBeNull()
  expect(secondLog).not.toBe(firstLog) // a fresh DOM node — the old Feed unmounted
  expect(firstLog!.isConnected).toBe(false)
})

// Companion guard: a content refresh of the *same* thread (e.g. a new reply
// arriving via thread_changed) must NOT remount the feed — that would lose
// the user's scroll position on every live update, not just on switching
// threads.
test('refreshing the same open thread (new posts, same root) keeps the same feed instance', () => {
  const { container, rerender } = render(<ThreadPane server={server()} thread={thread({ root_id: 'root' })} onClose={() => {}} />)
  const firstLog = container.querySelector('[data-feed="thread"]')

  rerender(
    <ThreadPane
      server={server()}
      thread={thread({
        root_id: 'root',
        posts: [post(), post({ id: 'r1', root_id: 'root' }), post({ id: 'r2', root_id: 'root', message: 'new reply' })],
      })}
      onClose={() => {}}
    />,
  )
  const secondLog = container.querySelector('[data-feed="thread"]')
  expect(secondLog).toBe(firstLog) // same instance: no remount for an ordinary refresh
})

test('the panel shows the root, its replies, the "N replies" divider, and a composer', () => {
  render(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  expect(screen.getByRole('complementary', { name: 'Thread' })).toBeInTheDocument()
  expect(screen.getByText('Thread · Town Square')).toBeInTheDocument()
  expect(screen.getByText('root text')).toBeInTheDocument()
  expect(screen.getByText('a reply')).toBeInTheDocument()
  expect(screen.getByText('Replies: 1')).toBeInTheDocument()
  expect(screen.getByRole('textbox', { name: 'Message' })).toBeInTheDocument()
})

test('the composer sends a reply via sendReply', async () => {
  const { sendReply } = await import('../chat')
  render(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'my reply{Enter}')
  expect(sendReply).toHaveBeenCalledWith(5, 'c-town', 'root', 'my reply', [])
})

test('"×" and Esc (focus in the panel) close the panel and return focus to the channel feed', async () => {
  const onClose = vi.fn()
  render(
    <>
      <div role="log" aria-label="Messages" data-feed="channel" tabIndex={-1} />
      <ThreadPane server={server()} thread={thread()} onClose={onClose} />
    </>,
  )
  const channelFeed = document.querySelector('[data-feed="channel"]')!
  await userEvent.click(screen.getByRole('button', { name: 'Close thread' }))
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(channelFeed).toHaveFocus()

  onClose.mockClear()
  const box = screen.getByRole('textbox', { name: 'Message' })
  box.focus()
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(channelFeed).toHaveFocus()
})

// Task 6 brief ("при открытии фокус — в композер панели"): opening a
// thread — from "N replies", the reply button, a notification — puts the
// caret in the panel's composer; opening another thread in the same panel
// does it again. A later re-render of the same thread (a live reply) must
// not steal focus back from wherever the user moved it.
test('opening a thread focuses the panel composer; a re-render of the same thread does not steal focus', () => {
  const { rerender } = render(
    <>
      <button>elsewhere</button>
      <ThreadPane server={server()} thread={thread()} onClose={() => {}} />
    </>,
  )
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveFocus()

  screen.getByRole('button', { name: 'elsewhere' }).focus()
  rerender(
    <>
      <button>elsewhere</button>
      <ThreadPane server={server()} thread={thread({ posts: [...thread().posts, post({ id: 'r2', root_id: 'root', message: 'live' })] })} onClose={() => {}} />
    </>,
  )
  expect(screen.getByRole('button', { name: 'elsewhere' })).toHaveFocus()

  rerender(
    <>
      <button>elsewhere</button>
      <ThreadPane server={server()} thread={thread({ root_id: 'other', posts: [post({ id: 'other' })] })} onClose={() => {}} />
    </>,
  )
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveFocus()
})

test('Esc is ignored while focus is outside the panel (e.g. a portalled popover)', async () => {
  const onClose = vi.fn()
  render(
    <>
      <button>elsewhere</button>
      <ThreadPane server={server()} thread={thread()} onClose={onClose} />
    </>,
  )
  screen.getByRole('button', { name: 'elsewhere' }).focus()
  await userEvent.keyboard('{Escape}')
  expect(onClose).not.toHaveBeenCalled()
})

test('root_deleted shows a banner and disables the composer', () => {
  render(<ThreadPane server={server()} thread={thread({ root_deleted: true })} onClose={() => {}} />)
  expect(screen.getByText('The original message was deleted')).toBeInTheDocument()
  expect(screen.getByRole('textbox', { name: 'Message' })).toBeDisabled()
})

test('capped shows the "showing the last 200 replies" banner with an "open in browser" link', async () => {
  const { openLink } = await import('../chat')
  render(<ThreadPane server={server()} thread={thread({ capped: true })} onClose={() => {}} />)
  expect(screen.getByText('Showing the last 200 replies')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Open in browser' }))
  expect(openLink).toHaveBeenCalledWith('https://mm/team/pl/root')
})

test('a load error shows "Retry", which re-opens the thread', async () => {
  render(<ThreadPane server={server()} thread={thread({ error: 'internal' })} onClose={() => {}} />)
  expect(screen.getByText("Couldn't load the thread")).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(openThread).toHaveBeenCalledWith(5, 'c-town', 'root')
})

test('scrolling to the top of the thread calls loadOlderReplies', () => {
  render(<ThreadPane server={server()} thread={thread({ has_more: true })} onClose={() => {}} />)
  const log = screen.getByRole('log')
  log.scrollTop = 0
  fireEvent.scroll(log)
  expect(loadOlderReplies).toHaveBeenCalledWith(5, 'root')
})

test('narrow window: shows the "back to channel" button, which also closes the panel', async () => {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: true, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn(),
  })) as unknown as typeof window.matchMedia
  const onClose = vi.fn()
  render(<ThreadPane server={server()} thread={thread()} onClose={onClose} />)
  await userEvent.click(screen.getByRole('button', { name: 'Back to channel' }))
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('unmounting the panel leaves no dangling Escape listener', async () => {
  const onClose = vi.fn()
  const { unmount } = render(<ThreadPane server={server()} thread={thread()} onClose={onClose} />)
  unmount()
  await userEvent.keyboard('{Escape}')
  expect(onClose).not.toHaveBeenCalled()
})

test('the drop target carries data-srv/data-channel/data-root for the thread', () => {
  const { container } = render(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  expect(target).toHaveAttribute('data-srv', '5')
  expect(target).toHaveAttribute('data-channel', 'c-town')
  expect(target).toHaveAttribute('data-root', 'root')
})

test('dropping a file uploads it to the thread (rootId set)', async () => {
  const { uploadAttachments } = await import('../chat')
  const { container } = render(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  const file = new File(['x'], 'x.png', { type: 'image/png' })
  fireEvent.drop(target, { dataTransfer: { types: ['Files'], files: [file] } })
  await waitFor(() => expect(uploadAttachments).toHaveBeenCalledWith(5, 'c-town', [file], 'root'))
})

// Fix round 1 (review, Minor 1): the drop target must cover the whole panel
// (feed + composer), like ChannelPane's covers its whole feed+composer
// section, not just the composer strip — a drop anywhere over the replies
// should attach too.
test('the drop target covers the whole panel (feed and composer both inside it), not just the composer strip', () => {
  const { container } = render(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  expect(target).toBe(container.querySelector('[role="complementary"]'))
  expect(target.querySelector('[role="log"]')).not.toBeNull()
  expect(target.querySelector('textarea')).not.toBeNull()
})

test('dragging files anywhere over the panel shows the overlay class; leaving it removes it', () => {
  const { container } = render(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  const target = container.querySelector('[data-file-drop-target]')!
  const file = new File(['x'], 'x.png', { type: 'image/png' })
  fireEvent.dragEnter(target, { dataTransfer: { types: ['Files'], files: [file] } })
  expect(target).toHaveClass('file-drop-target-active')
  fireEvent.dragLeave(target, { dataTransfer: { types: ['Files'], files: [file] } })
  expect(target).not.toHaveClass('file-drop-target-active')
})

// Fix round 1 (review, Minor 2): onClose must be stable across re-renders —
// App.tsx passes a fresh closure on every render (no selector), so the Esc
// listener effect must not tear down/re-add the global keydown listener
// each time.
test('the Esc listener is not rebound when onClose changes identity across re-renders', () => {
  const addSpy = vi.spyOn(window, 'addEventListener')
  const removeSpy = vi.spyOn(window, 'removeEventListener')
  const { rerender } = render(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  const keydownAddsAfterMount = addSpy.mock.calls.filter((c) => c[0] === 'keydown').length
  expect(keydownAddsAfterMount).toBe(1)
  addSpy.mockClear()
  removeSpy.mockClear()
  for (let i = 0; i < 3; i++) rerender(<ThreadPane server={server()} thread={thread()} onClose={() => {}} />)
  expect(addSpy.mock.calls.filter((c) => c[0] === 'keydown')).toHaveLength(0)
  expect(removeSpy.mock.calls.filter((c) => c[0] === 'keydown')).toHaveLength(0)
  addSpy.mockRestore()
  removeSpy.mockRestore()
})

test('Esc still calls the latest onClose even though the listener was bound to an earlier one', async () => {
  const first = vi.fn()
  const second = vi.fn()
  const { rerender } = render(<ThreadPane server={server()} thread={thread()} onClose={first} />)
  rerender(<ThreadPane server={server()} thread={thread()} onClose={second} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  box.focus()
  await userEvent.keyboard('{Escape}')
  expect(second).toHaveBeenCalledTimes(1)
  expect(first).not.toHaveBeenCalled()
})
