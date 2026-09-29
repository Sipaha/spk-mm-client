import { fireEvent, render, screen, within } from '@testing-library/react'
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
    onTeam: vi.fn(), onChannel: vi.fn(), onSignOut: vi.fn(), onRemove: vi.fn(), onReauth: vi.fn(),
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
