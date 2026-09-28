import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { setLocale } from '../i18n'
import { ReactorsModal } from './ReactorsModal'

beforeEach(() => setLocale('en'))

function anchor() {
  const el = document.createElement('button')
  el.textContent = 'anchor'
  document.body.appendChild(el)
  return el
}

test('unresolved reactors show as "Unknown user" rows, real ones by name', () => {
  const a = anchor()
  render(
    <ReactorsModal
      serverId={1}
      emoji="+1"
      count={3}
      mine={false}
      meId="u-alice"
      meAvatar=""
      users={[{ id: 'u-bob', name: 'bob', avatar: '' }, { id: 'u-x1', name: '', avatar: '' }]}
      anchorEl={a}
      onClose={() => {}}
    />,
  )
  const dialog = screen.getByRole('dialog')
  const rows = within(dialog).getAllByRole('listitem')
  expect(rows).toHaveLength(2)
  expect(rows[0]).toHaveTextContent('bob')
  expect(rows[1]).toHaveTextContent('Unknown user')
  a.remove()
})

test('the "You" row shows my real avatar when a picture version is known', () => {
  const a = anchor()
  render(
    <ReactorsModal
      serverId={1}
      emoji="+1"
      count={2}
      mine={true}
      meId="u-alice"
      meAvatar="7"
      users={[{ id: 'u-bob', name: 'bob', avatar: '' }]}
      anchorEl={a}
      onClose={() => {}}
    />,
  )
  const dialog = screen.getByRole('dialog')
  const rows = within(dialog).getAllByRole('listitem')
  expect(rows[0]).toHaveTextContent('You')
  // Decorative (alt=""), so not queryable by role — same convention as
  // every other Avatar in the app.
  expect(rows[0].querySelector('img')).toHaveAttribute('src', '/media/1/avatar/u-alice?v=7')
  a.remove()
})

test('every row\'s avatar loads lazily (the list is uncapped and always visible when open)', () => {
  const a = anchor()
  render(
    <ReactorsModal
      serverId={1}
      emoji="+1"
      count={3}
      mine={true}
      meId="u-alice"
      meAvatar="7"
      users={[{ id: 'u-bob', name: 'bob', avatar: '3' }, { id: 'u-carol', name: 'carol', avatar: '5' }]}
      anchorEl={a}
      onClose={() => {}}
    />,
  )
  const imgs = [...screen.getByRole('dialog').querySelectorAll('img')]
  expect(imgs.length).toBeGreaterThan(0)
  for (const img of imgs) expect(img).toHaveAttribute('loading', 'lazy')
  a.remove()
})

test('300 rows render, each with a lazy avatar', () => {
  const a = anchor()
  const users = Array.from({ length: 300 }, (_, i) => ({ id: `u-${i}`, name: `user${i}`, avatar: String(i) }))
  render(<ReactorsModal serverId={1} emoji="+1" count={300} mine={false} meId="u-alice" meAvatar="" users={users} anchorEl={a} onClose={() => {}} />)
  const dialog = screen.getByRole('dialog')
  expect(within(dialog).getAllByRole('listitem')).toHaveLength(300)
  const imgs = [...dialog.querySelectorAll('img')]
  expect(imgs).toHaveLength(300)
  expect(imgs.every((img) => img.getAttribute('loading') === 'lazy')).toBe(true)
  a.remove()
})

test('Tab past the last focusable element wraps to the first (focus trap)', async () => {
  const user = userEvent.setup()
  const a = anchor()
  render(
    <ReactorsModal
      serverId={1}
      emoji="+1"
      count={1}
      mine={false}
      meId="u-alice"
      meAvatar=""
      users={[{ id: 'u-bob', name: 'bob', avatar: '' }]}
      anchorEl={a}
      onClose={() => {}}
    />,
  )
  const close = screen.getByRole('button', { name: 'Close' })
  expect(close).toHaveFocus() // opens focused on Close — the only focusable element here
  await user.tab()
  expect(close).toHaveFocus() // wrapped straight back to itself
  await user.tab({ shift: true })
  expect(close).toHaveFocus()
  a.remove()
})

test('closing returns focus to the anchor chip', () => {
  const a = anchor()
  const { unmount } = render(
    <ReactorsModal serverId={1} emoji="+1" count={1} mine={false} meId="u-alice" meAvatar="" users={[]} anchorEl={a} onClose={() => {}} />,
  )
  unmount()
  expect(a).toHaveFocus()
  a.remove()
})
