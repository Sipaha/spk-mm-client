import { expect, test, type Locator, type Page } from '@playwright/test'
import { screenshotDir, apiCall, channel, expectSent, feed, removeServerFromMenu, serverId, signInAlice, testGet, testPost, unique } from './helpers'

// Keep verification artifacts in the current runner's scratch directory.
const SIDEBAR_SHOTS = screenshotDir

// A failed test must not leave its server behind: every later test signs in
// from the empty start screen (same rule as feed-scroll.spec.ts).
test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
})

test('sidebar, feed and sending once', async ({ page }) => {
  await signInAlice(page)
  // the DM row is now "avatar + bob, <status>"; the group "👥 alice, bob, carol" does not start with "bob"
  for (const name of [/Town Square/, /Off-Topic/, /Secret/, /^bob(,|$)/]) await expect(channel(page, name)).toBeVisible()
  await expect(feed(page).getByText('Message #150', { exact: true })).toBeVisible()

  const text = unique('hello e2e')
  await page.getByRole('textbox', { name: 'Message' }).fill(text)
  await page.keyboard.press('Enter')
  await expect(feed(page).getByText(text)).toBeVisible()
  await expectSent(feed(page).locator('article', { hasText: text }))
  await expect(feed(page).getByText(text)).toHaveCount(1) // REST reply and WS echo → one post
  await removeServerFromMenu(page)
})

test('jump-to-latest button: appears scrolling up, badges a post from someone else, click returns to it', async ({ page }) => {
  await signInAlice(page)
  const log = feed(page)
  await expect(log.getByText('Message #150', { exact: true })).toBeVisible()
  const button = page.getByRole('button', { name: 'Jump to latest messages' })
  await expect(button).toBeHidden()

  // Town Square has 150 seeded posts and a 60-post window: scrolling to the
  // top is well over a viewport away from the bottom. Retried: right after
  // sign-in the feed still gets updates, and one committed before the
  // (asynchronous) scroll event reaches onScroll follows the bottom — the
  // scroll is undone before the feed notices it (Task 7 flake analysis;
  // docs/backlog.md, «Треды» → «Лента: ранний скролл…»).
  await expect(async () => {
    await log.evaluate((el) => el.scrollTo({ top: 0 }))
    await expect(button).toBeVisible({ timeout: 1000 })
  }).toPass({ timeout: 15_000 })

  const text = unique('while scrolled away')
  await testPost(page, 'fake/post', { channel_id: 'c-town', username: 'bob', message: text })
  const badged = page.getByRole('button', { name: 'Jump to latest messages — 1 new' })
  await expect(badged).toBeVisible()
  await expect(badged.getByText('1', { exact: true })).toBeVisible()

  await badged.click()
  await expect(log.getByText(text, { exact: true })).toBeInViewport()
  await expect(page.getByRole('button', { name: /Jump to latest messages/ })).toBeHidden()
  await removeServerFromMenu(page)
})

test('a mention from someone else: badges, notification, reading clears it', async ({ page }) => {
  await signInAlice(page)
  const id = await serverId(page)
  const text = unique('@alice look')
  await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: text })
  // The server-level aggregate badge used to live on the ServerRail tile,
  // but the rail is hidden with only this one fake server configured
  // (sidebar-menu brief addendum 2026-09-29) — ListServers is what's left to
  // check it against (the desktop tray badge is the other place, Go-only).
  const mentions = async () => ((await apiCall(page, 'ListServers')) as { id: number; mentions: number }[]).find((s) => s.id === id)?.mentions
  await expect.poll(mentions).toBe(1)
  await expect(channel(page, /Off-Topic/).getByLabel('Mentions: 1')).toBeVisible()
  await expect
    .poll(async () => ((await testGet(page, 'notifications')) as { body: string }[]).map((n) => n.body))
    .toContain(`bob: ${text}`)
  const notes = (await testGet(page, 'notifications')) as { title: string; server_id: number; channel_id: string; body: string }[]
  expect(notes.find((n) => n.body === `bob: ${text}`)).toMatchObject({ title: 'Off-Topic', server_id: id, channel_id: 'c-offtopic' })

  await channel(page, /Off-Topic/).click()
  await expect(feed(page).getByText(text)).toBeVisible()
  await expect.poll(mentions).toBe(0)
  await expect(channel(page, /Off-Topic/).getByLabel('Mentions: 1')).toHaveCount(0)
  await removeServerFromMenu(page)
})

test('notification click opens its channel', async ({ page }) => {
  await signInAlice(page)
  await testPost(page, 'notification-click', { server_id: await serverId(page), channel_id: 'c-dm-bob' })
  await expect(page.getByRole('heading', { name: /bob/ })).toBeVisible()
  await expect(feed(page).getByText('Hi Alice, this is Bob')).toBeVisible()
  await removeServerFromMenu(page)
})

