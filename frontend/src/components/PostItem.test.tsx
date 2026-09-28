import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { ApiError } from '../api/client'
import type { EmojiDTO, PostView } from '../api/types'
import { forgetRecent } from '../emoji/recent'
import { setLocale } from '../i18n'
import { PostItem, type PostActions } from './PostItem'

const post = (o: Partial<PostView> = {}): PostView => ({
  id: 'p1', user_id: 'u-bob', author: 'bob', message: 'hello', create_at: new Date(2026, 8, 24, 13, 5).getTime(), ...o,
})
const actions = (): PostActions => ({
  link: vi.fn(), retry: vi.fn(), discard: vi.fn(), edit: vi.fn(), saveEdit: vi.fn().mockResolvedValue(undefined),
  cancelEdit: vi.fn(), remove: vi.fn(), markUnread: vi.fn(), save: vi.fn(), copyLink: vi.fn(),
  view: vi.fn(), download: vi.fn(), open: vi.fn(), react: vi.fn().mockResolvedValue(undefined),
  emojiInfo: vi.fn().mockResolvedValue({ recent: [], custom: [], custom_enabled: false }),
  reactionUsers: vi.fn().mockResolvedValue({ users: [], unknown: 0 }),
})
const me = { id: 'u-alice', username: 'alice' }
const dto = (o: Partial<EmojiDTO> = {}): EmojiDTO => ({ recent: [], custom: [], custom_enabled: false, ...o })

beforeEach(() => setLocale('en'))
// emoji/recent.ts caches per server id across the whole module — every
// test here uses server 1 (or 2), so drop both between tests or an
// earlier test's quick-reaction cache leaks into a later one. cleanup()
// runs first and explicitly (not left to RTL's own automatic afterEach,
// whose registration order relative to this one is not guaranteed):
// forgetRecent's notify() would otherwise re-awaken a not-yet-unmounted
// leftover QuickReactions from the previous test, which then refetches
// with *that* test's mock and can land its result here instead.
afterEach(() => {
  cleanup()
  forgetRecent(1)
  forgetRecent(2)
})

// hover shows the toolbar (PostItem's "hot" state, pointerenter/focusin —
// see the Ruling in AGENTS.md): it is only actually mounted once hot, not
// merely CSS-hidden, so every toolbar-button test needs this first.
const hover = (el: Element) => userEvent.hover(el)

test('head shows author, time and bot badge; follow-up hides them', () => {
  const { rerender } = render(<PostItem serverId={1} post={post({ bot: true, edit_at: 1 })} head me={me} locale="ru-RU" crt={false} actions={actions()} editing={false} />)
  expect(screen.getByText('bob')).toBeInTheDocument()
  expect(screen.getByText('BOT')).toBeInTheDocument()
  expect(screen.getAllByText('13:05')).toHaveLength(1)
  expect(screen.getByText('(edited)')).toBeInTheDocument()
  rerender(<PostItem serverId={1} post={post()} head={false} me={me} locale="ru-RU" crt={false} actions={actions()} editing={false} />)
  expect(screen.queryByText('bob')).toBeNull()
})

test('attachments, files, reactions and reply count', async () => {
  const a = actions()
  render(
    <PostItem
      serverId={1}
      post={post({
        message: '',
        attachments: [{ color: 'danger', pretext: 'Build', title: 'Pipeline #7', title_link: 'https://ci/7', text: 'failed on **test**', fields: [{ title: 'Branch', value: 'main', short: true }] }],
        files: [{ id: 'f1', name: 'report.pdf', size: 2048, mime: 'application/pdf' }],
        reactions: [{ emoji: '+1', count: 2, mine: true }, { emoji: 'custom_party', count: 1, mine: false }],
        reply_count: 3,
      })}
      head
      me={me}
      locale="en-US"
      crt
      actions={a}
      editing={false}
    />,
  )
  expect(screen.getByText('test').tagName).toBe('STRONG')
  expect(screen.getByText('Branch')).toBeInTheDocument()
  expect(screen.getByText('report.pdf')).toBeInTheDocument()
  expect(screen.getByText('2.0 KB')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '👍 2, you reacted' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: ':custom_party: 1' })).toBeInTheDocument()
  expect(screen.getByText('Replies: 3')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('link', { name: 'Pipeline #7' }))
  expect(a.link).toHaveBeenCalledWith('https://ci/7')
  await userEvent.click(screen.getByRole('button', { name: '👍 2, you reacted' }))
  expect(a.react).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }), '+1', false)
})

