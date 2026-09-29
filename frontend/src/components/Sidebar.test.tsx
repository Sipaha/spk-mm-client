import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { ServerDTO, SidebarDTO } from '../api/types'
import { setLocale } from '../i18n'
import { Sidebar } from './Sidebar'

const server: ServerDTO = { id: 1, name: 'Acme', url: 'u', signed_in: true, username: 'alice', gitlab: false, state: 'live', unread: true, mentions: 1 }

const sb: SidebarDTO = {
  team_id: 't1',
  selected_channel_id: 'c-town',
  teams: [
    { id: 't1', name: 'one', display_name: 'One', unread: true, mentions: 1 },
    { id: 't2', name: 'two', display_name: 'Two', unread: true, mentions: 0 },
  ],
  categories: [
    { id: 'fav', type: 'favorites', name: 'Favorites', collapsed: false, channels: null },
    {
      id: 'ch', type: 'channels', name: 'Channels', collapsed: false,
      channels: [
        { id: 'c-town', name: 'Town Square', type: 'O', unread: false, mentions: 0, muted: false },
        { id: 'c-off', name: 'Off-Topic', type: 'O', unread: true, mentions: 1, muted: false },
        { id: 'c-noise', name: 'Noise', type: 'O', unread: false, mentions: 0, muted: true },
      ],
    },
    {
      id: 'my', type: 'custom', name: 'Work', collapsed: true,
      channels: [
        { id: 'c-a', name: 'Quiet', type: 'P', unread: false, mentions: 0, muted: false },
        { id: 'c-b', name: 'Busy', type: 'P', unread: true, mentions: 0, muted: false },
      ],
    },
    { id: 'dm', type: 'direct_messages', name: 'Direct Messages', collapsed: false, channels: [{ id: 'c-dm', name: 'bob', type: 'D', unread: false, mentions: 0, muted: false }] },
  ],
}

function renderSidebar(over: Partial<Parameters<typeof Sidebar>[0]> = {}) {
  const props = {
    server, sidebar: sb, activeChannelId: 'c-town',
    onTeam: vi.fn(), onChannel: vi.fn(), onSignOut: vi.fn(), onRemove: vi.fn(), onReauth: vi.fn(), onAddServer: vi.fn(),
    ...over,
  }
  const { rerender } = render(<Sidebar {...props} />)
  // rerender: re-render the same tree with a subset of props changed — used
  // by the server-menu-closes-on-switch tests below (a fresh renderSidebar()
  // call would mount a new instance instead of exercising the prop-change
  // effect on the live one).
  return { ...props, rerender: (next: Partial<typeof props> = {}) => rerender(<Sidebar {...props} {...next} />) }
}

beforeEach(() => setLocale('en'))

test('categories: localized default names, custom as is, collapsed shows only unread', async () => {
  const p = renderSidebar()
  expect(screen.getByRole('button', { name: /Channels/ })).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByRole('button', { name: /Direct messages/ })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /Work/ })).toHaveAttribute('aria-expanded', 'false')
  expect(screen.queryByRole('button', { name: /Quiet/ })).toBeNull()
  expect(screen.getByRole('button', { name: /Busy/ })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /Work/ }))
  expect(screen.getByRole('button', { name: /Quiet/ })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /Off-Topic/ }))
  expect(p.onChannel).toHaveBeenCalledWith('c-off')
})

test('unread, mentions, muted and active are visible', () => {
  renderSidebar()
  const off = screen.getByRole('button', { name: /Off-Topic/ })
  expect(off).toHaveClass('font-semibold')
  expect(within(off).getByLabelText('Mentions: 1')).toHaveTextContent('1')
  expect(screen.getByRole('button', { name: /Noise/ })).toHaveClass('opacity-50')
  expect(screen.getByRole('button', { name: /Town Square/ })).toHaveAttribute('aria-current', 'true')
})

test('tone classes: read, unread and active channels each use their own sidebar token', () => {
  renderSidebar()
  const read = screen.getByRole('button', { name: 'Town Square' }) // active (selected_channel is c-town's aria-current, but Town Square is also active here)
  const unread = screen.getByRole('button', { name: /Off-Topic/ })
  const muted = screen.getByRole('button', { name: 'Noise' }) // read, not active
  expect(read).toHaveClass('border-sidebar-active-border', 'bg-sidebar-active-bg', 'text-sidebar-fg-unread')
  expect(unread).toHaveClass('font-semibold', 'text-sidebar-fg-unread', 'border-transparent')
  expect(muted).toHaveClass('text-sidebar-fg', 'border-transparent')
  expect(muted).not.toHaveClass('font-semibold')
})

