import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, vi } from 'vitest'
import type { ReactionUsersDTO } from '../api/types'
import { setLocale } from '../i18n'
import { _resetReactorCacheForTests } from '../reactorCache'
import { Reactions } from './Reactions'

const me = { id: 'u-alice' }

beforeEach(() => {
  setLocale('en')
  _resetReactorCacheForTests()
})

afterEach(() => {
  vi.useRealTimers()
})

const dto = (users: ReactionUsersDTO['users'], unknown = 0): ReactionUsersDTO => ({ users, unknown })

function setupHover() {
  vi.useFakeTimers()
  return userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
}

test('chips toggle our reaction and say whether it is ours', async () => {
  const onToggle = vi.fn()
  const loadReactors = vi.fn().mockResolvedValue(dto([]))
  render(
    <Reactions
      serverId={1}
      postId="p1"
      me={me}
      reactions={[{ emoji: '+1', count: 3, mine: true }, { emoji: 'tada', count: 1, mine: false }]}
      onToggle={onToggle}
      loadReactors={loadReactors}
    />,
  )
  const mine = screen.getByRole('button', { name: '👍 3, you reacted' })
  expect(mine).toHaveAttribute('aria-pressed', 'true')
  await userEvent.click(screen.getByRole('button', { name: '🎉 1' }))
  expect(onToggle).toHaveBeenCalledWith({ emoji: 'tada', count: 1, mine: false })
  await userEvent.click(mine)
  expect(onToggle).toHaveBeenLastCalledWith({ emoji: '+1', count: 3, mine: true })
  expect(screen.queryByRole('button', { name: 'Add reaction' })).toBeNull()
  // No native title — aria-label/the tooltip cover it, a native tooltip would double up.
  expect(mine).not.toHaveAttribute('title')
})

test('a custom emoji is a picture from the media endpoint once the set says it is not standard', async () => {
  const { container } = render(
    <Reactions serverId={4} postId="p1" me={me} reactions={[{ emoji: 'partyparrot', count: 1, mine: false }]} onToggle={vi.fn()} loadReactors={vi.fn()} />,
  )
  expect(screen.getByRole('button', { name: ':partyparrot: 1' })).toBeInTheDocument()
  await waitFor(() => expect(container.querySelector('img')).toHaveAttribute('src', '/media/4/emoji/partyparrot'))
  expect(container.querySelector('img')).not.toHaveAttribute('loading')
  fireEvent.error(container.querySelector('img')!)
  expect(container).toHaveTextContent(':partyparrot:')
})

test('a standard emoji outside the common table appears once the set loads', async () => {
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: 'avocado', count: 2, mine: false }]} onToggle={vi.fn()} loadReactors={vi.fn()} />)
  expect(await screen.findByRole('button', { name: '🥑 2' })).toBeInTheDocument()
})

test('chips are 28 px tall with an 18 px emoji; the add-reaction button matches and has an aria-label', async () => {
  const onAdd = vi.fn()
  const { container } = render(
    <Reactions
      serverId={4}
      postId="p1"
      me={me}
      reactions={[{ emoji: 'tada', count: 1, mine: false }, { emoji: 'partyparrot', count: 2, mine: false }]}
      onToggle={vi.fn()}
      onAdd={onAdd}
      loadReactors={vi.fn()}
    />,
  )
  const chip = screen.getByRole('button', { name: '🎉 1' })
  expect(chip.className).toContain('h-7')
  await waitFor(() => expect(container.querySelector('img')).toHaveAttribute('width', '18'))
  expect(container.querySelector('img')).toHaveAttribute('height', '18')
  const add = screen.getByRole('button', { name: 'Add reaction' })
  expect(add.className).toContain('h-7')
  expect(add.className).toContain('w-9')
  expect(add).toHaveAccessibleName('Add reaction')
  await userEvent.click(add)
  expect(onAdd).toHaveBeenCalledWith(add)
})

// ---- reactor tooltip ----

