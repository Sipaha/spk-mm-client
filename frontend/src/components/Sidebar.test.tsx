import { render, screen, within } from '@testing-library/react'
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
  render(<Sidebar {...props} />)
  return props
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
