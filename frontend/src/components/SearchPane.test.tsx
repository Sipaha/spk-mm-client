import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { SearchHit, ServerDTO } from '../api/types'
import { setLocale } from '../i18n'
import { useStore, type SearchSession } from '../store'

vi.mock('../chat', () => ({
  closeSearch: vi.fn(), emojiInfo: vi.fn().mockResolvedValue({ recent: [], custom: [], custom_enabled: false }),
  loadMoreSearch: vi.fn().mockResolvedValue(undefined), openHit: vi.fn().mockResolvedValue(undefined), openLink: vi.fn(),
  retrySearch: vi.fn().mockResolvedValue(undefined), submitSearch: vi.fn().mockResolvedValue(undefined),
}))
vi.mock('../api/client', () => ({
  client: { searchSuggest: vi.fn() },
  ApiError: class ApiError extends Error {
    constructor(public code: string, public detail: string) {
      super(code)
    }
  },
}))

const chat = await import('../chat')
const { SearchPane } = await import('./SearchPane')

const DAY = 86_400_000
const now = new Date(2026, 8, 30, 12).getTime()
const server: ServerDTO = {
  id: 1, name: 'Acme', url: 'https://mm', signed_in: true, username: 'alice', gitlab: false, state: 'live', unread: false, mentions: 0,
}
const hit = (id: string, o: Partial<SearchHit> = {}): SearchHit => ({
  id, user_id: 'u-bob', author: 'bob', message: `привет мир ${id}`, create_at: now, channel_id: 'c-town', channel_name: 'town-square',
  channel_display: 'Town Square', channel_type: 'O', jumpable: true, matches: [], ...o,
})
const session = (o: Partial<SearchSession> = {}): SearchSession => ({
  serverId: 1, teamId: 't1', submitted: 'привет', terms: ['привет'], hits: [], page: 1, hasNext: false, limitReached: false,
  loading: false, error: null, gen: 7, anchor: null, ...o,
})
const wide = (matches: boolean) => {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn(),
  })) as unknown as typeof window.matchMedia
}
const show = (s: SearchSession) => {
  useStore.setState({ search: s, rhs: 'search' })
  return render(<SearchPane server={server} search={s} />)
}

beforeEach(() => {
  setLocale('en')
  wide(false)
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(now)
  for (const f of [chat.closeSearch, chat.loadMoreSearch, chat.openHit, chat.retrySearch]) vi.mocked(f).mockClear()
  useStore.setState({ selectedId: 1, sidebar: { team_id: 't1', selected_channel_id: '', teams: [], categories: [] }, searchDraft: '' })
})
afterEach(() => vi.useRealTimers())

test('a complementary panel titled with the query: day separators, hits with the words marked, the channel as in: names it', () => {
  show(session({ hits: [hit('h1'), hit('h2', { create_at: now - DAY, channel_type: 'D', channel_name: 'carol', channel_display: 'carol' })] }))
  const pane = screen.getByRole('complementary', { name: 'Search results' })
  expect(within(pane).getByRole('heading')).toHaveTextContent('Search results · привет')
  expect(within(pane).getByText('Today')).toBeInTheDocument()
  expect(within(pane).getByText('Yesterday')).toBeInTheDocument()
  expect([...pane.querySelectorAll('mark')].map((m) => m.textContent)).toEqual(['привет', 'привет'])
  expect(within(pane).getByText('~town-square')).toBeInTheDocument()
  expect(within(pane).getByText('@carol')).toBeInTheDocument()
})

test("a hit's own matches are highlighted instead of the query's words", () => {
  show(session({ hits: [hit('h1', { message: 'Hellos and привет', matches: ['Hellos'] })] }))
  expect([...document.querySelectorAll('mark')].map((m) => m.textContent)).toEqual(['Hellos'])
})

test('a card shows files by name and the reply count', () => {
  show(session({ hits: [hit('h1', { reply_count: 3, files: [{ id: 'f1', name: 'a.pdf', size: 1, mime: 'application/pdf' }, { id: 'f2', name: 'b.png', size: 1, mime: 'image/png' }] })] }))
  expect(screen.getByText('a.pdf, b.png')).toBeInTheDocument()
  expect(screen.getByText('Replies: 3')).toBeInTheDocument()
})

