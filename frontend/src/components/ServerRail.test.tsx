import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { ServerDTO } from '../api/types'
import { setLocale } from '../i18n'
import { ServerRail } from './ServerRail'

const srv = (o: Partial<ServerDTO>): ServerDTO => ({
  id: 1, name: 'Acme', url: 'u', signed_in: true, username: 'a', gitlab: false, state: 'live', unread: false, mentions: 0, ...o,
})

beforeEach(() => setLocale('en'))

test('mentions pill beats the unread dot; 99+ cap', async () => {
  const onSelect = vi.fn()
  render(<ServerRail servers={[srv({ mentions: 3, unread: true }), srv({ id: 2, name: 'Beta', unread: true }), srv({ id: 3, name: 'Gamma', mentions: 150 })]} selectedId={1} onSelect={onSelect} />)
  expect(screen.getByLabelText('Mentions: 3')).toHaveTextContent('3')
  expect(screen.getAllByLabelText('Unread messages')).toHaveLength(1)
  expect(screen.getByLabelText('Mentions: 150')).toHaveTextContent('99+')
  // The badges are visual siblings of the button (for absolute positioning),
  // but its accessible name must still say so — not just the server name.
  expect(screen.getByRole('button', { name: /Mentions: 3/ })).toHaveAccessibleName('Acme — Mentions: 3')
  await userEvent.click(screen.getByRole('button', { name: /Beta.*Unread messages/ }))
  expect(onSelect).toHaveBeenCalledWith(2)
})

test('connection problems are visible on the server button', () => {
  render(<ServerRail servers={[srv({ state: 'reconnecting' }), srv({ id: 2, name: 'Beta', state: 'needs_reauth' })]} selectedId={1} onSelect={() => {}} />)
  expect(screen.getByRole('button', { name: 'Acme' })).toHaveAttribute('title', 'Acme — Offline — reconnecting…')
  expect(screen.getByRole('button', { name: 'Beta' })).toHaveAttribute('title', 'Beta — Session expired')
})