test('pending and failed posts', async () => {
  const a = actions()
  const { rerender } = render(<PostItem serverId={1} post={post({ pending: true, user_id: 'u-alice' })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  expect(screen.getByText('Sending…')).toBeInTheDocument()
  const failed = post({ failed: true, user_id: 'u-alice' })
  rerender(<PostItem serverId={1} post={failed} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(a.retry).toHaveBeenCalledWith(failed)
  await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
  expect(a.discard).toHaveBeenCalledWith(failed)
})

test('a pending post with files offers Cancel while any upload is not done, and hides it once all are', async () => {
  const a = actions()
  const waiting = post({
    pending: true, user_id: 'u-alice',
    files: [{ id: 'a1', name: 'x.png', size: 3, mime: 'image/png', staged: true, state: 'staged' }],
  })
  const { rerender } = render(<PostItem serverId={1} post={waiting} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await userEvent.click(screen.getByRole('button', { name: 'Cancel sending' }))
  expect(a.discard).toHaveBeenCalledWith(waiting)

  const done = post({
    pending: true, user_id: 'u-alice',
    files: [{ id: 'a1', name: 'x.png', size: 3, mime: 'image/png', staged: true, state: 'uploaded', sent: 3 }],
  })
  rerender(<PostItem serverId={1} post={done} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  expect(screen.queryByRole('button', { name: 'Cancel sending' })).toBeNull()
})

test('a pending post with no files offers no Cancel button', () => {
  render(<PostItem serverId={1} post={post({ pending: true, user_id: 'u-alice' })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  expect(screen.queryByRole('button', { name: 'Cancel sending' })).toBeNull()
})

test('own post: the "…" menu offers edit and delete; others only mark unread and copy link', async () => {
  const a = actions()
  const own = post({ user_id: 'u-alice' })
  const { container, rerender } = render(<PostItem serverId={1} post={own} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  expect(screen.queryByTestId('post-toolbar')).toBeNull() // not hovered/focused yet: not even mounted
  await hover(container.querySelector('[data-post-id]')!)
  await userEvent.click(await screen.findByRole('button', { name: 'More actions' }))
  const menu = await screen.findByRole('menu')
  expect(within(menu).getAllByRole('menuitem').map((b) => b.textContent)).toEqual(['Mark as unread', 'Copy link', 'Edit', 'Delete'])
  await userEvent.click(within(menu).getByRole('menuitem', { name: 'Edit' }))
  expect(a.edit).toHaveBeenCalledWith(own)
  expect(screen.queryByRole('menu')).toBeNull() // picking an item closes the menu

  await userEvent.click(screen.getByRole('button', { name: 'More actions' }))
  await userEvent.click(screen.getByRole('menuitem', { name: 'Delete' }))
  expect(a.remove).toHaveBeenCalledWith(own)

  const other = post()
  rerender(<PostItem serverId={1} post={other} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await userEvent.click(screen.getByRole('button', { name: 'More actions' }))
  const menu2 = await screen.findByRole('menu')
  expect(within(menu2).queryByRole('menuitem', { name: 'Edit' })).toBeNull()
  expect(within(menu2).queryByRole('menuitem', { name: 'Delete' })).toBeNull()
  await userEvent.click(within(menu2).getByRole('menuitem', { name: 'Mark as unread' }))
  expect(a.markUnread).toHaveBeenCalledWith(other)
  await userEvent.click(screen.getByRole('button', { name: 'More actions' }))
  await userEvent.click(screen.getByRole('menuitem', { name: 'Copy link' }))
  expect(a.copyLink).toHaveBeenCalledWith(other)
})

test('the toolbar is not rendered before hover/focus; it stays visible while the "…" menu is open after the pointer leaves', async () => {
  const a = actions()
  const { container } = render(<PostItem serverId={1} post={post({ user_id: 'u-alice' })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  const article = container.querySelector('[data-post-id]')!
  expect(screen.queryByTestId('post-toolbar')).toBeNull()
  await hover(article)
  expect(await screen.findByTestId('post-toolbar')).toBeInTheDocument()
  await userEvent.unhover(article)
  expect(screen.queryByTestId('post-toolbar')).toBeNull() // no menu/picker open: hides again

  await hover(article)
  await userEvent.click(screen.getByRole('button', { name: 'More actions' }))
  await screen.findByRole('menu')
  await userEvent.unhover(article)
  expect(screen.getByTestId('post-toolbar')).toBeInTheDocument() // menu still open: toolbar stays
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('menu')).toBeNull()
})

// fix round 1 (controller review of e63449f): closing the "…" menu after
// the pointer already left the post used to lose focus entirely — the
// toolbar (and its "…" button) unmounted in the same render that closed
// the menu, before an unmount-time cleanup could ever focus it.
test('Esc after the pointer left while the "…" menu was open restores focus to "…", and the toolbar stays', async () => {
  const a = actions()
  const { container } = render(<PostItem serverId={1} post={post({ user_id: 'u-alice' })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  const article = container.querySelector('[data-post-id]')!
  await hover(article)
  await userEvent.click(await screen.findByRole('button', { name: 'More actions' }))
  await screen.findByRole('menu')
  await userEvent.unhover(article)
  expect(screen.getByTestId('post-toolbar')).toBeInTheDocument() // menu still open keeps it visible
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('menu')).toBeNull()
  expect(screen.getByRole('button', { name: 'More actions' })).toHaveFocus()
  expect(screen.getByTestId('post-toolbar')).toBeInTheDocument() // stays: focus is inside it now
})

// Same finding, the item-click path: picking a menu item after the
// pointer left must also land focus back on "…", not nowhere.
test('picking a menu item after the pointer left restores focus to "…"', async () => {
  const a = actions()
  const other = post()
  const { container } = render(<PostItem serverId={1} post={other} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  const article = container.querySelector('[data-post-id]')!
  await hover(article)
  await userEvent.click(await screen.findByRole('button', { name: 'More actions' }))
  await screen.findByRole('menu')
  await userEvent.unhover(article)
  await userEvent.click(screen.getByRole('menuitem', { name: 'Copy link' }))
  expect(a.copyLink).toHaveBeenCalledWith(other)
  expect(screen.queryByRole('menu')).toBeNull()
  expect(screen.getByRole('button', { name: 'More actions' })).toHaveFocus()
})

// A click outside while the pointer had already left must NOT steal focus
// back — it's an "external" close (the user's own click already moved
// their attention), unlike Esc/an item pick above.
test('a click outside while the pointer had already left closes the menu without stealing focus, and the toolbar disappears', async () => {
  const a = actions()
  render(
    <>
      <button>elsewhere</button>
      <PostItem serverId={1} post={post({ user_id: 'u-alice' })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />
    </>,
  )
  const article = screen.getByText('hello').closest('[data-post-id]')!
  await hover(article)
  await userEvent.click(await screen.findByRole('button', { name: 'More actions' }))
  await screen.findByRole('menu')
  await userEvent.unhover(article)
  const outside = screen.getByRole('button', { name: 'elsewhere' })
  await userEvent.click(outside)
  expect(screen.queryByRole('menu')).toBeNull()
  expect(screen.queryByTestId('post-toolbar')).toBeNull() // nothing keeps it hot any more
  expect(document.activeElement).toBe(outside) // the user's own click, not stolen back
})

// The same fix applies to the emoji picker's own trigger (openPicker/
// closePicker), the reviewer's suspicion that "the same focus issue
// probably applies" — verified here rather than assumed.
test('closing the emoji picker after the pointer left restores focus to "Add reaction", and the toolbar stays', async () => {
  const a = actions()
  a.emojiInfo = vi.fn().mockResolvedValue(dto())
  const { container } = render(<PostItem serverId={1} post={post({ user_id: 'u-bob' })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  const article = container.querySelector('[data-post-id]')!
  await hover(article)
  await userEvent.click(await screen.findByRole('button', { name: 'Add reaction' }))
  await screen.findByRole('dialog', { name: 'Emoji picker' })
  await userEvent.unhover(article)
  expect(screen.getByTestId('post-toolbar')).toBeInTheDocument() // picker still open keeps it visible
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(screen.getByRole('button', { name: 'Add reaction' })).toHaveFocus()
  expect(screen.getByTestId('post-toolbar')).toBeInTheDocument()
})

test('focusing an element inside the post (not just hovering it) also shows the toolbar', async () => {
  const a = actions()
  render(<PostItem serverId={1} post={post({ reactions: [{ emoji: '+1', count: 1, mine: false }] })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  expect(screen.queryByTestId('post-toolbar')).toBeNull()
  screen.getByRole('button', { name: '👍 1' }).focus() // the reaction chip, not a toolbar button
  expect(await screen.findByTestId('post-toolbar')).toBeInTheDocument()
})

test('quick reactions: the first 3 known names appear once emojiInfo resolves; clicking toggles the reaction', async () => {
  const a = actions()
  a.emojiInfo = vi.fn().mockResolvedValue(dto({ recent: ['tada', 'unknown_custom', 'fire', 'rocket'], custom: [] }))
  const { container } = render(<PostItem serverId={1} post={post({ user_id: 'u-bob' })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await hover(container.querySelector('[data-post-id]')!)
  const toolbar = await screen.findByTestId('post-toolbar')
  await waitFor(() => expect(within(toolbar).getAllByRole('button', { name: /^React with/ })).toHaveLength(3))
  const names = within(toolbar).getAllByRole('button', { name: /^React with/ }).map((b) => b.getAttribute('aria-label'))
  expect(names).toEqual(['React with :tada:', 'React with :fire:', 'React with :rocket:'])
  await userEvent.click(within(toolbar).getByRole('button', { name: 'React with :tada:' }))
  expect(a.react).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }), 'tada', true)
})

test('quick reactions: clicking a name we already reacted with removes it instead', async () => {
  const a = actions()
  a.emojiInfo = vi.fn().mockResolvedValue(dto({ recent: ['tada'] }))
  const { container } = render(<PostItem serverId={1} post={post({ reactions: [{ emoji: 'tada', count: 1, mine: true }] })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await hover(container.querySelector('[data-post-id]')!)
  const toolbar = await screen.findByTestId('post-toolbar')
  const quick = await within(toolbar).findByRole('button', { name: 'React with :tada:' })
  await userEvent.click(quick)
  expect(a.react).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }), 'tada', false)
})

test('hovering several posts on the same server calls emojiInfo only once', async () => {
  const a = actions()
  a.emojiInfo = vi.fn().mockResolvedValue(dto({ recent: ['tada'] }))
  const { container } = render(
    <>
      <PostItem serverId={1} post={post({ id: 'p1' })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />
      <PostItem serverId={1} post={post({ id: 'p2' })} head={false} me={me} locale="en-US" crt={false} actions={a} editing={false} />
    </>,
  )
  const articles = container.querySelectorAll('[data-post-id]')
  await hover(articles[0])
  await waitFor(() => expect(within(screen.getByTestId('post-toolbar')).queryAllByRole('button', { name: /^React with/ })).toHaveLength(1))
  await userEvent.unhover(articles[0])
  await hover(articles[1])
  await waitFor(() => expect(within(screen.getByTestId('post-toolbar')).queryAllByRole('button', { name: /^React with/ })).toHaveLength(1))
  expect(a.emojiInfo).toHaveBeenCalledTimes(1)
})

// Final-review finding (UI pass 2026-09-28), the exact repro: hover, add an
// emoji via the picker, hover another post — the panel used to show 0 (or
// the *previous* add's) quick reactions, because invalidateRecent fired
// synchronously, racing ahead of the backend actually handling the click.
// react() now waits for actions.react's promise to settle first.
test('after adding a reaction via the picker, an already-shown toolbar picks it up once actions.react settles — not before, not stuck on the old list', async () => {
  const a = actions()
  let resolveReact: () => void = () => {}
  a.react = vi.fn(() => new Promise<void>((resolve) => { resolveReact = resolve }))
  a.emojiInfo = vi.fn()
    .mockResolvedValueOnce(dto({ recent: [] })) // post1's quick reactions, first show
    .mockResolvedValueOnce(dto({ recent: [] })) // the picker's own (separate) fetch
    .mockResolvedValueOnce(dto({ recent: ['avocado'] })) // post2's refetch once the add lands
  const { container } = render(
    <>
      <PostItem serverId={1} post={post({ id: 'p1' })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />
      <PostItem serverId={1} post={post({ id: 'p2' })} head={false} me={me} locale="en-US" crt={false} actions={a} editing={false} />
    </>,
  )
  const articles = container.querySelectorAll('[data-post-id]')
  await hover(articles[0])
  await screen.findByTestId('post-toolbar')
  await waitFor(() => expect(a.emojiInfo).toHaveBeenCalledTimes(1)) // empty recent: no quick reactions yet

  await userEvent.click(screen.getByRole('button', { name: 'Add reaction' }))
  const dialog = await screen.findByRole('dialog', { name: 'Emoji picker' })
  await waitFor(() => expect(a.emojiInfo).toHaveBeenCalledTimes(2)) // +1: the picker's own (separate) info fetch
  await userEvent.type(within(dialog).getByRole('textbox', { name: 'Search emoji' }), 'avocado')
  await userEvent.keyboard('{Enter}')
  expect(a.react).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }), 'avocado', true)

  await userEvent.unhover(articles[0])
  await hover(articles[1])
  const toolbar2 = await screen.findByTestId('post-toolbar')
  await within(toolbar2).findByRole('button', { name: 'Add reaction' })
  expect(a.emojiInfo).toHaveBeenCalledTimes(2) // actions.react has not settled yet: the cache is still fresh, no refetch

  resolveReact()
  await waitFor(() => expect(a.emojiInfo).toHaveBeenCalledTimes(3))
  await waitFor(() => expect(within(toolbar2).getByRole('button', { name: 'React with :avocado:' })).toBeInTheDocument())
})

test('inline edit: Enter saves, Escape cancels, errors stay visible', async () => {
  const a = actions()
  const own = post({ user_id: 'u-alice', message: 'v1' })
  const { rerender } = render(<PostItem serverId={1} post={own} head me={me} locale="en-US" crt={false} actions={a} editing />)
  const box = screen.getByRole('textbox', { name: 'Edit message' })
  expect(box).toHaveValue('v1')
  expect(screen.queryByRole('toolbar')).toBeNull()
  await userEvent.type(box, ' v2{Enter}')
  expect(a.saveEdit).toHaveBeenCalledWith(own, 'v1 v2')
  await userEvent.type(box, '{Escape}')
  expect(a.cancelEdit).toHaveBeenCalled()
  a.saveEdit = vi.fn().mockRejectedValue(new ApiError('forbidden', ''))
  rerender(<PostItem serverId={1} post={own} head me={me} locale="en-US" crt={false} actions={{ ...a }} editing />)
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('You are not allowed to do that')
})

test('the reaction button opens the picker; picking reacts, one already ours is not sent again', async () => {
  const a = actions()
  a.emojiInfo = vi.fn().mockResolvedValue({ recent: ['tada'], custom: [], custom_enabled: false })
  render(<PostItem serverId={1} post={post({ reactions: [{ emoji: 'tada', count: 1, mine: true }] })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await userEvent.click(screen.getAllByRole('button', { name: 'Add reaction' })[0])
  const dialog = await screen.findByRole('dialog', { name: 'Emoji picker' })
  expect(a.emojiInfo).toHaveBeenCalled()
  await userEvent.click(await within(dialog).findByRole('button', { name: ':rocket:' }))
  expect(a.react).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }), 'rocket', true)
  expect(screen.queryByRole('dialog')).toBeNull()

  await userEvent.click(screen.getAllByRole('button', { name: 'Add reaction' })[0])
  const again = await screen.findByRole('dialog', { name: 'Emoji picker' })
  const recent = await within(again).findByRole('region', { name: 'Recently used' })
  await userEvent.click(within(recent).getByRole('button', { name: ':tada:' }))
  expect(a.react).toHaveBeenCalledTimes(1)
})

test('system and pending posts offer no reaction button', () => {
  const { rerender } = render(<PostItem serverId={1} post={post({ system: true })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  expect(screen.queryByRole('button', { name: 'Add reaction' })).toBeNull()
  rerender(<PostItem serverId={1} post={post({ pending: true })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  expect(screen.queryByRole('button', { name: 'Add reaction' })).toBeNull()
})

// Review Focus #4: a pending, failed or system post gets no quick
// reactions (and no "save" once Task 3 adds it) even when hovered — and,
// unlike an ordinary post, hovering it never calls emojiInfo at all.
test('a system post shows a toolbar (mark unread/copy link still apply) but no quick reactions, and never calls emojiInfo', async () => {
  const a = actions()
  a.emojiInfo = vi.fn().mockResolvedValue(dto({ recent: ['tada'] }))
  const { container } = render(<PostItem serverId={1} post={post({ system: true })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await hover(container.querySelector('[data-post-id]')!)
  const toolbar = await screen.findByTestId('post-toolbar')
  expect(within(toolbar).queryAllByRole('button', { name: /^React with/ })).toHaveLength(0)
  expect(within(toolbar).queryByRole('button', { name: 'Add reaction' })).toBeNull()
  expect(within(toolbar).getByRole('button', { name: 'More actions' })).toBeInTheDocument()
  await new Promise((r) => setTimeout(r, 0))
  expect(a.emojiInfo).not.toHaveBeenCalled()
})

test('a pending or failed post shows no toolbar at all, hovered or not', async () => {
  const a = actions()
  const { container, rerender } = render(<PostItem serverId={1} post={post({ pending: true })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await hover(container.querySelector('[data-post-id]')!)
  expect(screen.queryByTestId('post-toolbar')).toBeNull()
  rerender(<PostItem serverId={1} post={post({ failed: true })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await hover(container.querySelector('[data-post-id]')!)
  expect(screen.queryByTestId('post-toolbar')).toBeNull()
})

test('head shows the author picture with presence; an unknown author gets initials', () => {
  const { container, rerender } = render(<PostItem serverId={2} post={post({ avatar: '9', status: 'dnd' })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  expect(container.querySelector('img')).toHaveAttribute('src', '/media/2/avatar/u-bob?v=9')
  expect(container.querySelector('[data-status="dnd"]')).toHaveAttribute('title', 'Do not disturb')
  rerender(<PostItem serverId={2} post={post({ author: '' })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  expect(container.querySelector('img')).toBeNull()
  expect(container).toHaveTextContent('?')
})

// fix round 1 (RULING #3): the reply slot is a real prop the threads task
// will consume, not just an inline `null` placeholder.
test('replyButton renders in the toolbar between "add reaction" and "…"; absent by default', async () => {
  const a = actions()
  const { container, rerender } = render(<PostItem serverId={1} post={post()} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await hover(container.querySelector('[data-post-id]')!)
  const toolbar = await screen.findByTestId('post-toolbar')
  expect(within(toolbar).queryByRole('button', { name: 'Reply' })).toBeNull()

  rerender(
    <PostItem serverId={1} post={post()} head me={me} locale="en-US" crt={false} actions={a} editing={false} replyButton={<button aria-label="Reply">Reply</button>} />,
  )
  const names = within(toolbar).getAllByRole('button').map((b) => b.getAttribute('aria-label'))
  expect(names).toEqual(['Add reaction', 'Save', 'Reply', 'More actions'])
})

// Task 3 brief: "сохранить" sits after "добавить реакцию" and before the
// reply slot; aria-pressed/aria-label track post.saved (state comes only
// from the prop — no optimistic local flip), and the icon swaps
// IconBookmark/IconBookmarkFilled with a text-accent class once saved.
test('save button: one fixed label, aria-pressed tracks post.saved, and the click it sends', async () => {
  const a = actions()
  const { container, rerender } = render(<PostItem serverId={1} post={post({ saved: false })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  await hover(container.querySelector('[data-post-id]')!)
  const toolbar = await screen.findByTestId('post-toolbar')
  const btn = within(toolbar).getByRole('button', { name: 'Save' })
  expect(btn).toHaveAttribute('aria-pressed', 'false')
  expect(btn.className).toContain('text-fg-muted')
  await userEvent.click(btn)
  expect(a.save).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }), true)

  rerender(<PostItem serverId={1} post={post({ saved: true })} head me={me} locale="en-US" crt={false} actions={a} editing={false} />)
  const savedBtn = within(toolbar).getByRole('button', { name: 'Save' }) // same label — no swap to "Remove from saved" (final-review RULING #4)
  expect(savedBtn).toBe(btn) // literally the same element: no label swap remounted it
  expect(savedBtn).toHaveAttribute('aria-pressed', 'true')
  expect(savedBtn.className).toContain('text-accent')
  await userEvent.click(savedBtn)
  expect(a.save).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' }), false)
})

// Review Focus #4: pending/failed/system posts get no "save" either, same
// gating as quick reactions and "add reaction" (canReact).
test('pending, failed and system posts offer no save button', async () => {
  const { container, rerender } = render(<PostItem serverId={1} post={post({ system: true })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  await hover(container.querySelector('[data-post-id]')!)
  await screen.findByTestId('post-toolbar')
  expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()

  rerender(<PostItem serverId={1} post={post({ pending: true })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  expect(screen.queryByTestId('post-toolbar')).toBeNull()
  expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()

  rerender(<PostItem serverId={1} post={post({ failed: true })} head me={me} locale="en-US" crt={false} actions={actions()} editing={false} />)
  expect(screen.queryByTestId('post-toolbar')).toBeNull()
  expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()
})
