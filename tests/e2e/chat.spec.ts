import { expect, test } from '@playwright/test'
import { apiCall, channel, feed, removeServerFromMenu, serverId, signInAlice, testGet, testPost, unique } from './helpers'

test('sidebar, feed and sending once', async ({ page }) => {
  await signInAlice(page)
  // the DM row is now "avatar + bob, <status>"; the group "👥 alice, bob, carol" does not start with "bob"
  for (const name of [/Town Square/, /Off-Topic/, /Secret/, /^bob(,|$)/]) await expect(channel(page, name)).toBeVisible()
  await expect(feed(page).getByText('Message #150', { exact: true })).toBeVisible()

  const text = unique('hello e2e')
  await page.getByRole('textbox', { name: 'Message' }).fill(text)
  await page.keyboard.press('Enter')
  await expect(feed(page).getByText(text)).toBeVisible()
  await expect(feed(page).getByText('Sending…')).toHaveCount(0)
  await expect(feed(page).getByText(text)).toHaveCount(1) // REST reply and WS echo → one post
  await removeServerFromMenu(page)
})

test('a mention from someone else: badges, notification, reading clears it', async ({ page }) => {
  await signInAlice(page)
  const id = await serverId(page)
  const text = unique('@alice look')
  await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: text })
  // exact: true — the ServerRail button's own accessible name is "Fake MM — Mentions: 1"
  // (Task 12 fix), a substring match on "Mentions: 1" would also hit it, not just the badge.
  await expect(page.getByRole('navigation').getByLabel('Mentions: 1', { exact: true })).toBeVisible()
  await expect(channel(page, /Off-Topic/).getByLabel('Mentions: 1')).toBeVisible()
  await expect
    .poll(async () => ((await testGet(page, 'notifications')) as { body: string }[]).map((n) => n.body))
    .toContain(`bob: ${text}`)
  const notes = (await testGet(page, 'notifications')) as { title: string; server_id: number; channel_id: string; body: string }[]
  expect(notes.find((n) => n.body === `bob: ${text}`)).toMatchObject({ title: 'Off-Topic', server_id: id, channel_id: 'c-offtopic' })

  await channel(page, /Off-Topic/).click()
  await expect(feed(page).getByText(text)).toBeVisible()
  await expect(page.getByRole('navigation').getByLabel('Mentions: 1', { exact: true })).toHaveCount(0)
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
  await expect(feed(page).getByText('Sending…')).toHaveCount(0)

  await box.press('ArrowUp')
  const edit = page.getByRole('textbox', { name: 'Edit message' })
  await edit.fill(`${text} v2`)
  await edit.press('Enter')
  await expect(feed(page).getByText(`${text} v2`)).toBeVisible()
  await expect(feed(page).locator('article', { hasText: `${text} v2` }).getByText('(edited)')).toBeVisible()

  const post = feed(page).locator('article', { hasText: `${text} v2` })
  await post.hover()
  page.once('dialog', (d) => d.accept())
  await post.getByRole('button', { name: 'Delete' }).click()
  await expect(feed(page).getByText(`${text} v2`)).toHaveCount(0)
  await removeServerFromMenu(page)
})

test('mark as unread keeps the channel unread while it is open', async ({ page }) => {
  await signInAlice(page)
  const unread = async () => ((await apiCall(page, 'ListServers')) as { unread: boolean }[]).at(-1)?.unread
  await expect.poll(unread).toBe(false) // baseline: nothing unread, so the dot below comes from this action
  const post = feed(page).locator('article', { hasText: 'Message #140' })
  await post.hover()
  await post.getByRole('button', { name: 'Mark as unread' }).click()
  // exact: true — same substring clash as above ("Fake MM — Unread messages" on the button).
  await expect(page.getByRole('navigation').getByLabel('Unread messages', { exact: true })).toBeVisible()
  await page.waitForTimeout(1000) // the open, focused channel must NOT auto-read it again
  await expect(page.getByRole('navigation').getByLabel('Unread messages', { exact: true })).toBeVisible()
  expect(await unread()).toBe(true)
  await removeServerFromMenu(page)
})

test('history loads up to the first message', async ({ page }) => {
  await signInAlice(page)
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
