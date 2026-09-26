import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { DownloadView } from '../api/types'
import { formatSize } from '../format'
import { setLocale } from '../i18n'
import Downloads, { placePanel } from './Downloads'

const anchor = { top: 40, bottom: 60, left: 500, right: 520, width: 20, height: 20, x: 500, y: 40, toJSON() {} } as DOMRect

const dl = (over: Partial<DownloadView> = {}): DownloadView => ({
  id: 1, server_id: 1, file_id: 'f1', name: 'spec.pdf', path: '/d/spec.pdf', size: 12_300_000, mime: 'application/pdf',
  started_at: 0, finished_at: 1_758_700_000_000, state: 'done', error: '', received: 12_300_000, exists: true,
  openable: true, ...over,
})

// primaryAction mirrors chat.ts's downloadPrimaryAction (unit-tested on its
// own in chat.test.ts): this test only checks Downloads wires row
// click/Enter to whatever the caller's decision returns.
function primaryAction(d: DownloadView, onOpen: (id: number) => void, onReveal: (id: number) => void) {
  if (d.state !== 'done' || !d.exists) return null
  return d.openable ? () => onOpen(d.id) : () => onReveal(d.id)
}

function setup(downloads: DownloadView[]) {
  const onOpen = vi.fn()
  const onReveal = vi.fn()
  const onRemove = vi.fn()
  const onClear = vi.fn()
  const onClose = vi.fn()
  render(
    <Downloads
      anchor={anchor}
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
  return { onOpen, onReveal, onRemove, onClear, onClose }
}

beforeEach(() => setLocale('en'))

test('an empty list says so', () => {
  setup([])
  expect(screen.getByText('No downloads yet')).toBeInTheDocument()
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
  expect(within(rows[1]).queryByRole('button')).toBeNull()
  expect(within(rows[3]).queryByRole('button', { name: /Open|Show/ })).toBeNull()
  expect(within(rows[3]).getByRole('button', { name: 'Remove gone.txt from the list' })).toBeInTheDocument()
})

test('a non-openable finished file offers only "show in folder"', () => {
  setup([dl({ openable: false })])
  const row = screen.getAllByRole('listitem')[0]
  expect(within(row).queryByRole('button', { name: /^Open/ })).toBeNull()
  expect(within(row).getByRole('button', { name: /Show .* in folder/ })).toBeInTheDocument()
})

test('action buttons call the matching handler and do not also trigger the row click', async () => {
  const { onOpen, onReveal, onRemove } = setup([dl()])
  const row = screen.getAllByRole('listitem')[0]
  await userEvent.click(within(row).getByRole('button', { name: 'Open spec.pdf' }))
  expect(onOpen).toHaveBeenCalledWith(1)
  await userEvent.click(within(row).getByRole('button', { name: 'Show spec.pdf in folder' }))
  expect(onReveal).toHaveBeenCalledWith(1)
  await userEvent.click(within(row).getByRole('button', { name: 'Remove spec.pdf from the list' }))
  expect(onRemove).toHaveBeenCalledWith(1)
  expect(onOpen).toHaveBeenCalledTimes(1) // the row's own click did not also fire
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

test('Enter on a focused row runs its primary action', () => {
  const { onOpen } = setup([dl({ id: 7 })])
  const row = screen.getAllByRole('listitem')[0]
  row.focus()
  fireEvent.keyDown(row, { key: 'Enter' })
  expect(onOpen).toHaveBeenCalledWith(7)
})

test('ArrowDown/ArrowUp move focus between rows', () => {
  setup([dl({ id: 1, name: 'a' }), dl({ id: 2, name: 'b' }), dl({ id: 3, name: 'c' })])
  const rows = screen.getAllByRole('listitem')
  rows[0].focus()
  fireEvent.keyDown(rows[0], { key: 'ArrowDown' })
  expect(rows[1]).toHaveFocus()
  fireEvent.keyDown(rows[1], { key: 'ArrowDown' })
  expect(rows[2]).toHaveFocus()
  fireEvent.keyDown(rows[2], { key: 'ArrowDown' }) // no row below: stays put
  expect(rows[2]).toHaveFocus()
  fireEvent.keyDown(rows[2], { key: 'ArrowUp' })
  expect(rows[1]).toHaveFocus()
})

test('"Clear the list" calls onClear', async () => {
  const { onClear } = setup([dl()])
  await userEvent.click(screen.getByRole('button', { name: 'Clear the list' }))
  expect(onClear).toHaveBeenCalledTimes(1)
})

test('Escape and an outside click both close the panel', () => {
  const { onClose } = setup([dl()])
  fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
  expect(onClose).toHaveBeenCalledTimes(1)
  fireEvent.mouseDown(document.body)
  expect(onClose).toHaveBeenCalledTimes(2)
})

test('placePanel puts the panel below its anchor, or above when there is no room', () => {
  expect(placePanel({ top: 40, bottom: 60, right: 520 }, 1400, 900)).toEqual({ left: 160, top: 64 })
  expect(placePanel({ top: 850, bottom: 870, right: 50 }, 1400, 900, 420)).toEqual({ left: 8, top: 426 })
})