test('team switcher appears with more than one team', async () => {
  const p = renderSidebar()
  await userEvent.click(screen.getByRole('button', { name: /Two/ }))
  expect(p.onTeam).toHaveBeenCalledWith('t2')
})

test('needs_reauth shows the sign-in-again action', async () => {
  const p = renderSidebar({ server: { ...server, state: 'needs_reauth' } })
  expect(screen.getByText('Session expired')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Sign in again' }))
  expect(p.onReauth).toHaveBeenCalled()
})

test('server menu signs out and removes', async () => {
  const p = renderSidebar()
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  await userEvent.click(screen.getByRole('menuitem', { name: 'Sign out' }))
  expect(p.onSignOut).toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  await userEvent.click(screen.getByRole('menuitem', { name: 'Remove server' }))
  expect(p.onRemove).toHaveBeenCalled()
})

// Sidebar-menu-brief addendum (2026-09-29): with the server rail hidden
// (single server), its "+" tile is unreachable — "Add server" moves into
// this menu instead, above Sign out/Remove server, opening the same flow.
test('server menu: "Add server" sits above Sign out/Remove server and closes the menu', async () => {
  const p = renderSidebar()
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  const items = screen.getAllByRole('menuitem').map((b) => b.textContent)
  expect(items).toEqual(['Add server', 'Sign out', 'Remove server'])
  await userEvent.click(screen.getByRole('menuitem', { name: 'Add server' }))
  expect(p.onAddServer).toHaveBeenCalledTimes(1)
  expect(screen.queryByRole('menu')).toBeNull()
})

// The bug (user report 2026-09-29): the menu had no outside-click/Escape
// handling at all, so it stayed open while the user clicked elsewhere. The
// fix reuses the same menu-button contract as PostMenu/FormattingMenu.

test('server menu: the anchor advertises the popup', () => {
  renderSidebar()
  const btn = screen.getByRole('button', { name: 'Server menu' })
  expect(btn).toHaveAttribute('aria-haspopup', 'menu')
  expect(btn).toHaveAttribute('aria-expanded', 'false')
  expect(btn).not.toHaveAttribute('aria-expanded', 'true')
})

test('server menu: picking an item closes it and returns focus to the anchor', async () => {
  const p = renderSidebar()
  const btn = screen.getByRole('button', { name: 'Server menu' })
  await userEvent.click(btn)
  expect(btn).toHaveAttribute('aria-expanded', 'true')
  await userEvent.click(screen.getByRole('menuitem', { name: 'Sign out' }))
  expect(p.onSignOut).toHaveBeenCalledTimes(1)
  expect(screen.queryByRole('menu')).toBeNull()
  expect(document.activeElement).toBe(btn)
})

test('server menu: a pointerdown outside the menu and its anchor closes it without stealing focus', async () => {
  renderSidebar()
  const btn = screen.getByRole('button', { name: 'Server menu' })
  await userEvent.click(btn)
  expect(screen.getByRole('menu')).toBeInTheDocument()
  fireEvent.mouseDown(document.body)
  expect(screen.queryByRole('menu')).toBeNull()
  expect(document.activeElement).not.toBe(btn)
})

test('server menu: a mousedown on the anchor itself does not close it (so its own click can toggle it)', async () => {
  renderSidebar()
  const btn = screen.getByRole('button', { name: 'Server menu' })
  await userEvent.click(btn)
  expect(screen.getByRole('menu')).toBeInTheDocument()
  fireEvent.mouseDown(btn)
  expect(screen.getByRole('menu')).toBeInTheDocument()
})

test('server menu: clicking the anchor while open closes it (open→closed, not a reopen)', async () => {
  renderSidebar()
  const btn = screen.getByRole('button', { name: 'Server menu' })
  await userEvent.click(btn) // closed → open
  expect(screen.getByRole('menu')).toBeInTheDocument()
  await userEvent.click(btn) // open → closed
  expect(screen.queryByRole('menu')).toBeNull()
})

test('server menu: Escape closes it and returns focus to the anchor', async () => {
  renderSidebar()
  const btn = screen.getByRole('button', { name: 'Server menu' })
  await userEvent.click(btn)
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('menu')).toBeNull()
  expect(document.activeElement).toBe(btn)
})

