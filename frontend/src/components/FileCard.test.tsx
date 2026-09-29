import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { FileCard } from './FileCard'

vi.mock('../chat', () => ({ revealSavedFile: vi.fn().mockResolvedValue(undefined), fileKey: (s: number, f: string) => `${s}/${f}` }))

const F = (o: Partial<FileView>): FileView => ({ id: 'f', name: 'x', size: 2048, mime: '', ...o })
const handlers = () => ({ onDownload: vi.fn(), onOpen: vi.fn(), onView: vi.fn() })

beforeEach(() => {
  setLocale('en')
  useStore.setState({ fileSaves: {}, downloads: [], toast: null })
})

// Official-client sizing (file-cards brief, 2026-09-30): fixed 320x64,
// shrinking to 204px wide before content truncates any further, never past
// its container (a 320px-wide thread panel).
test('card sizing: fixed width/height, shrinks down to a floor before overflowing its container', () => {
  const h = handlers()
  const { container } = render(<FileCard serverId={1} file={F({ name: 'spec.pdf', ext: 'pdf', mime: 'application/pdf' })} {...h} />)
  // The width/min/max trio lives on the outer wrapper, not the bordered
  // ".group" div one level in — see FileCard.tsx's comment: a flex item
  // with no width of its own is intrinsically sized by a descendant's
  // *specified* width, ignoring percentages like max-w-full (no definite
  // containing block yet during that pass), so putting the clamp there
  // would silently stop clamping once this card sits in a real flex-wrap
  // row (only caught by the narrow-thread-panel screenshot, not this
  // jsdom test — jsdom has no layout engine to reproduce it).
  const outer = container.firstElementChild!
  expect(outer).toHaveClass('w-80', 'min-w-[204px]', 'max-w-full', 'shrink')
  const card = container.querySelector('.group')!
  expect(card).toHaveClass('h-16', 'w-full')
})

test.each([
  ['spec.pdf', 'pdf', 'application/pdf', 'PDF', 'text-file-pdf'],
  ['report.docx', 'docx', 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', 'DOCX', 'text-file-document'],
  ['nums.xlsx', 'xlsx', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'XLSX', 'text-file-spreadsheet'],
  ['deck.pptx', 'pptx', 'application/vnd.openxmlformats-officedocument.presentationml.presentation', 'PPTX', 'text-file-presentation'],
  ['stuff.zip', 'zip', 'application/zip', 'ZIP', 'text-file-archive'],
  ['unknown.bin', 'bin', 'application/octet-stream', 'BIN', 'text-fg-muted'],
])('type mapping: %s gets the %s icon colour and a "%s <size>" meta line', (name, ext, mime, label, colorClass) => {
  const h = handlers()
  const { container } = render(<FileCard serverId={1} file={F({ name, ext, mime, size: 34719 })} {...h} onView={undefined} />)
  expect(screen.getByText(`${label} 34KB`)).toBeInTheDocument()
  expect(container.querySelector('svg')).toHaveClass(colorClass)
})

test('previewable (onView given): the card\'s name/icon area is the "View" action, not a download', async () => {
  const h = handlers()
  const file = F({ name: 'spec.pdf', ext: 'pdf', mime: 'application/pdf' })
  render(<FileCard serverId={1} file={file} {...h} />)
  const primary = screen.getByRole('button', { name: 'View spec.pdf' })
  await userEvent.click(primary)
  expect(h.onView).toHaveBeenCalledWith(file)
  expect(h.onDownload).not.toHaveBeenCalled()
  // Distinct from the secondary Download button — no duplicate accessible name.
  expect(screen.getAllByRole('button', { name: /spec\.pdf$/ })).toHaveLength(3) // View, Download, Open
})

test('non-previewable (no onView): the card\'s name/icon area downloads, labelled by the bare file name — never "Download <name>" (reserved for the secondary button)', async () => {
  const h = handlers()
  const file = F({ name: 'stuff.zip', ext: 'zip', mime: 'application/zip' })
  render(<FileCard serverId={1} file={file} onDownload={h.onDownload} onOpen={h.onOpen} />)
  const primary = screen.getByRole('button', { name: 'stuff.zip' })
  await userEvent.click(primary)
  expect(h.onDownload).toHaveBeenCalledWith(file)
  expect(h.onView).not.toHaveBeenCalled()
  // Exactly one "Download stuff.zip" (the secondary button) — e2e's exact
  // getByRole('button', { name: 'Download stuff.zip' }).click() must not
  // hit a strict-mode multiple-match error.
  expect(screen.getAllByRole('button', { name: 'Download stuff.zip' })).toHaveLength(1)
  expect(screen.getByRole('button', { name: 'Open stuff.zip' })).toBeInTheDocument()
})

test('secondary actions (download, open) are reserved but hidden until hover/focus-within, unless a download is in flight or already saved', () => {
  const h = handlers()
  const file = F({ name: 'stuff.zip', ext: 'zip', mime: 'application/zip' })
  const { container, rerender } = render(<FileCard serverId={1} file={file} onDownload={h.onDownload} onOpen={h.onOpen} />)
  const actions = () => container.querySelector('.group > div:last-child')!
  expect(actions()).toHaveClass('opacity-0', 'group-hover:opacity-100', 'group-focus-within:opacity-100')
  expect(actions()).not.toHaveClass('opacity-100')
  // Still present in the DOM (not display:none) and not disabled — a
  // Playwright click reaches it without a real prior hover, same as every
  // existing e2e download/open assertion.
  expect(screen.getByRole('button', { name: 'Download stuff.zip' })).toBeEnabled()

  act(() => useStore.getState().setFileSave('1/f', { state: 'saving', path: '', savedAt: 0 }))
  rerender(<FileCard serverId={1} file={file} onDownload={h.onDownload} onOpen={h.onOpen} />)
  expect(actions()).toHaveClass('opacity-100')

  act(() => useStore.getState().setFileSave('1/f', { state: 'saved', path: '/d/stuff.zip', savedAt: Date.now() }))
  rerender(<FileCard serverId={1} file={file} onDownload={h.onDownload} onOpen={h.onOpen} />)
  expect(actions()).toHaveClass('opacity-100')
})

test('a staged file (no server id yet) has no clickable action at all — plain name/meta, no buttons', () => {
  const file: FileView = { id: 'a1', name: 'notes.docx', size: 1000, mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', staged: true }
  render(<FileCard serverId={1} file={file} onDownload={vi.fn()} onOpen={vi.fn()} />)
  expect(screen.getByText('notes.docx')).toBeInTheDocument()
  expect(screen.getByText('DOCX 1000B')).toBeInTheDocument()
  expect(screen.queryByRole('button')).toBeNull()
})
