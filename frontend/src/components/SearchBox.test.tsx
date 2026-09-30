import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { AutocompleteDTO } from '../api/types'
import { setLocale } from '../i18n'
import { useStore } from '../store'

vi.mock('../api/client', () => ({ client: { searchSuggest: vi.fn() } }))
vi.mock('../chat', () => ({ submitSearch: vi.fn().mockResolvedValue(undefined) }))

const { client } = await import('../api/client')
const { submitSearch } = await import('../chat')
const { SearchBox, findSearchToken } = await import('./SearchBox')

const dto = (o: Partial<AutocompleteDTO>): AutocompleteDTO => ({ users: [], others: [], channels: [], emoji: [], commands: [], ...o })

beforeEach(() => {
  setLocale('en')
  useStore.setState({
    selectedId: 1, searchDraft: '', search: null, rhs: null,
    sidebar: { team_id: 't1', selected_channel_id: '', teams: [], categories: [] },
  })
  vi.mocked(client.searchSuggest).mockReset().mockResolvedValue(dto({}))
  vi.mocked(submitSearch).mockClear()
})

const field = () => screen.getByRole('combobox', { name: 'Search messages' })

test('findSearchToken: the filter under the caret, negated too; other words are none', () => {
  expect(findSearchToken('hello from:al', 13)).toEqual({ kind: 'users', lead: 'from:', prefix: 'al', start: 6, end: 13 })
  expect(findSearchToken('-in:town x', 7)).toEqual({ kind: 'channels', lead: '-in:', prefix: 'tow', start: 0, end: 8 })
  expect(findSearchToken('channel:@a', 10)?.kind).toBe('channels')
  expect(findSearchToken('hello from:al', 3)).toBeNull()
  expect(findSearchToken('from: ', 6)).toBeNull()
})

test('Ctrl+F focuses the field by the physical key (a Russian layout too) and keeps the webview find away', () => {
  render(<SearchBox variant="header" />)
  const ev = new KeyboardEvent('keydown', { key: 'а', code: 'KeyF', ctrlKey: true, bubbles: true, cancelable: true })
  window.dispatchEvent(ev)
  expect(ev.defaultPrevented).toBe(true)
  expect(field()).toHaveFocus()
})

test('Ctrl+F is left alone while a viewer or modal is open, or when someone took it', () => {
  render(<SearchBox variant="header" />)
  const modal = document.createElement('div')
  modal.setAttribute('aria-modal', 'true')
  document.body.appendChild(modal)
  const ev = new KeyboardEvent('keydown', { key: 'f', code: 'KeyF', ctrlKey: true, bubbles: true, cancelable: true })
  window.dispatchEvent(ev)
  expect(ev.defaultPrevented).toBe(false)
  expect(field()).not.toHaveFocus()
  modal.remove()
  const taken = new KeyboardEvent('keydown', { key: 'f', code: 'KeyF', ctrlKey: true, bubbles: true, cancelable: true })
  taken.preventDefault()
  window.dispatchEvent(taken)
  expect(field()).not.toHaveFocus()
})

test('Ctrl+F goes to the results panel field when it overlays the feed', () => {
  render(
    <>
      <SearchBox variant="header" />
      <SearchBox variant="pane" />
    </>,
  )
  fireEvent.keyDown(window, { key: 'f', code: 'KeyF', ctrlKey: true })
  expect(document.activeElement).toHaveAttribute('data-search-input', 'pane')
})

test('Enter searches the text; nothing is asked of the server for plain words', async () => {
  render(<SearchBox variant="header" />)
  await userEvent.type(field(), 'привет мир{Enter}')
  expect(submitSearch).toHaveBeenCalledWith('привет мир')
  expect(client.searchSuggest).not.toHaveBeenCalled()
})

test('from: suggests the team\'s users; ↓ and Enter insert the username, and Enter does not search then', async () => {
  vi.mocked(client.searchSuggest).mockResolvedValue(
    dto({ users: [{ id: 'u1', username: 'alice' }], others: [{ id: 'u2', username: 'alvin', full_name: 'Alvin K' }] }),
  )
  render(<SearchBox variant="header" />)
  await userEvent.type(field(), 'hi from:al')
  const list = await screen.findByRole('listbox')
  expect(client.searchSuggest).toHaveBeenLastCalledWith(1, 't1', 'users', 'al', expect.any(AbortSignal))
  expect(field()).toHaveAttribute('aria-expanded', 'true')
  expect(field()).toHaveAttribute('aria-controls', list.id)
  await userEvent.keyboard('{ArrowDown}{Enter}')
  expect(useStore.getState().searchDraft).toBe('hi from:alvin ')
  expect(submitSearch).not.toHaveBeenCalled()
  expect(screen.queryByRole('listbox')).toBeNull()
})

