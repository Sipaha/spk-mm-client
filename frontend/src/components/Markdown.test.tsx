import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { ChannelDTO, EmojiDTO, SidebarDTO } from '../api/types'
import { forgetRecent } from '../emoji/recent'
import { useStore } from '../store'
import { Markdown } from './Markdown'
import { emojiNames, isEmojiOnlyText, splitEmoji } from './remarkEmoji'
import { splitMentions } from './remarkMentions'

// Only ChannelMention's ~link test below needs the API client — it drives
// the real chat.ts openChannel (not a mock of openChannel itself), so the
// held-channel capture (sidebar-sections-brief.md fix round 1) is exercised
// end to end for this origin, the same way chat.test.ts does for the
// sidebar-click/notification origins.
vi.mock('../api/client', () => ({
  client: { openChannel: vi.fn(), attachments: vi.fn().mockResolvedValue([]) },
}))
const { client } = await import('../api/client')

afterEach(() => {
  forgetRecent(1)
  forgetRecent(2)
})

test('splitMentions finds usernames, keeps emails and trailing dots out', () => {
  expect(splitMentions('hi @alice, ask @Bob.jones. mail bob@example.com')).toEqual([
    { text: 'hi ' },
    { mention: 'alice', raw: 'alice' },
    { text: ', ask ' },
    { mention: 'bob.jones', raw: 'Bob.jones' },
    { text: '. mail bob@example.com' },
  ])
})

test('markdown: gfm, single newlines break, links via onLink, no raw html, no remote images', async () => {
  const onLink = vi.fn()
  const { container } = render(
    <Markdown
      text={'**bold** and [site](https://example.com)\nnext line\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n<b>raw</b> ![pic](https://evil.test/x.png)'}
      me="alice"
      onLink={onLink}
      serverId={1}
    />,
  )
  expect(screen.getByText('bold').tagName).toBe('STRONG')
  expect(container.querySelector('br')).not.toBeNull()
  expect(container.querySelector('table')).not.toBeNull()
  expect(container.querySelector('b')).toBeNull()
  expect(container.querySelector('img')).toBeNull()
  await userEvent.click(screen.getByRole('link', { name: 'site' }))
  expect(onLink).toHaveBeenCalledWith('https://example.com')
  await userEvent.click(screen.getByRole('link', { name: /pic/ }))
  expect(onLink).toHaveBeenCalledWith('https://evil.test/x.png')
})

test('mentions: me and @channel stand out, others are plain highlights, code is untouched', () => {
  const { container } = render(<Markdown text={'@alice @bob @channel `@alice`'} me="alice" onLink={() => {}} serverId={1} />)
  const spans = [...container.querySelectorAll('[data-mention]')]
  expect(spans.map((s) => s.getAttribute('data-mention'))).toEqual(['alice', 'bob', 'channel'])
  expect(spans[0]).toHaveClass('bg-mention-bg')
  expect(spans[1]).not.toHaveClass('bg-mention-bg')
  expect(spans[2]).toHaveClass('bg-mention-bg')
  expect(container.querySelector('code')!.textContent).toBe('@alice')
})

test('splitEmoji finds shortcodes, keeps adjacent ones separate, leaves the rest as text', () => {
  expect(splitEmoji('Build :white_check_mark: done')).toEqual([
    { text: 'Build ' },
    { emoji: 'white_check_mark', raw: ':white_check_mark:' },
    { text: ' done' },
  ])
  expect(splitEmoji(':a::b:')).toEqual([
    { emoji: 'a', raw: ':a:' },
    { emoji: 'b', raw: ':b:' },
  ])
  expect(splitEmoji('no colons here')).toEqual([{ text: 'no colons here' }])
})

test('isEmojiOnlyText: only shortcodes and whitespace is jumbo-eligible', () => {
  expect(isEmojiOnlyText(':white_check_mark: :tada:')).toBe(true)
  expect(isEmojiOnlyText('  :white_check_mark:  ')).toBe(true)
  expect(isEmojiOnlyText(':white_check_mark: done')).toBe(false)
  expect(isEmojiOnlyText('')).toBe(false)
  expect(isEmojiOnlyText('  ')).toBe(false)
})

test('emoji: a known shortcode and an alias both render as the glyph, inline (not jumbo)', () => {
  const { container } = render(<Markdown text={'Build :white_check_mark: :thumbsup: done'} me="alice" onLink={() => {}} serverId={1} />)
  expect(container.textContent).toBe('Build ✅ 👍 done')
  expect(container.querySelector('.text-3xl')).toBeNull()
})

test('emoji: an unknown name stays literal text, no image is requested', () => {
  const { container } = render(<Markdown text={'hi :not_a_real_emoji: there'} me="alice" onLink={() => {}} serverId={1} />)
  expect(container.textContent).toBe('hi :not_a_real_emoji: there')
  expect(container.querySelector('img')).toBeNull()
})

