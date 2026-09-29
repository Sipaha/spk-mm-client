import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { setLocale } from '../i18n'
import { IconCode, IconQuote } from './composerIcons'
import { FormattingMenu } from './FormattingMenu'

// anchorEl stands in for the toolbar's "more formatting" button: a real
// element (not just a DOMRect), so the outside-click exclusion and the
// focus-return-on-close behavior have something real to check against —
// same rig as PostMenu.test.tsx.
let anchorEl: HTMLButtonElement

beforeEach(() => {
  setLocale('en')
  anchorEl = document.createElement('button')
  anchorEl.textContent = 'More formatting options'
  anchorEl.getBoundingClientRect = () =>
    ({ top: 800, bottom: 820, left: 500, right: 520, width: 20, height: 20, x: 500, y: 800, toJSON() {} }) as DOMRect
  document.body.appendChild(anchorEl)
})

afterEach(() => anchorEl.remove())

const items = [
  { mode: 'quote' as const, label: 'Quote', Icon: IconQuote },
  { mode: 'code' as const, label: 'Code', Icon: IconCode },
]

function setup() {
  const onPick = vi.fn()
  const onClose = vi.fn()
  const view = render(<FormattingMenu anchorEl={anchorEl} items={items} onPick={onPick} onClose={onClose} />)
  return { onPick, onClose, ...view }
}

test('lists the given items by label, in order', () => {
  setup()
  expect(screen.getAllByRole('menuitem').map((b) => b.textContent)).toEqual(['Quote', 'Code'])
})

test('clicking an item closes the menu, returns focus to the anchor, and reports its mode', async () => {
  const { onPick, onClose } = setup()
  await userEvent.click(screen.getByRole('menuitem', { name: 'Code' }))
  expect(onPick).toHaveBeenCalledWith('code')
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(document.activeElement).toBe(anchorEl)
})

test('opens with focus on the first item', () => {
  setup()
  expect(screen.getByRole('menuitem', { name: 'Quote' })).toHaveFocus()
})

test('ArrowDown/ArrowUp move focus through the items and wrap around', () => {
  setup()
  const item = (name: string) => screen.getByRole('menuitem', { name })
  item('Quote').focus()
  fireEvent.keyDown(item('Quote'), { key: 'ArrowDown' })
  expect(item('Code')).toHaveFocus()
  fireEvent.keyDown(item('Code'), { key: 'ArrowDown' }) // wraps to the top
  expect(item('Quote')).toHaveFocus()
  fireEvent.keyDown(item('Quote'), { key: 'ArrowUp' }) // wraps to the bottom
  expect(item('Code')).toHaveFocus()
})

test('Escape closes the menu via a document-level listener and returns focus to the anchor', async () => {
  const { onClose } = setup()
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(document.activeElement).toBe(anchorEl)
})

// A click outside is an "external" close — must not steal focus back.
test('a click outside (not on the anchor) closes the menu without stealing focus', () => {
  const { onClose } = setup()
  fireEvent.mouseDown(document.body)
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(document.activeElement).not.toBe(anchorEl)
})

test('a mousedown on the anchor does not close the menu (so its own click can toggle it)', () => {
  const { onClose } = setup()
  fireEvent.mouseDown(anchorEl)
  expect(onClose).not.toHaveBeenCalled()
})

test('focus leaving both the menu and the anchor closes it, without forcing focus back', () => {
  const { onClose } = setup()
  const last = screen.getByRole('menuitem', { name: 'Code' })
  last.focus()
  const outside = document.createElement('button')
  document.body.appendChild(outside)
  fireEvent.blur(last, { relatedTarget: outside })
  expect(onClose).toHaveBeenCalledTimes(1)
  outside.remove()
})