test('server menu: window blur closes it', async () => {
  renderSidebar()
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  expect(screen.getByRole('menu')).toBeInTheDocument()
  fireEvent.blur(window)
  expect(screen.queryByRole('menu')).toBeNull()
})

test('server menu: switching the active channel closes it', async () => {
  const p = renderSidebar()
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  expect(screen.getByRole('menu')).toBeInTheDocument()
  p.rerender({ activeChannelId: 'c-off' })
  expect(screen.queryByRole('menu')).toBeNull()
})

test('server menu: switching servers closes it', async () => {
  const p = renderSidebar()
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  expect(screen.getByRole('menu')).toBeInTheDocument()
  p.rerender({ server: { ...server, id: 2 } })
  expect(screen.queryByRole('menu')).toBeNull()
})

test('server menu: document/window listeners are added only while open and removed on close (no leak)', async () => {
  const addDoc = vi.spyOn(document, 'addEventListener')
  const removeDoc = vi.spyOn(document, 'removeEventListener')
  const addWin = vi.spyOn(window, 'addEventListener')
  const removeWin = vi.spyOn(window, 'removeEventListener')
  renderSidebar()
  await userEvent.click(screen.getByRole('button', { name: 'Server menu' }))
  const mousedownHandler = addDoc.mock.calls.find(([type]) => type === 'mousedown')?.[1]
  const keydownHandler = addDoc.mock.calls.find(([type]) => type === 'keydown')?.[1]
  const blurHandler = addWin.mock.calls.find(([type]) => type === 'blur')?.[1]
  expect(mousedownHandler).toBeInstanceOf(Function)
  expect(keydownHandler).toBeInstanceOf(Function)
  expect(blurHandler).toBeInstanceOf(Function)
  await userEvent.keyboard('{Escape}') // closes via Escape
  expect(removeDoc).toHaveBeenCalledWith('mousedown', mousedownHandler)
  expect(removeDoc).toHaveBeenCalledWith('keydown', keydownHandler)
  expect(removeWin).toHaveBeenCalledWith('blur', blurHandler)
  addDoc.mockRestore()
  removeDoc.mockRestore()
  addWin.mockRestore()
  removeWin.mockRestore()
})

test('DM rows show the partner picture with presence; bots have no dot; groups keep their mark', () => {
  const dms: SidebarDTO = {
    ...sb,
    categories: [{
      id: 'dm', type: 'direct_messages', name: 'Direct Messages', collapsed: false,
      channels: [
        { id: 'c-dm', name: 'bob', type: 'D', unread: false, mentions: 0, muted: false, user_id: 'u-bob', avatar: '5', status: 'online' },
        { id: 'c-bot', name: 'ci', type: 'D', unread: false, mentions: 0, muted: false, user_id: 'u-ci', avatar: '0', bot: true },
        { id: 'c-gm', name: 'alice, bob, carol', type: 'G', unread: false, mentions: 0, muted: false },
      ],
    }],
  }
  renderSidebar({ sidebar: dms })
  const bob = screen.getByRole('button', { name: 'bob, Online' })
  expect(bob.querySelector('img')).toHaveAttribute('src', '/media/1/avatar/u-bob?v=5')
  expect(bob.querySelector('[data-status="online"]')).not.toBeNull()
  const ci = screen.getByRole('button', { name: 'ci' })
  expect(ci.querySelector('[data-status]')).toBeNull()
  // The group DM's type marker is IconGroup (an svg icon), not emoji text.
  expect(screen.getByRole('button', { name: /alice, bob, carol/ }).querySelector('svg')).not.toBeNull()
})

test('muted channels stay dim whether read or unread (ruling 1: muted is the only grey tier)', () => {
  const withMutedUnread: SidebarDTO = {
    ...sb,
    categories: (sb.categories ?? []).map((c) =>
      c.id === 'ch'
        ? { ...c, channels: (c.channels ?? []).map((ch) => (ch.id === 'c-off' ? { ...ch, muted: true } : ch)) }
        : c,
    ),
  }
  renderSidebar({ sidebar: withMutedUnread })
  const mutedUnread = screen.getByRole('button', { name: /Off-Topic/ })
  // Still bold/full-brightness-token like any other unread row (rowTone
  // does not know about `muted`) — the dimming comes only from opacity-50.
  expect(mutedUnread).toHaveClass('font-semibold', 'text-sidebar-fg-unread', 'opacity-50')
})

