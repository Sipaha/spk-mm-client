import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'
import { setLocale } from '../i18n'
import { Composer } from './Composer'
import { recordAudio } from '../dictationAudio'
import { client } from '../api/client'

vi.mock('../dictationAudio', () => ({ recordAudio: vi.fn(), MAX_RECORDING_SECONDS: 60 }))
vi.mock('../api/client', async (original) => {
  const real = await original<typeof import('../api/client')>()
  return { ...real, client: { ...real.client, transcribeAudio: vi.fn(), getFormattingBarHidden: vi.fn().mockResolvedValue(false) } }
})
const props = () => ({ serverId: 1, channelId: 'c1', channelName: 'Channel', draft: 'Draft', attachments: [], onSend: vi.fn().mockResolvedValue(undefined), onDraft: vi.fn(), onEditLast: vi.fn() })
const stop = vi.fn().mockResolvedValue('encoded-wav')
const cancel = vi.fn()
beforeEach(() => {
  setLocale('en'); vi.clearAllMocks()
  vi.mocked(recordAudio).mockResolvedValue({ stop, cancel })
  vi.mocked(client.transcribeAudio).mockResolvedValue('recognized words')
})

test('dictates into the latest editable draft, saves it, and posts only on explicit send', async () => {
  const p = props(); render(<Composer {...p} />)
  fireEvent.click(screen.getByRole('button', { name: 'Dictate message' }))
  await screen.findByRole('button', { name: 'Finish dictation' })
  expect(screen.getByRole('button', { name: 'Send message' })).toBeDisabled()
  fireEvent.change(screen.getByRole('textbox', { name: 'Message' }), { target: { value: 'Edited while recording' } })
  fireEvent.click(screen.getByRole('button', { name: 'Finish dictation' }))
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('Edited while recording recognized words'))
  expect(p.onDraft).toHaveBeenLastCalledWith('Edited while recording recognized words')
  expect(p.onSend).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Send message' }))
  expect(p.onSend).toHaveBeenCalledWith('Edited while recording recognized words', [])
})

test('cancel during recognition aborts the API and ignores late results, retaining the draft', async () => {
  let resolve!: (text: string) => void
  vi.mocked(client.transcribeAudio).mockReturnValue(new Promise((r) => { resolve = r }))
  const p = props(); render(<Composer {...p} rootId="thread" />)
  fireEvent.click(screen.getByRole('button', { name: 'Dictate message' }))
  fireEvent.click(await screen.findByRole('button', { name: 'Finish dictation' }))
  await waitFor(() => expect(client.transcribeAudio).toHaveBeenCalled())
  const signal = vi.mocked(client.transcribeAudio).mock.calls[0][1]!
  fireEvent.click(screen.getByRole('button', { name: 'Cancel dictation' }))
  expect(signal.aborted).toBe(true)
  await act(async () => resolve('late text'))
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('Draft')
  expect(p.onSend).not.toHaveBeenCalled()
})

test('leaving the composer cancels an outstanding microphone permission request', async () => {
  let resolve!: (v: { stop: typeof stop; cancel: typeof cancel }) => void
  vi.mocked(recordAudio).mockReturnValue(new Promise((r) => { resolve = r }))
  const { unmount } = render(<Composer {...props()} />)
  fireEvent.click(screen.getByRole('button', { name: 'Dictate message' }))
  const signal = vi.mocked(recordAudio).mock.calls[0][0]
  unmount()
  expect(signal.aborted).toBe(true)
  await act(async () => resolve({ stop, cancel }))
  expect(cancel).toHaveBeenCalled()
  expect(client.transcribeAudio).not.toHaveBeenCalled()
})

test('permission denied keeps the draft and explains how to retry', async () => {
  vi.mocked(recordAudio).mockRejectedValue(new DOMException('denied', 'NotAllowedError'))
  render(<Composer {...props()} />)
  fireEvent.click(screen.getByRole('button', { name: 'Dictate message' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Allow microphone access')
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('Draft')
})

test('the duration limit stops once and inserts recognition without a second click', async () => {
  render(<Composer {...props()} />)
  fireEvent.click(screen.getByRole('button', { name: 'Dictate message' }))
  await screen.findByRole('button', { name: 'Finish dictation' })
  await act(async () => { vi.mocked(recordAudio).mock.calls[0][1](); vi.mocked(recordAudio).mock.calls[0][1]() })
  expect(stop).toHaveBeenCalledTimes(1)
  expect(client.transcribeAudio).toHaveBeenCalledTimes(1)
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('Draft recognized words')
})