test('states: searching, nothing found, a failure with Retry, offline, the page limit', async () => {
  const { rerender } = show(session({ loading: true }))
  expect(screen.getByRole('status')).toHaveTextContent('Searching…')
  rerender(<SearchPane server={server} search={session()} />)
  expect(screen.getByRole('status')).toHaveTextContent('No results')
  rerender(<SearchPane server={server} search={session({ error: 'internal' })} />)
  expect(screen.getByRole('alert')).toHaveTextContent("Couldn't search")
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(chat.retrySearch).toHaveBeenCalled()
  rerender(<SearchPane server={server} search={session({ error: 'offline' })} />)
  expect(screen.getByRole('alert')).toHaveTextContent('No connection to the server')
  rerender(<SearchPane server={server} search={session({ hits: [hit('h1')], limitReached: true })} />)
  expect(screen.getByRole('status')).toHaveTextContent('Showing the first results — refine your search')
  rerender(<SearchPane server={server} search={session({ hits: [hit('h1')], hasNext: true, error: 'offline' })} />)
  expect(screen.getByText('привет', { selector: 'mark' })).toBeInTheDocument() // the hits stay
  expect(screen.getByRole('alert')).toHaveTextContent('No connection')
})

test('the next page is asked as the list nears its end', () => {
  show(session({ hits: [hit('h1')], hasNext: true }))
  const list = document.querySelector<HTMLElement>('[data-search-results]')!
  vi.mocked(chat.loadMoreSearch).mockClear()
  Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 5000 })
  Object.defineProperty(list, 'clientHeight', { configurable: true, value: 500 })
  list.scrollTop = 1000
  fireEvent.scroll(list)
  expect(chat.loadMoreSearch).not.toHaveBeenCalled()
  list.scrollTop = 4200
  fireEvent.scroll(list)
  expect(chat.loadMoreSearch).toHaveBeenCalledTimes(1)
})

describe('position', () => {
  const saved = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetTop')
  beforeAll(() => {
    Object.defineProperty(HTMLElement.prototype, 'offsetTop', {
      configurable: true,
      get() {
        const id = (this as HTMLElement).dataset.hitId
        return id ? Number(id.slice(1)) * 100 : 0
      },
    })
  })
  afterAll(() => {
    if (saved) Object.defineProperty(HTMLElement.prototype, 'offsetTop', saved)
  })

  test('back to the results: the list comes back to its anchor; scrolling keeps the anchor in the session', () => {
    vi.useRealTimers()
    const hits = [hit('h1'), hit('h2'), hit('h3'), hit('h4')]
    const { unmount } = show(session({ hits, anchor: { id: 'h3', offset: -20 } }))
    const list = document.querySelector<HTMLElement>('[data-search-results]')!
    expect(list.scrollTop).toBe(320)
    list.scrollTop = 190 // h1 (at 100, jsdom: no height) is above: h2 at 200 is the first on screen
    fireEvent.scroll(list)
    unmount()
    expect(useStore.getState().search?.anchor).toEqual({ id: 'h2', offset: 10 })
  })
})

test('a click opens the hit; a failed jump shows in the card and the others stay', async () => {
  const { ApiError } = await import('../api/client')
  vi.mocked(chat.openHit).mockRejectedValueOnce(new ApiError('post_gone', ''))
  show(session({ hits: [hit('h1'), hit('h2')] }))
  const card = document.querySelector<HTMLElement>('[data-hit-id="h1"]')!
  await userEvent.click(within(card).getByText('bob'))
  expect(chat.openHit).toHaveBeenCalledWith(expect.objectContaining({ id: 'h1' }))
  expect(await within(card).findByRole('alert')).toHaveTextContent('The message was deleted or is not available')
  expect(document.querySelector('[data-hit-id="h2"]')).not.toBeNull()
  card.focus()
  await userEvent.keyboard('{Enter}')
  expect(chat.openHit).toHaveBeenCalledTimes(2)
})