// "More unreads"/"More mentions" overflow pills (sidebar-unread-brief.md
// ruling 2). jsdom has no IntersectionObserver, so these tests install a
// minimal fake that records observe() calls and lets the test fire
// synthetic entries through the captured callback.
class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = []
  callback: IntersectionObserverCallback
  observed: Element[] = []
  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback
    FakeIntersectionObserver.instances.push(this)
  }
  observe(el: Element) {
    this.observed.push(el)
  }
  unobserve(el: Element) {
    this.observed = this.observed.filter((e) => e !== el)
  }
  disconnect() {
    this.observed = []
  }
  fire(entries: Partial<IntersectionObserverEntry>[]) {
    act(() => this.callback(entries as IntersectionObserverEntry[], this as unknown as IntersectionObserver))
  }
}

const rootBounds = { top: 0, bottom: 300, left: 0, right: 200, width: 200, height: 300 } as DOMRectReadOnly
const aboveRect = { top: -50, bottom: -10 } as DOMRectReadOnly
const belowRect = { top: 320, bottom: 350 } as DOMRectReadOnly

let realIO: typeof IntersectionObserver | undefined

beforeEach(() => {
  FakeIntersectionObserver.instances = []
  realIO = globalThis.IntersectionObserver
  globalThis.IntersectionObserver = FakeIntersectionObserver as unknown as typeof IntersectionObserver
})

afterEach(() => {
  globalThis.IntersectionObserver = realIO as typeof IntersectionObserver
})

test('overflow pills: hidden by default, only countable (unread-or-mention, non-muted-unless-mentioned) rows are observed', () => {
  renderSidebar()
  // aria-hidden="true" by default: computeAccessibleName treats a hidden
  // element's name as empty (same as a screen reader would), so a name-based
  // getByRole query can't find it at all — same pattern as Feed.tsx's
  // "Jump to latest" button (Feed.test.tsx queries its hidden state via
  // queryByRole(...).not.toBeInTheDocument(), never getByRole).
  expect(screen.queryByRole('button', { name: /More (unreads|mentions) above/ })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /More (unreads|mentions) below/ })).not.toBeInTheDocument()

  const observer = FakeIntersectionObserver.instances.at(-1)!
  const offTopic = screen.getByRole('button', { name: /Off-Topic/ }) // unread, not muted → countable
  const busy = screen.getByRole('button', { name: 'Busy' }) // unread, in a collapsed category, shown because unread → countable
  const noise = screen.getByRole('button', { name: 'Noise' }) // muted, read, no mention → not countable
  const townSquare = screen.getByRole('button', { name: 'Town Square' }) // read, active → not countable
  expect(observer.observed).toContain(offTopic)
  expect(observer.observed).toContain(busy)
  expect(observer.observed).not.toContain(noise)
  expect(observer.observed).not.toContain(townSquare)
})

test('overflow pills: a hidden-above unread channel with a mention shows the top pill in its "mentions" style; clicking scrolls it into view', async () => {
  renderSidebar()
  const observer = FakeIntersectionObserver.instances.at(-1)!
  const offTopic = screen.getByRole('button', { name: /Off-Topic/ }) // mentions: 1
  offTopic.scrollIntoView = vi.fn()
  observer.fire([{ target: offTopic, isIntersecting: false, rootBounds, boundingClientRect: aboveRect }])

  const top = screen.getByRole('button', { name: 'More mentions above' })
  expect(top).toHaveAttribute('aria-hidden', 'false')
  await userEvent.click(top)
  expect(offTopic.scrollIntoView).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'smooth' }))
})

test('overflow pills: a hidden-below unread channel without a mention shows the bottom pill in its plain "unreads" style', () => {
  renderSidebar()
  const observer = FakeIntersectionObserver.instances.at(-1)!
  const busy = screen.getByRole('button', { name: 'Busy' }) // mentions: 0
  busy.scrollIntoView = vi.fn()
  observer.fire([{ target: busy, isIntersecting: false, rootBounds, boundingClientRect: belowRect }])

  const bottom = screen.getByRole('button', { name: 'More unreads below' })
  expect(bottom).toHaveAttribute('aria-hidden', 'false')
  fireEvent.click(bottom)
  expect(busy.scrollIntoView).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'smooth' }))
})