test('hovering a chip shows the tooltip after the hover delay, with the right text and aria-describedby', async () => {
  const loadReactors = vi.fn().mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }, { id: 'u-carol', name: 'carol', avatar: '' }]))
  const user = setupHover()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 3, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  const chip = screen.getByRole('button', { name: '👍 3, you reacted' })
  expect(chip).not.toHaveAttribute('aria-describedby')

  await user.hover(chip)
  expect(loadReactors).not.toHaveBeenCalled() // not before the delay
  await act(async () => vi.advanceTimersByTime(300))
  expect(loadReactors).toHaveBeenCalledWith('p1', '+1')
  // Names only — no "reacted with :emoji:" suffix (2026-09-29 ruling): the
  // user already hovered this specific chip, so the emoji is redundant.
  await waitFor(() => expect(screen.getByRole('tooltip')).toHaveTextContent('You, bob and carol', { normalizeWhitespace: true }))
  expect(screen.getByRole('tooltip').textContent).toBe('You, bob and carol')
  expect(chip).toHaveAttribute('aria-describedby', screen.getByRole('tooltip').id)
})

test('ru wording matches the brief exactly', async () => {
  setLocale('ru')
  const loadReactors = vi.fn().mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }]))
  const user = setupHover()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 2, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  await user.hover(screen.getByRole('button'))
  await act(async () => vi.advanceTimersByTime(300))
  await waitFor(() => expect(screen.getByRole('tooltip')).toHaveTextContent('Вы и bob'))
  expect(screen.getByRole('tooltip').textContent).toBe('Вы и bob')
})

test('12 reactors: the tooltip truncates to 10 shown plus an "and N others" button', async () => {
  const users = Array.from({ length: 11 }, (_, i) => ({ id: `u${i}`, name: `user${i}`, avatar: '' }))
  const loadReactors = vi.fn().mockResolvedValue(dto(users))
  const user = setupHover()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 12, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  await user.hover(screen.getByRole('button'))
  await act(async () => vi.advanceTimersByTime(300))
  const tip = await screen.findByRole('tooltip')
  const more = await waitFor(() => {
    const btn = screen.getByRole('button', { name: '2 other users' })
    expect(tip).toContainElement(btn)
    return btn
  })
  expect(more.tagName).toBe('BUTTON')
})

test('shows "…" while loading', async () => {
  let resolve!: (v: ReactionUsersDTO) => void
  const loadReactors = vi.fn(() => new Promise<ReactionUsersDTO>((r) => { resolve = r }))
  const user = setupHover()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 1, mine: false }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  await user.hover(screen.getByRole('button'))
  await act(async () => vi.advanceTimersByTime(300))
  expect(await screen.findByRole('tooltip')).toHaveTextContent('…')
  await act(async () => {
    resolve(dto([{ id: 'u-bob', name: 'bob', avatar: '' }]))
    await Promise.resolve()
  })
  await waitFor(() => expect(screen.getByRole('tooltip')).toHaveTextContent('bob'))
})

test('the cache is hit for the same count and misses when the count changes', async () => {
  const loadReactors = vi.fn().mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }]))
  const user = setupHover()
  const { rerender } = render(
    <Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 1, mine: false }]} onToggle={vi.fn()} loadReactors={loadReactors} />,
  )
  const chip = screen.getByRole('button')
  await user.hover(chip)
  await act(async () => vi.advanceTimersByTime(300))
  await waitFor(() => expect(loadReactors).toHaveBeenCalledTimes(1))
  await user.unhover(chip)
  await act(async () => vi.advanceTimersByTime(200)) // past the leave grace

  // Same count: a second hover hits the cache — no new call.
  await user.hover(chip)
  await act(async () => vi.advanceTimersByTime(300))
  await waitFor(() => expect(screen.getByRole('tooltip')).toHaveTextContent('bob'))
  expect(loadReactors).toHaveBeenCalledTimes(1)
  await user.unhover(chip)
  await act(async () => vi.advanceTimersByTime(200))

  // The count changed (a new reaction arrived) — stale, refetches.
  loadReactors.mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }, { id: 'u-carol', name: 'carol', avatar: '' }]))
  rerender(
    <Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 2, mine: false }]} onToggle={vi.fn()} loadReactors={loadReactors} />,
  )
  await user.hover(screen.getByRole('button'))
  await act(async () => vi.advanceTimersByTime(300))
  await waitFor(() => expect(loadReactors).toHaveBeenCalledTimes(2))
})

test('rendering many chips makes no fetch calls without hover', () => {
  const loadReactors = vi.fn()
  const reactions = Array.from({ length: 50 }, (_, i) => ({ emoji: `e${i}`, count: 1, mine: false }))
  render(<Reactions serverId={1} postId="p1" me={me} reactions={reactions} onToggle={vi.fn()} loadReactors={loadReactors} />)
  expect(loadReactors).not.toHaveBeenCalled()
})