test('edit with arrow-up, delete with confirmation', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  // Switching channels round-trips through OpenChannel (async, fire-and-forget from the
  // click handler); without waiting for the pane to actually land on Off-Topic, typing
  // straight into "the Message textbox" can still hit Town Square's composer and send there.
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
  const box = page.getByRole('textbox', { name: 'Message' })
  const text = unique('to edit')
  await box.fill(text)
  await page.keyboard.press('Enter')
  await expect(feed(page).getByText(text)).toBeVisible()
  await expectSent(feed(page).locator('article', { hasText: text }))

  await box.press('ArrowUp')
  const edit = page.getByRole('textbox', { name: 'Edit message' })
  await edit.fill(`${text} v2`)
  await edit.press('Enter')
  await expect(feed(page).getByText(`${text} v2`)).toBeVisible()
  await expect(feed(page).locator('article', { hasText: `${text} v2` }).getByText('(edited)')).toBeVisible()

  // Edit and Delete moved into the "…" menu (Task 2, UI pass 2026-09-28).
  const post = feed(page).locator('article', { hasText: `${text} v2` })
  await post.hover()
  await post.getByRole('button', { name: 'More actions' }).click()
  page.once('dialog', (d) => d.accept())
  await page.getByRole('menuitem', { name: 'Delete' }).click()
  await expect(feed(page).getByText(`${text} v2`)).toHaveCount(0)
  await removeServerFromMenu(page)
})

test('editing a multiline message opens an editor fitted to its content and grows with more text', async ({ page }) => {
  await signInAlice(page)
  const composer = page.getByRole('textbox', { name: 'Message' })
  const prefix = unique('editable')
  const lines = Array.from({ length: 7 }, (_, i) => `${prefix} line ${i + 1}`)
  await composer.fill(lines.join('\n'))
  await composer.press('Enter')
  await expect(feed(page).getByText(lines[0], { exact: false })).toBeVisible()
  // ArrowUp intentionally skips optimistic pending posts. Confirm this post
  // first, otherwise the shortcut can edit an earlier one-line own message.
  await expectSent(feed(page).locator('article', { hasText: lines[0] }))
  await composer.press('ArrowUp')

  const edit = page.getByRole('textbox', { name: 'Edit message' })
  await expect(edit).toHaveValue(lines.join('\n'))
  const initial = await edit.evaluate((el) => ({ client: el.clientHeight, scroll: el.scrollHeight }))
  expect(initial.client).toBeGreaterThan(100)
  // clientHeight excludes the 1px border on each side; scrollHeight does not.
  expect(initial.client).toBeGreaterThanOrEqual(initial.scroll - 3)

  await edit.fill([...lines, 'one more line', 'and another'].join('\n'))
  const grown = await edit.evaluate((el) => ({ client: el.clientHeight, scroll: el.scrollHeight }))
  expect(grown.client).toBeGreaterThan(initial.client)
  expect(grown.client).toBeGreaterThanOrEqual(grown.scroll - 3)
  await edit.press('Escape')
})

// latestPost: a fresh post from bob, the feed's last row. The hover tests
// below used a seed post («Message #140») whose place on screen depends on
// how much every earlier test posted into Town Square (the fake is shared by
// the whole run). In some runs it sat half above the feed's top edge: the
// hover scrolled it into view by less than NEAR_BOTTOM, the feed stayed "at
// its bottom", the next update right after sign-in (read state, sidebar)
// pinned it back down, the post slid from under the pointer and its toolbar
// — rendered only while hovered — was gone for good (the click waited 30 s
// for a button nothing would show again). The last row is fully on screen
// at the bottom: hovering it scrolls nothing, and a re-pin moves nothing.
async function latestPost(page: Page) {
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  const text = unique('hover target')
  await testPost(page, 'fake/post', { channel_id: 'c-town', username: 'bob', message: text })
  const post = feed(page).locator('article', { hasText: text })
  await expect(post).toBeInViewport({ ratio: 1 })
  return { post, text }
}

test('mark as unread keeps the channel unread while it is open', async ({ page }) => {
  await signInAlice(page)
  const unread = async () => ((await apiCall(page, 'ListServers')) as { unread: boolean }[]).at(-1)?.unread
  const { post } = await latestPost(page)
  await expect.poll(unread).toBe(false) // baseline: bob's post is read in the open channel, so the dot below comes from this action
  await post.hover()
  await post.getByRole('button', { name: 'More actions' }).click()
  await page.getByRole('menuitem', { name: 'Mark as unread' }).click()
  // The server-level dot used to live on the ServerRail tile, hidden with
  // only this one fake server configured (sidebar-menu brief addendum
  // 2026-09-29) — ListServers (the `unread` helper above) is the check now.
  await expect.poll(unread).toBe(true)
  await page.waitForTimeout(1000) // the open, focused channel must NOT auto-read it again
  expect(await unread()).toBe(true)
  await removeServerFromMenu(page)
})

