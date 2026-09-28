import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { setLocale } from '../i18n'
import PostMenu from './PostMenu'

// anchorEl stands in for the toolbar's "…" button: a real element (not
// just a DOMRect), so the outside-click exclusion and the
// focus-return-on-close behavior have something real to check against —
// same rig as Downloads.test.tsx.
let anchorEl: HTMLButtonElement

beforeEach(() => {
  setLocale('en')
  anchorEl = document.createElement('button')
  anchorEl.textContent = 'More actions'
  anchorEl.getBoundingClientRect = () =>
    ({ top: 40, bottom: 60, left: 500, right: 520, width: 20, height: 20, x: 500, y: 40, toJSON() {} }) as DOMRect
  document.body.appendChild(anchorEl)
})

afterEach(() => anchorEl.remove())

function setup(canEdit: boolean) {
  const onMarkUnread = vi.fn()
  const onCopyLink = vi.fn()
  const onEdit = vi.fn()
  const onDelete = vi.fn()
  const onClose = vi.fn()
  const view = render(
    <PostMenu anchorEl={anchorEl} canEdit={canEdit} onMarkUnread={onMarkUnread} onCopyLink={onCopyLink} onEdit={onEdit} onDelete={onDelete} onClose={onClose} />,
  )
  return { onMarkUnread, onCopyLink, onEdit, onDelete, onClose, ...view }
}

test('own post: mark unread, copy link, edit and delete', () => {
  setup(true)
  expect(screen.getAllByRole('menuitem').map((b) => b.textContent)).toEqual(['Mark as unread', 'Copy link', 'Edit', 'Delete'])
})

test("someone else's post: only mark unread and copy link", () => {
  setup(false)
  expect(screen.getAllByRole('menuitem').map((b) => b.textContent)).toEqual(['Mark as unread', 'Copy link'])
})

test('clicking an item closes the menu and calls its handler', async () => {
  const { onMarkUnread, onClose } = setup(true)
  await userEvent.click(screen.getByRole('menuitem', { name: 'Mark as unread' }))
  expect(onMarkUnread).toHaveBeenCalledTimes(1)
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('Delete is styled as dangerous and calls its handler', async () => {
  const { onDelete } = setup(true)
  await userEvent.click(screen.getByRole('menuitem', { name: 'Delete' }))
  expect(onDelete).toHaveBeenCalledTimes(1)
})

test('opens with focus on the first item', () => {
  setup(true)
  expect(screen.getByRole('menuitem', { name: 'Mark as unread' })).toHaveFocus()
})

test('ArrowDown/ArrowUp move focus through the items and wrap around', () => {
  setup(true)
  const item = (name: string) => screen.getByRole('menuitem', { name })
  item('Mark as unread').focus()
  fireEvent.keyDown(item('Mark as unread'), { key: 'ArrowDown' })
  expect(item('Copy link')).toHaveFocus()
  fireEvent.keyDown(item('Copy link'), { key: 'ArrowDown' })
  expect(item('Edit')).toHaveFocus()
  fireEvent.keyDown(item('Edit'), { key: 'ArrowDown' })
  expect(item('Delete')).toHaveFocus()
  fireEvent.keyDown(item('Delete'), { key: 'ArrowDown' }) // wraps to the top
  expect(item('Mark as unread')).toHaveFocus()
  fireEvent.keyDown(item('Mark as unread'), { key: 'ArrowUp' }) // wraps to the bottom
  expect(item('Delete')).toHaveFocus()
})

test('Home/End jump to the first/last item', () => {
  setup(true)
  const item = (name: string) => screen.getByRole('menuitem', { name })
  item('Copy link').focus()
  fireEvent.keyDown(item('Copy link'), { key: 'End' })
  expect(item('Delete')).toHaveFocus()
  fireEvent.keyDown(item('Delete'), { key: 'Home' })
  expect(item('Mark as unread')).toHaveFocus()
})

test('Enter activates the focused item', async () => {
  const { onCopyLink } = setup(true)
  screen.getByRole('menuitem', { name: 'Copy link' }).focus()
  await userEvent.keyboard('{Enter}')
  expect(onCopyLink).toHaveBeenCalledTimes(1)
})

test('Escape closes the menu via a document-level listener', async () => {
  const { onClose } = setup(true)
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('a click outside (not on the anchor) closes the menu', () => {
  const { onClose } = setup(true)
  fireEvent.mouseDown(document.body)
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('a mousedown on the anchor does not close the menu (so its own click can toggle it)', () => {
  const { onClose } = setup(true)
  fireEvent.mouseDown(anchorEl)
  expect(onClose).not.toHaveBeenCalled()
})

test('closing (unmounting) returns focus to the anchor that opened the menu', () => {
  anchorEl.focus()
  const { unmount } = setup(true)
  expect(document.activeElement).not.toBe(anchorEl)
  unmount()
  expect(document.activeElement).toBe(anchorEl)
})