test('emoji: no conversion inside inline code or a fenced code block', () => {
  const { container } = render(
    <Markdown text={'`:smile:` and\n\n```\n:smile:\n```'} me="alice" onLink={() => {}} serverId={1} />,
  )
  expect(container.querySelector('code')!.textContent).toBe(':smile:')
  expect(container.querySelector('pre code')!.textContent).toBe(':smile:\n')
  expect(container.textContent).not.toContain('😄')
})

test('emoji: a custom emoji known through emojiInfo() renders as an img via /media', async () => {
  const emojiInfo = vi.fn().mockResolvedValue({ recent: [], custom: ['party'], custom_enabled: true } satisfies EmojiDTO)
  const { container } = render(<Markdown text={'go :party: go'} me="alice" onLink={() => {}} serverId={1} emojiInfo={emojiInfo} />)
  await waitFor(() => expect(container.querySelector('img')).not.toBeNull())
  const img = container.querySelector('img')!
  expect(img.getAttribute('src')).toContain('/media/1/emoji/party')
  expect(container.textContent).not.toContain(':party:')
})

test('emoji: adjacent :a::b: both convert', () => {
  const { container } = render(<Markdown text={':white_check_mark::thumbsup:'} me="alice" onLink={() => {}} serverId={1} />)
  expect(container.textContent).toBe('✅👍')
})

test('emoji: a URL-like "http://x:8080:" is not mangled into a shortcode', () => {
  // A real emoji named "8080" that emojiInfo would confirm as custom — if
  // the plugin mistakenly treated the URL's ":8080:" as a shortcode this
  // would render as an <img> and call emojiInfo; it must not, because the
  // whole "http://x:8080" is a link (GFM autolink), whose children the
  // walk never touches — see remarkEmoji.ts and remarkMentions' identical
  // link skip.
  const emojiInfo = vi.fn().mockResolvedValue({ recent: [], custom: ['8080'], custom_enabled: true } satisfies EmojiDTO)
  const { container } = render(<Markdown text={'see http://x:8080: now'} me="alice" onLink={() => {}} serverId={1} emojiInfo={emojiInfo} />)
  expect(container.querySelector('img')).toBeNull()
  expect(emojiInfo).not.toHaveBeenCalled()
  expect(container.textContent).toContain('8080')
})

test('emoji: jumbo — a message made only of emoji renders larger', () => {
  const { container } = render(<Markdown text={':white_check_mark: :tada:'} me="alice" onLink={() => {}} serverId={1} />)
  expect(container.querySelectorAll('.text-3xl')).toHaveLength(2)
})

// --- Fix round 1 (review findings) ---

test('splitEmoji/emojiNames accept uppercase letters in the name, as the webapp and server do', () => {
  expect(splitEmoji(':MyEmoji:')).toEqual([{ emoji: 'MyEmoji', raw: ':MyEmoji:' }])
  expect(emojiNames('go :MyEmoji: and :white_check_mark: go')).toEqual(['MyEmoji', 'white_check_mark'])
})

test('isEmojiOnlyText accepts uppercase names in its shape check too', () => {
  expect(isEmojiOnlyText(':MyEmoji:')).toBe(true)
})

test('emoji: a mixed-case standard shortcode resolves case-insensitively, same lookup as the webapp', () => {
  const { container } = render(<Markdown text={'done :White_Check_Mark:'} me="alice" onLink={() => {}} serverId={1} />)
  expect(container.textContent).toBe('done ✅')
})

test('emoji: an uppercase custom emoji name is tokenized and resolves via emojiInfo (case preserved for the lookup)', async () => {
  const emojiInfo = vi.fn().mockResolvedValue({ recent: [], custom: ['MyEmoji'], custom_enabled: true } satisfies EmojiDTO)
  const { container } = render(<Markdown text={'go :MyEmoji: go'} me="alice" onLink={() => {}} serverId={1} emojiInfo={emojiInfo} />)
  await waitFor(() => expect(container.querySelector('img')).not.toBeNull())
  expect(container.querySelector('img')!.getAttribute('src')).toContain('/media/1/emoji/MyEmoji')
})

test('emoji: jumbo does not fire when the message mixes a resolved and an unresolved shortcode', () => {
  const { container } = render(<Markdown text={':white_check_mark: :notanemoji:'} me="alice" onLink={() => {}} serverId={1} />)
  // The resolved half must not render enlarged just because the raw text's
  // *shape* looked jumbo-eligible — review round 1 #2.
  expect(container.querySelector('.text-3xl')).toBeNull()
  expect(container.textContent).toBe('✅ :notanemoji:')
})

test('emoji: jumbo waits for a custom name to resolve before firing, then applies to the whole message', async () => {
  const emojiInfo = vi.fn().mockResolvedValue({ recent: [], custom: ['party'], custom_enabled: true } satisfies EmojiDTO)
  const { container } = render(<Markdown text={':white_check_mark: :party:'} me="alice" onLink={() => {}} serverId={1} emojiInfo={emojiInfo} />)
  await waitFor(() => expect(container.querySelectorAll('.text-3xl')).toHaveLength(2))
})

