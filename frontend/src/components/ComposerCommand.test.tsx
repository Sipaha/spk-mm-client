import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'
import { ApiError } from '../api/client'
import type { AttachmentView } from '../api/types'
import { setLocale } from '../i18n'
import { Composer } from './Composer'

vi.mock('../api/client', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/client')>()
  return {
    ...real,
    isDesktop: vi.fn(() => false),
    client: {
      ...real.client,
      getFormattingBarHidden: vi.fn().mockResolvedValue(false),
      setFormattingBarHidden: vi.fn().mockResolvedValue(undefined),
      autocomplete: vi.fn().mockResolvedValue({ users: [], others: [], channels: [], emoji: [], commands: [] }),
    },
  }
})
vi.mock('../chat', () => ({
  pickAttachments: vi.fn(), attachFromClipboard: vi.fn(), uploadAttachments: vi.fn(), removeAttachment: vi.fn(), retryAttachment: vi.fn(),
}))

beforeEach(() => setLocale('en'))

function setup(attachments: AttachmentView[] = []) {
  const onSend = vi.fn().mockResolvedValue(undefined)
  const onCommand = vi.fn().mockResolvedValue(undefined)
  const onDraft = vi.fn()
  render(
    <Composer channelId="c1" channelName="Town Square" draft="" serverId={1} attachments={attachments}
      onSend={onSend} onCommand={onCommand} onDraft={onDraft} onEditLast={() => {}} />,
  )
  return { box: screen.getByLabelText('Message') as HTMLTextAreaElement, onSend, onCommand }
}

test('a message starting with / is executed, not posted', async () => {
  const { box, onSend, onCommand } = setup([{ id: 'a1', name: 'x.png', size: 1, mime: 'image/png', state: 'uploaded', sent: 1, error: '' }])
  await userEvent.type(box, '/echo hi{Escape}{Enter}')
  expect(onCommand).toHaveBeenCalledWith('/echo hi')
  expect(onSend).not.toHaveBeenCalled()
  expect(box.value).toBe('')
})

test('an unknown command keeps the text and offers to send it as a message; Enter again sends it', async () => {
  const { box, onSend, onCommand } = setup()
  onCommand.mockRejectedValueOnce(new ApiError('command_not_found', ''))
  await userEvent.type(box, '/nope x{Enter}')
  expect(box.value).toBe('/nope x')
  expect(await screen.findByRole('alert')).toHaveTextContent("Command with a trigger of '/nope' not found.")
  expect(screen.getByRole('button', { name: 'Click here to send as a message.' })).toBeInTheDocument()
  await userEvent.keyboard('{Enter}')
  expect(onSend).toHaveBeenCalledWith('/nope x', [])
  expect(onCommand).toHaveBeenCalledTimes(1)
  expect(screen.queryByRole('alert')).toBeNull()
})

test('the "send as a message" button sends the text as it is', async () => {
  const { box, onSend, onCommand } = setup()
  onCommand.mockRejectedValueOnce(new ApiError('command_not_found', ''))
  await userEvent.type(box, '/nope{Escape}{Enter}')
  await userEvent.click(await screen.findByRole('button', { name: 'Click here to send as a message.' }))
  expect(onSend).toHaveBeenCalledWith('/nope', [])
  expect(box.value).toBe('')
})

test('an edited text runs as a command again; another failure keeps the text with its error', async () => {
  const { box, onCommand } = setup()
  onCommand.mockRejectedValueOnce(new ApiError('command_not_found', '')).mockRejectedValueOnce(new ApiError('forbidden', ''))
  await userEvent.type(box, '/nope{Escape}{Enter}')
  await screen.findByRole('alert')
  await userEvent.type(box, 'x{Escape}{Enter}')
  expect(onCommand).toHaveBeenLastCalledWith('/nopex')
  expect(box.value).toBe('/nopex')
  expect(await screen.findByRole('alert')).not.toHaveTextContent('Command with a trigger')
})