test('hidden on leave (after the grace delay) and on Esc', async () => {
  const loadReactors = vi.fn().mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }]))
  const user = setupHover()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 1, mine: false }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  const chip = screen.getByRole('button')
  await user.hover(chip)
  await act(async () => vi.advanceTimersByTime(300))
  await screen.findByRole('tooltip')

  await user.unhover(chip)
  await act(async () => vi.advanceTimersByTime(200))
  expect(screen.queryByRole('tooltip')).toBeNull()

  await user.hover(chip)
  await act(async () => vi.advanceTimersByTime(300))
  await screen.findByRole('tooltip')
  act(() => {
    fireEvent.keyDown(window, { key: 'Escape' })
  })
  expect(screen.queryByRole('tooltip')).toBeNull()
})

test('hidden on scroll', async () => {
  const loadReactors = vi.fn().mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }]))
  const user = setupHover()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 1, mine: false }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  await user.hover(screen.getByRole('button'))
  await act(async () => vi.advanceTimersByTime(300))
  await screen.findByRole('tooltip')
  act(() => {
    fireEvent.scroll(window)
  })
  expect(screen.queryByRole('tooltip')).toBeNull()
})

test('the tooltip stays open while the pointer moves from the chip into it', async () => {
  const loadReactors = vi.fn().mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }]))
  const user = setupHover()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 1, mine: false }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  const chip = screen.getByRole('button')
  await user.hover(chip)
  await act(async () => vi.advanceTimersByTime(300))
  const tip = await screen.findByRole('tooltip')

  await user.unhover(chip)
  await user.hover(tip) // the pointer crossed into the tooltip before the grace delay elapsed
  await act(async () => vi.advanceTimersByTime(200))
  expect(screen.getByRole('tooltip')).toBeInTheDocument()
})

test('hidden on blur (focus leaving both the chip and the tooltip)', async () => {
  const loadReactors = vi.fn().mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }]))
  render(
    <div>
      <Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 1, mine: false }]} onToggle={vi.fn()} loadReactors={loadReactors} />
      <button>elsewhere</button>
    </div>,
  )
  vi.useFakeTimers()
  const chip = screen.getByRole('button', { name: '👍 1' })
  act(() => chip.focus())
  await act(async () => vi.advanceTimersByTime(300))
  await screen.findByRole('tooltip')
  act(() => screen.getByRole('button', { name: 'elsewhere' }).focus())
  expect(screen.queryByRole('tooltip')).toBeNull()
})

// e2e-bisected regression (2026-09-29, density-review.md "media.spec
// failure" + coordinator follow-up): a stuck tooltip anchored to a chip
// that unmounted before its hover delay fired stayed open forever (no
// mouseleave/blur can fire for a detached node) and, once the sidebar
// server-rail fix shifted other UI underneath its {0,0}-anchored position,
// ate a click meant for an unrelated button.
test('a chip removed before its hover delay elapses never shows an orphaned tooltip', async () => {
  const loadReactors = vi.fn().mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }]))
  const user = setupHover()
  const { rerender } = render(
    <Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 1, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />,
  )
  const chip = screen.getByRole('button', { name: '👍 1, you reacted' })
  await user.hover(chip) // schedules show() after HOVER_DELAY — not fired yet
  rerender(<Reactions serverId={1} postId="p1" me={me} reactions={[]} onToggle={vi.fn()} loadReactors={loadReactors} />) // the chip's reaction is gone — it unmounts
  await act(async () => vi.advanceTimersByTime(300)) // the pending timer now fires with a detached anchor
  expect(screen.queryByRole('tooltip')).toBeNull()
})

