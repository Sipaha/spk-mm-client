import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import type { SidebarDTO } from '../api/types'
import { useStore } from '../store'
import { Markdown } from './Markdown'
import { splitChannels } from './remarkChannelMentions'

vi.mock('../chat', async (importOriginal) => ({ ...(await importOriginal<typeof import('../chat')>()), openChannel: vi.fn() }))
const { openChannel } = await import('../chat')

const sidebar: SidebarDTO = {
  team_id: 't', selected_channel_id: 'c-town', teams: [],
  categories: [{ id: 'x', type: 'channels', name: 'Channels', collapsed: false, channels: [
    { id: 'c-off', name: 'Off-Topic', type: 'O', unread: false, mentions: 0, muted: false, slug: 'off-topic' },
  ] }],
}

beforeEach(() => useStore.setState({ selectedId: 1, sidebar }))
afterEach(() => useStore.setState({ selectedId: null, sidebar: null }))

test('splitChannels finds ~names, not inside words, trailing dots stay text', () => {
  expect(splitChannels('see ~off-topic. and a~b ~~x')).toEqual([
    { text: 'see ' },
    { channel: 'off-topic', raw: 'off-topic' },
    { text: '. and a~b ~~x' },
  ])
})

test('a known ~channel is a link to it by its display name; an unknown one stays text; code is untouched', async () => {
  const { container } = render(<Markdown text={'go to ~off-topic or ~nope `~off-topic`'} me="alice" onLink={() => {}} serverId={1} />)
  const link = screen.getByRole('link', { name: '~Off-Topic' })
  expect(container.textContent).toContain('~nope')
  expect(container.querySelector('code')!.textContent).toBe('~off-topic')
  await userEvent.click(link)
  expect(openChannel).toHaveBeenCalledWith(1, 'c-off')
})

test('another server\'s message does not resolve against this sidebar', () => {
  render(<Markdown text={'~off-topic'} me="alice" onLink={() => {}} serverId={2} />)
  expect(screen.queryByRole('link')).toBeNull()
})
