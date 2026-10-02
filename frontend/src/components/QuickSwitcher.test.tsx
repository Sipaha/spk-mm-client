import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { setLocale } from '../i18n'

const quickChannels = vi.fn().mockResolvedValue([
  { server_id: 2, server_name: 'Work', id: 'c1', name: 'Town Square', type: 'O', team_id: 't1', team_name: 'team', team_display: 'Acme', last_activity_at: 20 },
  { server_id: 3, server_name: 'Friends', id: 'd1', name: 'Bob', type: 'D', team_id: '', team_name: '', team_display: '', last_activity_at: 10 },
])
const selectServer = vi.fn()
const openChannel = vi.fn()
vi.mock('../api/client', () => ({ client: { quickChannels } }))
vi.mock('../chat', () => ({ selectServer, openChannel }))
const { QuickSwitcher } = await import('./QuickSwitcher')
const { rankQuickChannels } = await import('./QuickSwitcher')

beforeEach(() => { setLocale('en'); vi.clearAllMocks() })

test('Ctrl+K opens the global switcher, filters, and opens the selected channel', async () => {
  render(<QuickSwitcher />)
  fireEvent.keyDown(window, { key: 'л', code: 'KeyK', ctrlKey: true })
  const input = await screen.findByRole('textbox', { name: 'Find a channel or direct message' })
  await waitFor(() => expect(screen.getByText('Town Square')).toBeInTheDocument())
  fireEvent.change(input, { target: { value: 'bob friends' } })
  expect(screen.queryByText('Town Square')).toBeNull()
  fireEvent.keyDown(input, { key: 'Enter' })
  expect(selectServer).toHaveBeenCalledWith(3)
  expect(openChannel).toHaveBeenCalledWith(3, 'd1')
})

test('ranking prefers a direct name match, then recent activity', () => {
  const base = [
    { server_id: 1, server_name: 'S', id: 'old-group', name: 'alex, roman', type: 'G', team_id: '', team_name: '', team_display: '', last_activity_at: 1 },
    { server_id: 1, server_name: 'S', id: 'new-group', name: 'denis, roman', type: 'G', team_id: '', team_name: '', team_display: '', last_activity_at: 30 },
    { server_id: 1, server_name: 'S', id: 'dm', name: 'roman.makarskiy', type: 'D', team_id: '', team_name: '', team_display: '', last_activity_at: 2 },
  ]
  expect(rankQuickChannels(base, 'roman').map((x) => x.id)).toEqual(['dm', 'new-group', 'old-group'])
  expect(rankQuickChannels(base, '').map((x) => x.id)).toEqual(['new-group', 'dm', 'old-group'])
})