test('a chip removed while its tooltip is already open closes the tooltip', async () => {
  const loadReactors = vi.fn().mockResolvedValue(dto([{ id: 'u-bob', name: 'bob', avatar: '' }]))
  const user = setupHover()
  const { rerender } = render(
    <Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 1, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />,
  )
  const chip = screen.getByRole('button', { name: '👍 1, you reacted' })
  await user.hover(chip)
  await act(async () => vi.advanceTimersByTime(300))
  await screen.findByRole('tooltip')
  rerender(<Reactions serverId={1} postId="p1" me={me} reactions={[]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  expect(screen.queryByRole('tooltip')).toBeNull()
})

// ---- the full-list modal ----

test('the "and N others" button opens the modal with the full list, "You" first', async () => {
  const users = Array.from({ length: 11 }, (_, i) => ({ id: `u${i}`, name: `user${i}`, avatar: '' }))
  const loadReactors = vi.fn().mockResolvedValue(dto(users))
  const user = setupHover()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 12, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  await user.hover(screen.getByRole('button'))
  await act(async () => vi.advanceTimersByTime(300))
  const more = await screen.findByRole('button', { name: '2 other users' })
  await user.click(more)

  const dialog = await screen.findByRole('dialog')
  const rows = within(dialog).getAllByRole('listitem')
  expect(rows).toHaveLength(12) // "You" + 11 named
  expect(rows[0]).toHaveTextContent('You')
  expect(rows[1]).toHaveTextContent('user0')
  expect(rows[11]).toHaveTextContent('user10')
  expect(screen.queryByRole('tooltip')).toBeNull() // the tooltip closes once the modal opens
})

test('Esc and the backdrop close the modal; focus returns to the chip', async () => {
  const loadReactors = vi.fn().mockResolvedValue(dto(Array.from({ length: 11 }, (_, i) => ({ id: `u${i}`, name: `user${i}`, avatar: '' }))))
  const user = setupHover()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 12, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  const chip = screen.getByRole('button')
  await user.hover(chip)
  await act(async () => vi.advanceTimersByTime(300))
  await user.click(await screen.findByRole('button', { name: '2 other users' }))
  await screen.findByRole('dialog')

  await user.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(chip).toHaveFocus()

  // Backdrop click closes it too.
  await user.hover(chip)
  await act(async () => vi.advanceTimersByTime(300))
  await user.click(await screen.findByRole('button', { name: '2 other users' }))
  const dialog = await screen.findByRole('dialog')
  await user.click(dialog.parentElement!) // the backdrop, not the panel itself
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(chip).toHaveFocus()
})

test('keyboard: Tab from a focused chip with a showing tooltip reaches the "and N others" button', async () => {
  const users = Array.from({ length: 11 }, (_, i) => ({ id: `u${i}`, name: `user${i}`, avatar: '' }))
  const loadReactors = vi.fn().mockResolvedValue(dto(users))
  vi.useFakeTimers()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 12, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  const chip = screen.getByRole('button')
  act(() => chip.focus())
  await act(async () => vi.advanceTimersByTime(300))
  const more = await screen.findByRole('button', { name: '2 other users' })
  fireEvent.keyDown(chip, { key: 'Tab' })
  expect(more).toHaveFocus()
})

test('keyboard: Shift+Tab from the overflow button returns to the chip (symmetric hand-off)', async () => {
  const users = Array.from({ length: 11 }, (_, i) => ({ id: `u${i}`, name: `user${i}`, avatar: '' }))
  const loadReactors = vi.fn().mockResolvedValue(dto(users))
  vi.useFakeTimers()
  render(<Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 12, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />)
  const chip = screen.getByRole('button', { name: '👍 12, you reacted' })
  act(() => chip.focus())
  await act(async () => vi.advanceTimersByTime(300))
  const more = await screen.findByRole('button', { name: '2 other users' })
  act(() => more.focus())
  fireEvent.keyDown(more, { key: 'Tab', shiftKey: true })
  expect(chip).toHaveFocus()
})

test('keyboard: Tab from the overflow button moves to whatever is next after the chip, not the end of the document', async () => {
  const users = Array.from({ length: 11 }, (_, i) => ({ id: `u${i}`, name: `user${i}`, avatar: '' }))
  const loadReactors = vi.fn().mockResolvedValue(dto(users))
  vi.useFakeTimers()
  render(
    <div>
      <Reactions serverId={1} postId="p1" me={me} reactions={[{ emoji: '+1', count: 12, mine: true }]} onToggle={vi.fn()} loadReactors={loadReactors} />
      <button>after the reactions row</button>
    </div>,
  )
  const chip = screen.getByRole('button', { name: '👍 12, you reacted' })
  act(() => chip.focus())
  await act(async () => vi.advanceTimersByTime(300))
  const more = await screen.findByRole('button', { name: '2 other users' })
  act(() => more.focus())
  fireEvent.keyDown(more, { key: 'Tab' })
  expect(screen.getByRole('button', { name: 'after the reactions row' })).toHaveFocus()
})
