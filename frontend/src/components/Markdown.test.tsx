import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { Markdown } from './Markdown'
import { splitMentions } from './remarkMentions'

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
  const { container } = render(<Markdown text={'@alice @bob @channel `@alice`'} me="alice" onLink={() => {}} />)
  const spans = [...container.querySelectorAll('[data-mention]')]
  expect(spans.map((s) => s.getAttribute('data-mention'))).toEqual(['alice', 'bob', 'channel'])
  expect(spans[0]).toHaveClass('bg-amber-100')
  expect(spans[1]).not.toHaveClass('bg-amber-100')
  expect(spans[2]).toHaveClass('bg-amber-100')
  expect(container.querySelector('code')!.textContent).toBe('@alice')
})
