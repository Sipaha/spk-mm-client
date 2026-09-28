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

test('clicking an item closes the menu, returns focus to the anchor, and calls its handler', async () => {
  const { onMarkUnread, onClose } = setup(true)
  await userEvent.click(screen.getByRole('menuitem', { name: 'Mark as unread' }))
  expect(onMarkUnread).toHaveBeenCalledTimes(1)
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(document.activeElement).toBe(anchorEl) // fix round 1
})

test('Delete is styled as dangerous, returns focus to the anchor, and calls its handler', async () => {
  const { onDelete } = setup(true)
  await userEvent.click(screen.getByRole('menuitem', { name: 'Delete' }))
  expect(onDelete).toHaveBeenCalledTimes(1)
  expect(document.activeElement).toBe(anchorEl)
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

test('Escape closes the menu via a document-level listener and returns focus to the anchor', async () => {
  const { onClose } = setup(true)
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(document.activeElement).toBe(anchorEl) // fix round 1
})

// fix round 1 (controller review of e63449f): a click outside is an
// "external" close — the user's click already moved their attention
// elsewhere, so this must not steal focus back to the anchor.
test('a click outside (not on the anchor) closes the menu without stealing focus', () => {
  const { onClose } = setup(true)
  expect(screen.getByRole('menuitem', { name: 'Mark as unread' })).toHaveFocus() // opened with focus in the menu
  fireEvent.mouseDown(document.body)
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(document.activeElement).not.toBe(anchorEl)
})

test('a mousedown on the anchor does not close the menu (so its own click can toggle it)', () => {
  const { onClose } = setup(true)
  fireEvent.mouseDown(anchorEl)
  expect(onClose).not.toHaveBeenCalled()
})

// fix round 1: this menu does not trap Tab — the controller's finding was
// that Tab past the last item moved focus to <body> while the menu stayed
// open, because nothing was listening for focus leaving the menu.
test('focus leaving both the menu and the anchor (e.g. Tab past the last item) closes the menu, without forcing focus back', () => {
  const { onClose } = setup(true)
  const last = screen.getByRole('menuitem', { name: 'Delete' })
  last.focus()
  const outside = document.createElement('button')
  outside.textContent = 'elsewhere'
  document.body.appendChild(outside)
  fireEvent.blur(last, { relatedTarget: outside })
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(document.activeElement).not.toBe(anchorEl)
  outside.remove()
})

test('focus moving between two items in the menu (e.g. Tab, not just the arrow keys) does not close it', () => {
  const { onClose } = setup(true)
  const first = screen.getByRole('menuitem', { name: 'Mark as unread' })
  const second = screen.getByRole('menuitem', { name: 'Copy link' })
  first.focus()
  fireEvent.blur(first, { relatedTarget: second })
  expect(onClose).not.toHaveBeenCalled()
})

test('focus moving from an item back to the anchor (e.g. Shift+Tab from the first item) does not close it', () => {
  const { onClose } = setup(true)
  const first = screen.getByRole('menuitem', { name: 'Mark as unread' })
  first.focus()
  fireEvent.blur(first, { relatedTarget: anchorEl })
  expect(onClose).not.toHaveBeenCalled()
})

// The controller's exact repro, with real Tab key presses rather than a
// synthetic blur: Tab through all 4 items and one more past Delete.
test('real Tab key presses through every item and past the last one close the menu (nothing in this document survives it silently open)', async () => {
  const { onClose } = setup(true)
  await userEvent.tab() // → Copy link
  await userEvent.tab() // → Edit
  await userEvent.tab() // → Delete (last item)
  expect(onClose).not.toHaveBeenCalled()
  await userEvent.tab() // past the last item: nowhere else in the document to go
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('unmounting the menu on its own (not via Esc/an item) does not move focus', () => {
  anchorEl.focus()
  const { unmount } = setup(true)
  expect(document.activeElement).not.toBe(anchorEl) // opened with focus in the menu
  unmount()
  expect(document.activeElement).not.toBe(anchorEl) // still not — no unmount-time focus stealing
})
