import { act, fireEvent, render, screen } from '@testing-library/react'
import { vi } from 'vitest'
import type { DownloadView, FileView } from '../api/types'
import { setLocale } from '../i18n'
import { useStore } from '../store'
import { DownloadButton, SAVED_CHECK_MS } from './DownloadButton'
import { FileCard } from './FileCard'

vi.mock('../chat', () => ({ revealSavedFile: vi.fn().mockResolvedValue(undefined), fileKey: (s: number, f: string) => `${s}/${f}` }))
const { revealSavedFile } = await import('../chat')

const file: FileView = { id: 'f1', name: 'report.zip', size: 1000, mime: 'application/zip' }
const dl = (over: Partial<DownloadView> = {}): DownloadView => ({
  id: 7, server_id: 1, file_id: 'f1', name: 'report.zip', path: '', size: 1000, mime: 'application/zip',
  started_at: 0, finished_at: 0, state: 'downloading', error: '', received: 250, exists: false, openable: false, ...over,
})

beforeEach(() => {
  setLocale('en')
  useStore.setState({ fileSaves: {}, downloads: [], toast: null })
  vi.mocked(revealSavedFile).mockClear()
})
afterEach(() => vi.useRealTimers())

const download = () => screen.getByRole('button', { name: 'Download report.zip' })

test('idle: the download button only, with its plain icon; a click downloads', () => {
  const onDownload = vi.fn()
  render(<FileCard serverId={1} file={file} onDownload={onDownload} onOpen={vi.fn()} />)
  expect(download()).not.toHaveAttribute('aria-busy')
  expect(download().querySelector('[data-state="idle"]')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Show report.zip in folder' })).not.toBeInTheDocument()
  fireEvent.click(download())
  expect(onDownload).toHaveBeenCalledWith(file)
})

test('saving: the button is busy, shows a spinner, then the progress of its downloads-list entry', () => {
  useStore.getState().setFileSave('1/f1', { state: 'saving', path: '', savedAt: 0 })
  const { rerender } = render(<FileCard serverId={1} file={file} onDownload={vi.fn()} onOpen={vi.fn()} />)
  expect(download()).toHaveAttribute('aria-busy', 'true')
  expect(download()).toHaveAttribute('title', 'Downloading report.zip…')
  expect(download().querySelector('[data-state="spinner"]')).toBeInTheDocument()
  act(() => useStore.setState({ downloads: [dl(), dl({ id: 8, server_id: 2 })] }))
  rerender(<FileCard serverId={1} file={file} onDownload={vi.fn()} onOpen={vi.fn()} />)
  const ring = download().querySelector('[data-state="progress"]')!
  expect(ring).toHaveAttribute('data-pct', '25')
})

test('saved: a brief check, then the plain icon again; "Show in folder" stays and reveals the saved path', () => {
  vi.useFakeTimers()
  render(<FileCard serverId={1} file={file} onDownload={vi.fn()} onOpen={vi.fn()} />)
  act(() => useStore.getState().setFileSave('1/f1', { state: 'saved', path: '/d/report.zip', savedAt: Date.now() }))
  expect(download().querySelector('[data-state="saved"]')).toBeInTheDocument()
  expect(download()).toHaveAttribute('title', 'Saved to /d/report.zip')
  const reveal = screen.getByRole('button', { name: 'Show report.zip in folder' })
  expect(reveal).toHaveAttribute('title', 'Show in folder')
  act(() => void vi.advanceTimersByTime(SAVED_CHECK_MS + 10))
  expect(download().querySelector('[data-state="idle"]')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Show report.zip in folder' }))
  expect(revealSavedFile).toHaveBeenCalledWith('/d/report.zip')
})

test('a card mounted long after the save (scrolled back into view) has no check, but keeps "Show in folder"', () => {
  useStore.getState().setFileSave('1/f1', { state: 'saved', path: '/d/report.zip', savedAt: Date.now() - 60_000 })
  render(<FileCard serverId={1} file={file} onDownload={vi.fn()} onOpen={vi.fn()} />)
  expect(download().querySelector('[data-state="idle"]')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Show report.zip in folder' })).toBeInTheDocument()
})

test('the same file id on another server is another file', () => {
  useStore.getState().setFileSave('2/f1', { state: 'saved', path: '/d/other.zip', savedAt: Date.now() })
  render(<FileCard serverId={1} file={file} onDownload={vi.fn()} onOpen={vi.fn()} />)
  expect(screen.queryByRole('button', { name: 'Show report.zip in folder' })).not.toBeInTheDocument()
})

test('the viewer header\'s text variant keeps its "Download" name and gets a "Show in folder" text button', () => {
  useStore.getState().setFileSave('1/f1', { state: 'saved', path: '/d/report.zip', savedAt: Date.now() })
  render(<DownloadButton serverId={1} file={file} onDownload={vi.fn()} text />)
  expect(screen.getByRole('button', { name: 'Download', exact: true } as never)).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Show in folder' }))
  expect(revealSavedFile).toHaveBeenCalledWith('/d/report.zip')
})

test('ru: the reveal button\'s tooltip', () => {
  setLocale('ru')
  useStore.getState().setFileSave('1/f1', { state: 'saved', path: '/d/report.zip', savedAt: 0 })
  render(<FileCard serverId={1} file={file} onDownload={vi.fn()} onOpen={vi.fn()} />)
  expect(screen.getByRole('button', { name: 'Показать report.zip в папке' })).toHaveAttribute('title', 'Показать в папке')
})