// The Save button keeps one fixed label ("Save") whether saved or not —
// toggle state is aria-pressed only, no label swap (final-review RULING
// #4, UI pass 2026-09-28).
test('save a post for later: hover, Save, reload keeps it, then unsave', async ({ page }) => {
  await signInAlice(page)
  const { post, text } = await latestPost(page)
  await post.hover()
  await post.getByRole('button', { name: 'Save' }).click()
  await expect(post.getByRole('button', { name: 'Save' })).toHaveAttribute('aria-pressed', 'true')

  await page.reload()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  const postAfterReload = feed(page).locator('article', { hasText: text })
  await expect(postAfterReload).toBeInViewport({ ratio: 1 })
  await postAfterReload.hover()
  await expect(postAfterReload.getByRole('button', { name: 'Save' })).toHaveAttribute('aria-pressed', 'true')

  await postAfterReload.getByRole('button', { name: 'Save' }).click()
  await expect(postAfterReload.getByRole('button', { name: 'Save' })).toHaveAttribute('aria-pressed', 'false')
  await removeServerFromMenu(page)
})

test('history loads up to the first message', async ({ page }) => {
  await signInAlice(page)
  // Scroll up only once the feed has settled at its end, as a user would: a
  // scroll to the top before the first rows are laid out loads a page with
  // no anchor and the feed stays at scrollTop 0 (docs/backlog.md). Later
  // scrollTo(0) calls fire no scroll event there; the feed asks for the next
  // page on its own after each one lands (Feed.tsx, fillViewportIfShort —
  // this test's old flake, Task 7).
  await expect(feed(page).getByText('Message #150', { exact: true })).toBeVisible()
  await expect
    .poll(
      async () => {
        await feed(page).evaluate((el) => el.scrollTo({ top: 0 }))
        return feed(page).getByText('Message #1', { exact: true }).count()
      },
      { timeout: 20_000 },
    )
    .toBeGreaterThan(0)
  await removeServerFromMenu(page)
})

test('expired session: cached chat stays readable, sign in again restores it', async ({ page }) => {
  await signInAlice(page)
  await testPost(page, 'fake/revoke')
  await expect(page.getByText('Session expired — showing saved messages.')).toBeVisible({ timeout: 15_000 })
  await expect(feed(page).getByText('Message #150', { exact: true })).toBeVisible()
  await page.getByRole('status').getByRole('button', { name: 'Sign in again' }).first().click()
  await page.getByLabel('Login or email').fill('alice')
  await page.getByLabel('Password').fill('secret')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await expect(page.getByText('Session expired — showing saved messages.')).toHaveCount(0)
  expect(((await apiCall(page, 'ListServers')) as { state: string }[]).at(-1)?.state).not.toBe('needs_reauth')
  await removeServerFromMenu(page)
})

// Sidebar-menu brief (2026-09-29): the "⋯" menu had no outside-click
// handling at all and stayed open while the user clicked elsewhere.
test('server menu closes when clicking elsewhere in the app', async ({ page }) => {
  await signInAlice(page)
  await page.getByRole('button', { name: 'Server menu' }).click()
  await expect(page.getByRole('menuitem', { name: 'Sign out' })).toBeVisible()
  await page.screenshot({ path: `${SIDEBAR_SHOTS}/menu-open.png` }) // behavioural fix; not asserted, just a visual check nothing moved
  await feed(page).click()
  await expect(page.getByRole('menuitem', { name: 'Sign out' })).toHaveCount(0)
  await removeServerFromMenu(page)
})

