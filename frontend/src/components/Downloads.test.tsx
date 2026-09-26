import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { DownloadView } from '../api/types'
import { formatSize } from '../format'
import { setLocale } from '../i18n'
import Downloads, { placePanel } from './Downloads'

const dl = (over: Partial<DownloadView> = {}): DownloadView => ({
  id: 1, server_id: 1, file_id: 'f1', name: 'spec.pdf', path: '/d/spec.pdf', size: 12_300_000, mime: 'application/pdf',
  started_at: 0, finished_at: 1_758_700_000_000, state: 'done', error: '', received: 12_300_000, exists: true,
  openable: true, ...over,
})

// primaryAction mirrors chat.ts's downloadPrimaryAction (unit-tested on its
// own in chat.test.ts): this test only checks Downloads wires a row's
// button/Enter to whatever the caller's decision returns.
function primaryAction(d: DownloadView, onOpen: (id: number) => void, onReveal: (id: number) => void) {
  if (d.state !== 'done' || !d.exists) return null
  return d.openable ? () => onOpen(d.id) : () => onReveal(d.id)
}

// anchorEl stands in for the header's ⬇ button: a real element (not just a
// DOMRect), so the outside-click exclusion and the focus-return-on-close
// behavior have something real to check against.
let anchorEl: HTMLButtonElement

beforeEach(() => {
  setLocale('en')
  anchorEl = document.createElement('button')
  anchorEl.textContent = 'Downloads'
  anchorEl.getBoundingClientRect = () =>
    ({ top: 40, bottom: 60, left: 500, right: 520, width: 20, height: 20, x: 500, y: 40, toJSON() {} }) as DOMRect
  document.body.appendChild(anchorEl)
})

afterEach(() => anchorEl.remove())

function setup(downloads: DownloadView[]) {
  const onOpen = vi.fn()
  const onReveal = vi.fn()
  const onRemove = vi.fn()
  const onClear = vi.fn()
  const onClose = vi.fn()
  const view = render(
    <Downloads
      anchorEl={anchorEl}
      downloads={downloads}
      locale="en-US"
      primaryAction={(d) => primaryAction(d, onOpen, onReveal)}
      onOpen={onOpen}
      onReveal={onReveal}
      onRemove={onRemove}
      onClear={onClear}
      onClose={onClose}
    />,
  )
  return { onOpen, onReveal, onRemove, onClear, onClose, ...view }
}

test('an empty list says so, outside role="list"', () => {
  setup([])
  expect(screen.getByText('No downloads yet')).toBeInTheDocument()
  expect(screen.queryByRole('list')).toBeNull()
})

test('row states: done, downloading (with a progress bar), failed, deleted', () => {
  setup([
    dl({ id: 1, name: 'spec.pdf' }),
    dl({ id: 2, name: 'movie.mp4', state: 'downloading', received: 3_100_000, size: 12_300_000 }),
    dl({ id: 3, name: 'boom.zip', state: 'failed', error: 'no_file' }),
    dl({ id: 4, name: 'gone.txt', state: 'done', exists: false }),
  ])
  const rows = screen.getAllByRole('listitem')
  expect(within(rows[0]).getByText(new RegExp(formatSize(12_300_000).replace('.', '\\.')))).toBeInTheDocument()
  expect(within(rows[1]).getByText(`${formatSize(3_100_000)} of ${formatSize(12_300_000)}`)).toBeInTheDocument()
  expect(within(rows[2]).getByText('Error: File not found')).toBeInTheDocument()
  expect(within(rows[3]).getByText('File deleted')).toBeInTheDocument()
  // only a finished, present file offers Open/Show in folder; downloading and deleted do not
  expect(within(rows[0]).getByRole('button', { name: 'Open spec.pdf' })).toBeInTheDocument()
  expect(within(rows[0]).getByRole('button', { name: 'Show spec.pdf in folder' })).toBeInTheDocument()
  // row 1 (downloading) still has its primary (inert) button, but no action buttons
  expect(within(rows[1]).getAllByRole('button')).toHaveLength(1)
  expect(within(rows[3]).queryByRole('button', { name: /Open|Show/ })).toBeNull()
  expect(within(rows[3]).getByRole('button', { name: 'Remove gone.txt from the list' })).toBeInTheDocument()
})

test('a non-openable finished file offers only "show in folder"', () => {
  setup([dl({ openable: false })])
  const row = screen.getAllByRole('listitem')[0]
  expect(within(row).queryByRole('button', { name: /^Open/ })).toBeNull()
  expect(within(row).getByRole('button', { name: /Show .* in folder/ })).toBeInTheDocument()
})