test('in: inserts a channel slug, a DM as @name and a GM as @a,b,c; a negated filter keeps its minus', async () => {
  vi.mocked(client.searchSuggest).mockResolvedValue(
    dto({
      channels: [
        { id: 'c1', name: 'town-square', display_name: 'Town Square', type: 'O' },
        { id: 'd1', name: '@bob', display_name: 'Bob', type: 'D' },
        { id: 'g1', name: '@alice,bob,carol', display_name: 'alice, bob, carol', type: 'G' },
      ],
    }),
  )
  render(<SearchBox variant="header" />)
  await userEvent.type(field(), '-in:')
  await screen.findByRole('listbox')
  expect(client.searchSuggest).toHaveBeenLastCalledWith(1, 't1', 'channels', '', expect.any(AbortSignal))
  expect(screen.getByRole('option', { name: /Town Square/ })).toHaveTextContent('~town-square')
  await userEvent.keyboard('{Tab}')
  expect(useStore.getState().searchDraft).toBe('-in:town-square ')
  await userEvent.type(field(), 'in:@')
  await screen.findByRole('listbox')
  await userEvent.click(screen.getByRole('option', { name: /alice, bob, carol/ }))
  expect(useStore.getState().searchDraft).toBe('-in:town-square in:@alice,bob,carol ')
})

test('Esc: closes the suggestions, then clears the text, then leaves the field — each time taken', async () => {
  vi.mocked(client.searchSuggest).mockResolvedValue(dto({ users: [{ id: 'u1', username: 'alice' }] }))
  render(<SearchBox variant="header" />)
  await userEvent.type(field(), 'from:a')
  await screen.findByRole('listbox')
  const esc = () => {
    const ev = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
    field().dispatchEvent(ev)
    return ev.defaultPrevented
  }
  expect(esc()).toBe(true)
  await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull())
  expect(useStore.getState().searchDraft).toBe('from:a')
  expect(esc()).toBe(true)
  await waitFor(() => expect(useStore.getState().searchDraft).toBe(''))
  expect(field()).toHaveFocus()
  expect(esc()).toBe(true)
  expect(field()).not.toHaveFocus()
})

test('focusing the header field shows a kept session the narrow overlay left', () => {
  useStore.setState({ search: { gen: 1 } as never, rhs: null })
  render(<SearchBox variant="header" />)
  field().focus()
  expect(useStore.getState().rhs).toBe('search')
})

test('Codex search7: team switch invalidates a pending suggestion response', async () => {
  let resolve!: (d: AutocompleteDTO) => void
  vi.mocked(client.searchSuggest).mockReturnValueOnce(new Promise<AutocompleteDTO>((r) => (resolve = r)))
  render(<SearchBox variant="header" />)
  await userEvent.type(field(), 'from:al')
  await waitFor(() => expect(client.searchSuggest).toHaveBeenCalled())
  const signal = vi.mocked(client.searchSuggest).mock.calls[0][4]!
  await act(async () => {
    useStore.getState().setSidebar(1, { team_id: 't2', selected_channel_id: '', teams: [], categories: [] })
    resolve(dto({ users: [{ id: 'old-user', username: 'alice-old-team' }] }))
  })
  expect(screen.queryByRole('option', { name: /alice-old-team/ })).toBeNull()
  expect(signal.aborted).toBe(true)
})

test('a team switch closes an open suggestion list and asks the new team for the same word', async () => {
  vi.mocked(client.searchSuggest).mockResolvedValueOnce(dto({ users: [{ id: 'u1', username: 'alice-old-team' }] }))
  render(<SearchBox variant="header" />)
  await userEvent.type(field(), 'from:al')
  await screen.findByRole('option', { name: /alice-old-team/ })
  vi.mocked(client.searchSuggest).mockResolvedValueOnce(dto({ users: [{ id: 'u2', username: 'alice-new-team' }] }))
  act(() => useStore.getState().setSidebar(1, { team_id: 't2', selected_channel_id: '', teams: [], categories: [] }))
  expect(screen.queryByRole('option', { name: /alice-old-team/ })).toBeNull()
  await screen.findByRole('option', { name: /alice-new-team/ })
  expect(client.searchSuggest).toHaveBeenLastCalledWith(1, 't2', 'users', 'al', expect.any(AbortSignal))
})

test('narrow: the header field under the overlay hands focus, text and caret to the panel field', async () => {
  render(
    <>
      <SearchBox variant="header" />
    </>,
  )
  act(() => useStore.setState({ searchDraft: 'привет мир' }))
  const header = field() as HTMLInputElement
  expect(header).toHaveValue('привет мир')
  header.focus()
  header.setSelectionRange(3, 3)
  const { unmount } = render(<SearchBox variant="pane" />)
  const pane = document.querySelector<HTMLInputElement>('input[data-search-input="pane"]')!
  expect(pane).toHaveFocus()
  expect(pane.value).toBe('привет мир')
  expect(pane.selectionStart).toBe(3)
  unmount()
})