// Sidebar-menu brief addendum (2026-09-29): with exactly one server, the
// rail is hidden and "Add server" moves into the "⋯" menu so the rail's own
// "+" tile stays reachable.
test('a single server hides the rail; "⋯" → Add server reaches the add-server form', async ({ page }) => {
  await signInAlice(page)
  await expect(page.getByRole('button', { name: 'Fake MM' })).toHaveCount(0) // no rail tile — nothing to pick between with one server
  await page.screenshot({ path: `${SIDEBAR_SHOTS}/single-server-layout.png` })

  await page.getByRole('button', { name: 'Server menu' }).click()
  const items = await page.getByRole('menuitem').allTextContents()
  expect(items).toEqual(['Add server', 'Sign out', 'Remove server'])
  await page.screenshot({ path: `${SIDEBAR_SHOTS}/menu-open-single-server.png` })
  await page.getByRole('menuitem', { name: 'Add server' }).click()
  await expect(page.getByRole('heading', { name: 'Add a Mattermost server' })).toBeVisible()

  // No dead end: deselecting the one server for the add-server screen brings
  // the rail back (App.tsx's showRail — the add screen has no menu of its
  // own to reach the rail's "+"/other servers from), so the original server
  // is still reachable to back out without adding a second one.
  await expect(page.getByRole('button', { name: 'Fake MM' })).toBeVisible()
  await page.getByRole('button', { name: 'Fake MM' }).click()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await removeServerFromMenu(page)
})

// Density-brief addendum + "smart placement" update (2026-09-29): the hover
// toolbar sits inside the hovered post's own box by default; it only
// overhangs above (the pre-addendum look) when staying inside would cover
// the post's own rendered content. Both outcomes assert the toolbar's
// bounding box is where expected relative to the article's, not just that
// it renders (a class-only assertion wouldn't catch a real geometry bug).
const DENSITY_SHOTS = screenshotDir

test('post toolbar: a short post keeps it inside the post; a long wrapping post or an attachment card overhangs above', async ({ page }) => {
  await signInAlice(page)
  const short = unique('short toolbar test post')
  // Located by their unique parts: the same test run twice against one fake
  // (--repeat-each) would otherwise find the previous run's copies too.
  const longHead = unique('a very long wrapping toolbar test message')
  const attachmentTitle = unique('toolbar-test-attachment')
  const long = longHead +
    ' padded out with enough extra words that it wraps across more than one line and its own last line of text reaches under the top-right corner where the hover toolbar would otherwise sit, forcing it to move above the post instead of covering that text.'
  await testPost(page, 'fake/post', { channel_id: 'c-town', username: 'bob', message: short })
  await testPost(page, 'fake/post', { channel_id: 'c-town', username: 'bob', message: long })
  await testPost(page, 'fake/webhook', {
    channel_id: 'c-town', username: 'bob', message: '', override_username: 'jenkins',
    override_icon_url: '/static/images/webhook-icon.png',
    attachments: [{ color: '#00c100', title: attachmentTitle, text: 'Branch: **master**', footer: 'build #1' }],
  })
  await page.reload()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()

  const box = (loc: Locator) => loc.evaluate((el) => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, left: r.left, right: r.right } })

  const shortPost = feed(page).locator('article', { hasText: short })
  await shortPost.hover()
  const shortToolbar = shortPost.getByTestId('post-toolbar')
  await expect(shortToolbar).toBeVisible()
  await expect(shortToolbar).toHaveAttribute('data-placement', 'inside-top')
  const shortArticleBox = await box(shortPost)
  const shortToolbarBox = await box(shortToolbar)
  expect(shortToolbarBox.top).toBeGreaterThanOrEqual(shortArticleBox.top - 1) // inside: never above the article's own top
  expect(shortToolbarBox.bottom).toBeLessThanOrEqual(shortArticleBox.bottom + 1)
  await page.screenshot({ path: `${DENSITY_SHOTS}/toolbar-e2e-1-short-inside.png` })
  await page.mouse.move(5, 5)

  const longPost = feed(page).locator('article', { hasText: longHead })
  await longPost.hover()
  const longToolbar = longPost.getByTestId('post-toolbar')
  await expect(longToolbar).toBeVisible()
  await expect(longToolbar).toHaveAttribute('data-placement', 'overhang')
  const longArticleBox = await box(longPost)
  const longToolbarBox = await box(longToolbar)
  expect(longToolbarBox.top).toBeLessThan(longArticleBox.top) // overhangs above the article's own top
  await page.screenshot({ path: `${DENSITY_SHOTS}/toolbar-e2e-2-wrapping-overhang.png` })
  await page.mouse.move(5, 5)

  const attachmentPost = feed(page).locator('article', { hasText: attachmentTitle })
  await attachmentPost.hover()
  const attachmentToolbar = attachmentPost.getByTestId('post-toolbar')
  await expect(attachmentToolbar).toBeVisible()
  await expect(attachmentToolbar).toHaveAttribute('data-placement', 'overhang')
  const attachmentArticleBox = await box(attachmentPost)
  const attachmentToolbarBox = await box(attachmentToolbar)
  expect(attachmentToolbarBox.top).toBeLessThan(attachmentArticleBox.top)
  await page.screenshot({ path: `${DENSITY_SHOTS}/toolbar-e2e-3-attachment-overhang.png` })

  await removeServerFromMenu(page)
})
