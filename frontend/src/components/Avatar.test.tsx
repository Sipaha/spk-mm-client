import { act, fireEvent, render } from '@testing-library/react'
import type { ServerState } from '../api/types'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { Avatar, presenceKey } from './Avatar'

beforeEach(() => setLocale('en'))

test('a loaded profile: picture with its version, fixed size, presence dot', () => {
  const { container } = render(<Avatar serverId={3} userId="u-bob" version="42" name="bob" status="away" size={36} surface="app" />)
  const img = container.querySelector('img')!
  expect(img).toHaveAttribute('src', '/media/3/avatar/u-bob?v=42')
  expect(img).toHaveAttribute('width', '36')
  expect(img).toHaveAttribute('height', '36')
  expect(img).toHaveAttribute('alt', '')
  // Not loading="lazy": feed rows are virtualized already, and a hidden window
  // never runs WebKit's lazy-load check, which keeps every removed not-yet-loaded
  // image — with its whole detached row — alive until the next paint.
  expect(img).not.toHaveAttribute('loading')
  const dot = container.querySelector('[data-status]')!
  expect(dot).toHaveAttribute('data-status', 'away')
  expect(dot).toHaveAttribute('title', 'Away')
  expect(dot).toHaveAttribute('aria-hidden', 'true')
})

test('no profile yet or a failed load: initials; a new version tries again', () => {
  const { container, rerender } = render(<Avatar serverId={3} userId="u-x" name="" size={36} surface="app" />)
  expect(container.querySelector('img')).toBeNull()
  expect(container).toHaveTextContent('?')
  rerender(<Avatar serverId={3} userId="u-bob" version="1" name="bob" size={36} surface="app" />)
  fireEvent.error(container.querySelector('img')!)
  expect(container.querySelector('img')).toBeNull()
  expect(container).toHaveTextContent('B')
  rerender(<Avatar serverId={3} userId="u-bob" version="2" name="bob" size={36} surface="app" />)
  expect(container.querySelector('img')).toHaveAttribute('src', '/media/3/avatar/u-bob?v=2')
})

test('offline and out-of-office look the same; no status, no dot', () => {
  expect(presenceKey('ooo')).toBe('offline')
  expect(presenceKey('offline')).toBe('offline')
  expect(presenceKey('dnd')).toBe('dnd')
  const { container } = render(<Avatar serverId={1} userId="u" version="0" name="x" size={20} surface="sidebar" />)
  expect(container.querySelector('[data-status]')).toBeNull()
})

test('a failed picture is tried again once the server goes live again', () => {
  const live = (state: ServerState) =>
    act(() => useStore.getState().setServers([{ id: 3, name: 'A', url: 'https://a', signed_in: true, username: 'a', gitlab: false, state, unread: false, mentions: 0 }]))
  live('reconnecting')
  const { container } = render(<Avatar serverId={3} userId="u-bob" version="7" name="bob" size={36} surface="app" />)
  fireEvent.error(container.querySelector('img')!) // offline: /media/ answered 404
  expect(container.querySelector('img')).toBeNull()
  live('reconnecting')
  expect(container.querySelector('img')).toBeNull()
  live('live')
  expect(container.querySelector('img')).toHaveAttribute('src', '/media/3/avatar/u-bob?v=7')
})
