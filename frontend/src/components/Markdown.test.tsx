import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { EmojiDTO } from '../api/types'
import { forgetRecent } from '../emoji/recent'
import { Markdown } from './Markdown'
import { isEmojiOnlyText, splitEmoji } from './remarkEmoji'
import { splitMentions } from './remarkMentions'

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