test('action buttons call the matching handler and do not also trigger the row', async () => {
  const { onOpen, onReveal, onRemove } = setup([dl()])
  const row = screen.getAllByRole('listitem')[0]
  await userEvent.click(within(row).getByRole('button', { name: 'Open spec.pdf' }))
  expect(onOpen).toHaveBeenCalledTimes(1)
  await userEvent.click(within(row).getByRole('button', { name: 'Show spec.pdf in folder' }))
  expect(onReveal).toHaveBeenCalledTimes(1)
  await userEvent.click(within(row).getByRole('button', { name: 'Remove spec.pdf from the list' }))
  expect(onRemove).toHaveBeenCalledWith(1)
  expect(onOpen).toHaveBeenCalledTimes(1) // still just the one real click on it
})

test('Enter on the Remove button calls only remove, not the row primary action', async () => {
  const { onOpen, onReveal, onRemove } = setup([dl()])
  const row = screen.getAllByRole('listitem')[0]
  const removeBtn = within(row).getByRole('button', { name: 'Remove spec.pdf from the list' })
  removeBtn.focus()
  await userEvent.keyboard('{Enter}')
  expect(onRemove).toHaveBeenCalledWith(1)
  expect(onOpen).not.toHaveBeenCalled()
  expect(onReveal).not.toHaveBeenCalled()
})

test('clicking a finished row runs its primary action (open); a non-openable one shows its folder', async () => {
  const { onOpen } = setup([dl({ id: 5 })])
  await userEvent.click(screen.getByText('spec.pdf'))
  expect(onOpen).toHaveBeenCalledWith(5)
})

test('a downloading or deleted row has no primary action', async () => {
  const { onOpen, onReveal } = setup([dl({ id: 6, state: 'downloading' })])
  await userEvent.click(screen.getByText('spec.pdf'))
  expect(onOpen).not.toHaveBeenCalled()
  expect(onReveal).not.toHaveBeenCalled()
})

test('Enter on a focused row runs its primary action', async () => {
  const { onOpen } = setup([dl({ id: 7 })])
  const row = screen.getAllByRole('listitem')[0]
  within(row).getByText('spec.pdf').closest('button')!.focus()
  await userEvent.keyboard('{Enter}')
  expect(onOpen).toHaveBeenCalledWith(7)
})

test('ArrowDown/ArrowUp move focus between the rows’ primary buttons', () => {
  setup([dl({ id: 1, name: 'a' }), dl({ id: 2, name: 'b' }), dl({ id: 3, name: 'c' })])
  const btn = (name: string) => screen.getByText(name).closest('button')!
  btn('a').focus()
  fireEvent.keyDown(btn('a'), { key: 'ArrowDown' })
  expect(btn('b')).toHaveFocus()
  fireEvent.keyDown(btn('b'), { key: 'ArrowDown' })
  expect(btn('c')).toHaveFocus()
  fireEvent.keyDown(btn('c'), { key: 'ArrowDown' }) // no row below: stays put
  expect(btn('c')).toHaveFocus()
  fireEvent.keyDown(btn('c'), { key: 'ArrowUp' })
  expect(btn('b')).toHaveFocus()
})

test('"Clear the list" calls onClear', async () => {
  const { onClear } = setup([dl()])
  await userEvent.click(screen.getByRole('button', { name: 'Clear the list' }))
  expect(onClear).toHaveBeenCalledTimes(1)
})

test('opening moves focus into the panel, and Esc closes it via a document-level listener regardless of focus', async () => {
  anchorEl.focus() // simulates focus having stayed on ⬇ right up to the panel mounting
  const { onClose } = setup([dl()])
  expect(document.activeElement).not.toBe(anchorEl) // moved into the panel on open
  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('closing (unmounting) returns focus to the anchor that opened the panel', () => {
  anchorEl.focus()
  const { unmount } = setup([dl()])
  expect(document.activeElement).not.toBe(anchorEl)
  unmount()
  expect(document.activeElement).toBe(anchorEl)
})

test('an empty list still moves focus to the panel itself (no rows to focus)', () => {
  setup([])
  expect(screen.getByRole('dialog')).toHaveFocus()
})

test('a click outside (not on the anchor) closes the panel', () => {
  const { onClose } = setup([dl()])
  fireEvent.mouseDown(document.body)
  expect(onClose).toHaveBeenCalledTimes(1)
})

test('a mousedown on the anchor does not close the panel (so its own click can toggle it)', () => {
  const { onClose } = setup([dl()])
  fireEvent.mouseDown(anchorEl)
  expect(onClose).not.toHaveBeenCalled()
})

test('placePanel puts the panel below its anchor, or above when there is no room', () => {
  expect(placePanel({ top: 40, bottom: 60, right: 520 }, 1400, 900)).toEqual({ left: 160, top: 64 })
  expect(placePanel({ top: 850, bottom: 870, right: 50 }, 1400, 900, 420)).toEqual({ left: 8, top: 426 })
})