test('a link in a hit opens the link, not the jump', async () => {
  show(session({ hits: [hit('h1', { message: 'see [site](https://example.com)' })] }))
  await userEvent.click(screen.getByRole('link', { name: 'site' }))
  expect(chat.openLink).toHaveBeenCalledWith('https://example.com')
  expect(chat.openHit).not.toHaveBeenCalled()
})

test('a hit of a channel not known yet cannot be opened, and says why', async () => {
  show(session({ hits: [hit('h1', { jumpable: false, channel_name: '', channel_display: '', channel_type: '' })] }))
  const card = document.querySelector<HTMLElement>('[data-hit-id="h1"]')!
  expect(card).toHaveAttribute('aria-disabled', 'true')
  await userEvent.click(within(card).getByText('bob'))
  expect(chat.openHit).not.toHaveBeenCalled()
  expect(within(card).getByText('The channel is not loaded yet — no jump')).toBeInTheDocument()
})

test('Esc closes with focus in the panel only, and not when someone took it', () => {
  show(session({ hits: [hit('h1')] }))
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(chat.closeSearch).not.toHaveBeenCalled()
  const card = document.querySelector<HTMLElement>('[data-hit-id="h1"]')!
  card.focus()
  const taken = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
  taken.preventDefault()
  window.dispatchEvent(taken)
  expect(chat.closeSearch).not.toHaveBeenCalled()
  fireEvent.keyDown(card, { key: 'Escape' })
  expect(chat.closeSearch).toHaveBeenCalledTimes(1)
})

test('× closes the search', async () => {
  show(session())
  await userEvent.click(screen.getByRole('button', { name: 'Close search' }))
  expect(chat.closeSearch).toHaveBeenCalled()
})

test('narrow: over the feed with its own query field; "Back to channel" leaves it, the session stays; an opened hit shows the feed', async () => {
  wide(true)
  show(session({ hits: [hit('h1')] }))
  const pane = screen.getByRole('complementary', { name: 'Search results' })
  expect(pane).toHaveClass('absolute', 'inset-0')
  expect(within(pane).getByRole('combobox', { name: 'Search messages' })).toHaveAttribute('data-search-input', 'pane')
  await userEvent.click(within(pane).getByRole('button', { name: 'Back to channel' }))
  expect(useStore.getState().rhs).toBeNull()
  expect(useStore.getState().search).not.toBeNull()
  act(() => useStore.setState({ rhs: 'search' }))
  vi.mocked(chat.openHit).mockResolvedValueOnce(true)
  await userEvent.click(within(pane).getByText('bob'))
  expect(chat.openHit).toHaveBeenCalled()
  expect(useStore.getState().rhs).toBeNull()
})

test('narrow: a jump ended quietly (another search, a close) does not hide the panel', async () => {
  wide(true)
  show(session({ hits: [hit('h1')] }))
  vi.mocked(chat.openHit).mockResolvedValueOnce(false)
  await userEvent.click(screen.getByText('bob'))
  expect(useStore.getState().rhs).toBe('search')
})

test('narrow: Esc in the empty query field hands focus to the panel, and the next Esc closes it', async () => {
  wide(true)
  show(session({ hits: [hit('h1')] }))
  const box = screen.getByRole('combobox', { name: 'Search messages' })
  box.focus()
  await userEvent.keyboard('{Escape}')
  expect(screen.getByRole('complementary', { name: 'Search results' })).toHaveFocus()
  expect(chat.closeSearch).not.toHaveBeenCalled()
  await userEvent.keyboard('{Escape}')
  expect(chat.closeSearch).toHaveBeenCalledTimes(1)
})

test('a card is a button; selecting its text with the mouse does not jump', async () => {
  show(session({ hits: [hit('h1')] }))
  const card = screen.getByRole('button', { name: /bob/ })
  expect(card).toHaveAttribute('data-hit-id', 'h1')
  const text = within(card).getByText('привет', { selector: 'mark' })
  const spy = vi.spyOn(window, 'getSelection').mockReturnValue({ isCollapsed: false, toString: () => 'привет', anchorNode: text.firstChild } as unknown as Selection)
  fireEvent.click(text)
  expect(chat.openHit).not.toHaveBeenCalled()
  spy.mockRestore()
  fireEvent.click(text)
  expect(chat.openHit).toHaveBeenCalledTimes(1)
})