test('emoji: a shortcode inside a markdown link\'s label converts; the URL itself is untouched', async () => {
  const onLink = vi.fn()
  render(<Markdown text={'[:smile: click here](https://example.com)'} me="alice" onLink={onLink} serverId={1} />)
  const link = screen.getByRole('link')
  expect(link.textContent).toBe('😄 click here')
  await userEvent.click(link)
  expect(onLink).toHaveBeenCalledWith('https://example.com')
})

// --- inline mode (header-markdown-brief 2026-09-30) ---

test('inline: a link renders clickable, wrapped in a span not a div, no raw brackets', async () => {
  const onLink = vi.fn()
  const { container } = render(
    <Markdown text={'Board: [Sprint](https://example.com/sprint)'} me="alice" onLink={onLink} serverId={1} inline />,
  )
  expect(container.querySelector('div')).toBeNull()
  expect(container.firstElementChild!.tagName).toBe('SPAN')
  expect(container.textContent).not.toContain('[Sprint]')
  expect(container.textContent).not.toContain('(https://example.com/sprint)')
  const link = screen.getByRole('link', { name: 'Sprint' })
  await userEvent.click(link)
  expect(onLink).toHaveBeenCalledWith('https://example.com/sprint')
})

test('inline: no block elements render — paragraphs, a heading and a list all flatten to text', () => {
  const { container } = render(
    <Markdown text={'# Title\n\nfirst para\n\n- one\n- two\n\n> quoted'} me="alice" onLink={() => {}} serverId={1} inline />,
  )
  for (const tag of ['h1', 'p', 'ul', 'li', 'blockquote']) expect(container.querySelector(tag)).toBeNull()
  expect(container.textContent).toContain('Title')
  expect(container.textContent).toContain('first para')
  expect(container.textContent).toContain('one')
  expect(container.textContent).toContain('two')
  expect(container.textContent).toContain('quoted')
})

test('inline: a soft line break becomes a space, not a <br> — stays one visual line', () => {
  const { container } = render(<Markdown text={'line one\nline two'} me="alice" onLink={() => {}} serverId={1} inline />)
  // No <br> element: that's the part that would force a real line break even
  // under the caller's `white-space: nowrap` (a raw "\n" character next to
  // it — mdast-util-to-hast's break handler always emits one alongside the
  // <br>, cosmetic only — is itself collapsible whitespace under nowrap, so
  // it renders as a single space; only the element forces a hard break).
  expect(container.querySelector('br')).toBeNull()
  expect(container.textContent!.replace(/\s+/g, ' ').trim()).toBe('line one line two')
})

test('non-inline mode is unaffected: still a div with a real <p>', () => {
  const { container } = render(<Markdown text={'hello'} me="alice" onLink={() => {}} serverId={1} />)
  expect(container.querySelector('div.md')).not.toBeNull()
  expect(container.querySelector('p')).not.toBeNull()
})

// ChannelMention's ~link (sidebar-sections-brief.md fix round 1): a third
// origin, besides a Sidebar row click and a notification, that switches the
// active channel — clicking it calls chat.ts's real openChannel (not a
// mock), so this proves the held-channel capture is centralized there and
// fires for this origin too, not just the Sidebar-click one fix round 1
// replaced.
test('~channel link: clicking it opens the channel and holds it (fix round 1: capture is centralized in chat.ts, not Sidebar-click-only)', async () => {
  const sb: SidebarDTO = {
    team_id: 't1', selected_channel_id: 'town', teams: [],
    categories: [{
      id: 'c1', type: 'channels', name: 'Channels', collapsed: false,
      channels: [{ id: 'town', name: 'Town Square', type: 'O', slug: 'town-square', unread: true, mentions: 1, muted: false }],
    }],
  }
  useStore.setState({ selectedId: 1, sidebar: sb, channel: null, heldChannel: null })
  const ch: ChannelDTO = {
    id: 'town', name: 'Town Square', type: 'O', header: '', purpose: '', team_id: 't1', team_name: 'team', posts: [],
    new_since: 0, has_more: false, loaded: true, syncing: false, gap_after: '', draft: '', me_id: 'u-alice', crt: false, muted: false,
  }
  vi.mocked(client.openChannel).mockResolvedValue(ch)

  render(<Markdown text="~town-square" me="alice" onLink={() => {}} serverId={1} />)
  const link = screen.getByRole('link', { name: '~Town Square' })
  await userEvent.click(link)

  expect(client.openChannel).toHaveBeenCalledWith(1, 'town')
  await waitFor(() => expect(useStore.getState().heldChannel).toEqual({ id: 'town', hadMentions: true }))
})
