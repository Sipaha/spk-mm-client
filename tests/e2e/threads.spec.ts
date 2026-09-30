import { expect, test, type Page } from '@playwright/test'
import {
  apiCall, channel, fakePost, feed, removeServerFromMenu, repliesLink, rootRow, seedThread, serverId, setCRT, signInAlice,
  testGet, testPost, threadComposer, threadFeed, threadPane, unique,
} from './helpers'

// Screenshots of scenarios 1, 5, 7 and 8 (Task 7): outside the repository.
const shots = process.env.E2E_SHOTS ?? 'test-results'

// Every test sets the fake's CRT mode before signing in (the client reads
// it at bootstrap) and puts it back to the suite's default afterwards; a
// failed test must not leave its server behind either (chat.spec.ts rule).
test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
  if (page.url().startsWith('http')) await testPost(page, 'fake/crt', { mode: 'disabled' })
})

async function signInWithCRT(page: Page, mode: 'always_on' | 'disabled') {
  await setCRT(page, mode)
  await signInAlice(page)
}

test('CRT on: a root shows "Replies: N" and no replies; the panel shows the thread and sends a reply; Esc closes', async ({ page }) => {
  await signInWithCRT(page, 'always_on')
  const root = unique('crt root')
  const r1 = unique('first reply')
  const r2 = unique('second reply')
  await seedThread(page, root, [r1, r2])

  await expect(repliesLink(page, root, 2)).toBeVisible()
  await expect(feed(page).getByText(r1)).toHaveCount(0)
  await expect(feed(page).getByText(r2)).toHaveCount(0)

  await repliesLink(page, root, 2).click()
  const pane = threadPane(page)
  await expect(pane).toBeVisible()
  await expect(threadFeed(page).getByText(root)).toBeVisible()
  await expect(threadFeed(page).getByText(r1)).toBeVisible()
  await expect(threadFeed(page).getByText(r2)).toBeVisible()

  const mine = unique('my reply')
  await threadComposer(page).fill(mine)
  await page.keyboard.press('Enter')
  await expect(threadFeed(page).getByText(mine)).toBeVisible()
  await expect(threadFeed(page).getByText('Sending…')).toHaveCount(0)
  await expect(threadFeed(page).getByText(mine)).toHaveCount(1) // REST reply and WS echo → one post
  await expect(feed(page).getByText(mine)).toHaveCount(0)
  await expect(repliesLink(page, root, 3)).toBeVisible()
  await page.screenshot({ path: `${shots}/threads-1-crt-panel.png` })

  await threadComposer(page).focus()
  await page.keyboard.press('Escape')
  await expect(pane).toBeHidden()
  await expect(repliesLink(page, root, 3)).toBeVisible()
})

test('the reply button of the hovered post opens its thread with the caret in the panel composer', async ({ page }) => {
  await signInWithCRT(page, 'always_on')
  const root = unique('hover root')
  const reply = unique('hover reply')
  await seedThread(page, root, [reply])

  await rootRow(page, root).hover()
  await rootRow(page, root).getByRole('button', { name: 'Reply in thread' }).click()
  await expect(threadFeed(page).getByText(reply)).toBeVisible()
  await expect(threadComposer(page)).toBeFocused()
})

test('a live reply shows up in the open panel, and the thread is marked read on the server', async ({ page }) => {
  await signInWithCRT(page, 'always_on')
  // alice's own root: she follows the thread (the server answers PUT read
  // with 404 for a thread one does not follow — and the client stops).
  const root = unique('live root')
  const rootId = await fakePost(page, 'c-town', 'alice', root)
  await fakePost(page, 'c-town', 'carol', unique('earlier reply'), rootId)
  await repliesLink(page, root, 1).click()
  await expect(threadPane(page)).toBeVisible()

  const live = unique('live reply')
  await fakePost(page, 'c-town', 'bob', live, rootId)
  await expect(threadFeed(page).getByText(live)).toBeVisible()
  await expect(repliesLink(page, root, 2)).toBeVisible()
  await expect
    .poll(async () => (((await testGet(page, 'fake/thread-reads')) as { RootID: string }[] | null) ?? []).filter((r) => r.RootID === rootId).length)
    .toBeGreaterThan(0)
})

test('an edited and a deleted reply update the panel and the count; a deleted root leaves a banner and a disabled composer', async ({ page }) => {
  await signInWithCRT(page, 'always_on')
  const root = unique('edit root')
  const keep = unique('reply to edit')
  const drop = unique('reply to delete')
  const { rootId, replyIds } = await seedThread(page, root, [keep, drop])
  await repliesLink(page, root, 2).click()
  await expect(threadFeed(page).getByText(drop)).toBeVisible()

  const edited = unique('edited reply')
  await testPost(page, 'fake/edit', { post_id: replyIds[0], message: edited })
  await expect(threadFeed(page).getByText(edited)).toBeVisible()
  await expect(threadFeed(page).getByText(keep, { exact: true })).toHaveCount(0)

  await testPost(page, 'fake/delete', { post_id: replyIds[1] })
  await expect(threadFeed(page).getByText(drop)).toHaveCount(0)
  await expect(repliesLink(page, root, 1)).toBeVisible()
  await expect(threadPane(page).getByText('Replies: 1', { exact: true })).toBeVisible() // the divider

  await testPost(page, 'fake/delete', { post_id: rootId })
  await expect(threadPane(page).getByText('The original message was deleted')).toBeVisible()
  await expect(threadComposer(page)).toBeDisabled()
})

// A real (small, valid) 64x64 PNG (same fixture as attachments.spec.ts).
const PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAABOklEQVR4nO2a0RHCMAxDU45l2IWBGKy7MA585KcH59R2HCtxrV/A0osaypVs5fUuK+uGDtCrBEArAdBKALTuI4Z+ng/qpW03vu2YATRCU28zgTEAYEanPtiJ0QWgjv4/RI2h38Qm6funaRqwjf4zVlqFuIFB6dXzZQCj0ytcBAA+6aVeXADP9CLH5X9KsAD8l5/vew6ASs90j34JYZefkyF0AzMsf1UjSegGllBcgHk2QBWVJ24DqygB0EoAtBIALRLA/Clsp6g8cRtYRaEB5tkGjSShGyhzlNDOEL2Bgi7h1J3VAIqB43uBS6jKvwSmo6ABTwa+l+wS8mEQuYj3wGgG6XzN36zVw/zBkW5p9N9CtlWop3UdNTCpAnlW4phAgTHLaZWqY5olzwsd5XnHuMxPiWmVAGglAFpfPR9mbwpvJicAAAAASUVORK5CYII='

test('an image pasted into the panel composer is a chip there only; Enter sends a reply with the image', async ({ page }) => {
  await signInWithCRT(page, 'always_on')
  const root = unique('paste root')
  const { rootId } = await seedThread(page, root, [unique('paste reply')])
  await repliesLink(page, root, 1).click()
  // AGENTS.md trap: the synthetic event must reach the panel that belongs
  // to this thread — wait until its drop target (data-root) is mounted.
  await expect(page.locator(`[data-file-drop-target][data-root="${rootId}"]`)).toBeVisible()

  const name = unique('thread shot') + '.png'
  await page.evaluate(
    ([base64, name, rootId]) => {
      const dt = new DataTransfer()
      dt.items.add(new File([Uint8Array.from(atob(base64), (c) => c.charCodeAt(0))], name, { type: 'image/png' }))
      const ta = document.querySelector(`[data-root="${rootId}"] textarea`) as HTMLTextAreaElement
      ta.focus()
      const ev = new ClipboardEvent('paste', { bubbles: true, cancelable: true })
      Object.defineProperty(ev, 'clipboardData', { value: dt })
      ta.dispatchEvent(ev)
    },
    [PNG_BASE64, name, rootId] as const,
  )
  const tray = (scope: ReturnType<typeof threadPane>) => scope.getByRole('list', { name: 'Attachments' })
  await expect(tray(threadPane(page)).getByText(name)).toBeVisible()
  await expect(page.locator('[data-file-drop-target]:not([data-root])').getByText(name)).toHaveCount(0) // not in the channel composer
  await page.screenshot({ path: `${shots}/threads-5-paste-chip.png` })

  const text = unique('reply with a picture')
  await threadComposer(page).fill(text)
  await page.keyboard.press('Enter')
  const post = threadFeed(page).locator('article', { hasText: text })
  await expect(post.getByRole('img', { name })).toBeVisible()
  await expect(post.getByText('Sending…')).toHaveCount(0)
  await expect(tray(threadPane(page))).toHaveCount(0)
  await expect(feed(page).getByText(text)).toHaveCount(0)
})

test('a long thread shows its last replies; scrolling up loads the rest up to the root without gaps or repeats', async ({ page }) => {
  await signInWithCRT(page, 'always_on')
  const { root_id: rootId } = (await testPost(page, 'fake/thread', { channel_id: 'c-town', username: 'bob', replies: 150 })) as { root_id: string }
  const id = await serverId(page)
  // The seeded root's text is "Thread root" (shared by every seeded
  // thread): open it by id, the way a notification click does.
  await testPost(page, 'notification-click', { server_id: id, channel_id: 'c-town', root_id: rootId })
  const log = threadFeed(page)
  await expect(log.getByText('Reply 150', { exact: true })).toBeVisible()
  const first = (await apiCall(page, 'GetThread', { id, root_id: rootId })) as { posts: unknown[]; has_more: boolean }
  expect(first.posts).toHaveLength(61) // the root + the last page of 60
  expect(first.has_more).toBe(true)

  // Walk up to the first reply (the root always heads the thread, loaded or
  // not), collecting every row the virtualizer renders on the way.
  const seen = new Map<string, string>() // post id → text
  const atTop = async () =>
    (await log.getByText('Reply 1', { exact: true }).isVisible()) && (await log.getByText('Thread root', { exact: true }).isVisible())
  for (let i = 0; i < 300; i++) {
    const rows = await log.locator('article[data-post-id]').evaluateAll((els) =>
      els.map((el) => ({ id: el.getAttribute('data-post-id')!, text: el.textContent ?? '' })),
    )
    const ids = rows.map((r) => r.id)
    expect(new Set(ids).size, 'no row rendered twice').toBe(ids.length)
    for (const r of rows) seen.set(r.id, r.text)
    if (await atTop()) break
    await log.evaluate((el) => el.scrollBy({ top: -Math.max(200, el.clientHeight / 2) }))
    await page.waitForTimeout(50)
  }
  expect(await atTop()).toBe(true)
  await expect(threadPane(page).getByText('Loading missed messages…')).toHaveCount(0)

  const numbers = [...seen.values()].flatMap((t) => [...t.matchAll(/Reply (\d+)/g)].map((m) => Number(m[1])))
  expect(new Set(numbers).size).toBe(150)
  expect(Math.min(...numbers)).toBe(1)
  expect(Math.max(...numbers)).toBe(150)
  expect(seen.size).toBe(151) // root + 150 replies

  const th = (await apiCall(page, 'GetThread', { id, root_id: rootId })) as { posts: { id: string; message: string }[] }
  expect(th.posts.map((p) => p.message)).toEqual(['Thread root', ...Array.from({ length: 150 }, (_, i) => `Reply ${i + 1}`)])
})

test('a mention in a thread reply notifies with its root; the click opens the channel and the panel; the badge goes out', async ({ page }) => {
  await signInWithCRT(page, 'always_on')
  const id = await serverId(page)
  const root = unique('mention root')
  const { rootId } = await seedThread(page, root, [unique('before the mention')])
  await channel(page, /Off-Topic/).click()
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()

  const text = unique('@alice have a look')
  await fakePost(page, 'c-town', 'carol', text, rootId)
  await expect
    .poll(async () => ((await testGet(page, 'notifications')) as { body: string; root_id?: string; channel_id: string }[])
      .filter((n) => n.body.includes(text)).map((n) => [n.channel_id, n.root_id]))
    .toEqual([['c-town', rootId]])
  // The server-level aggregate badge used to live on the ServerRail tile,
  // hidden with only this one fake server configured (sidebar-menu brief
  // addendum 2026-09-29) — ListServers is what's left to check it against
  // (the desktop tray badge is the other place, Go-only).
  const mentions = async () => ((await apiCall(page, 'ListServers')) as { id: number; mentions: number }[]).find((s) => s.id === id)?.mentions
  await expect.poll(mentions).toBe(1)
  await page.screenshot({ path: `${shots}/threads-7-mention-badge.png` })

  await testPost(page, 'notification-click', { server_id: id, channel_id: 'c-town', root_id: rootId })
  // level 1: the channel's own heading — the open panel's is "Thread · Town Square".
  await expect(page.getByRole('heading', { level: 1, name: /Town Square/ })).toBeVisible()
  await expect(threadFeed(page).getByText(text)).toBeVisible()
  await expect.poll(mentions).toBe(0)
  await page.screenshot({ path: `${shots}/threads-7-opened-from-notification.png` })
})

// Reply-style brief (2026-09-28): the context line reads "Commented on
// <author>'s message: <snippet>" (webapp-like emphasis), and EVERY inline
// reply — not just the first of a run — sits behind a left border bar
// (reply-bar), so a run of consecutive replies reads as one continuous block.
test('CRT off: replies sit in the feed under a "Commented on bob\'s message" line, behind a left border bar, that opens the panel; the root shows "Replies: N"', async ({ page }) => {
  await signInWithCRT(page, 'disabled')
  const root = unique('flat root')
  const r1 = unique('flat reply one')
  const r2 = unique('flat reply two')
  const rootId = await fakePost(page, 'c-town', 'bob', root)
  // A reply right under its root needs no context line (webapp isFirstReply):
  // another post in between makes the replies a new run.
  await fakePost(page, 'c-town', 'carol', unique('in between'))
  await fakePost(page, 'c-town', 'carol', r1, rootId)
  await fakePost(page, 'c-town', 'bob', r2, rootId)

  await expect(feed(page).getByText(r1)).toBeVisible()
  await expect(feed(page).getByText(r2)).toBeVisible()
  await expect(repliesLink(page, root, 2)).toBeVisible()
  const context = feed(page).getByRole('button', { name: `Commented on bob's message: ${root}` })
  await expect(context).toHaveCount(1) // only the first reply of the run carries it

  // Both replies of the run — not just the first — get the bar. rootRow's
  // hasText is a substring match, and the root's own text is embedded as r1's
  // context-line snippet ("Commented on bob's message: <root text>") — filter
  // that out so the root's own row (no bar) isn't confused with r1's.
  await expect(rootRow(page, r1).getByTestId('reply-bar')).toBeVisible()
  await expect(rootRow(page, r2).getByTestId('reply-bar')).toBeVisible()
  await expect(rootRow(page, root).filter({ hasNotText: 'Commented on' }).getByTestId('reply-bar')).toHaveCount(0) // the root itself: no bar
  await page.screenshot({ path: `${shots}/threads-8-crt-off-feed.png` })

  await context.click()
  await expect(threadFeed(page).getByText(r2)).toBeVisible()
  await expect(threadFeed(page).getByText(root)).toBeVisible()
  await page.screenshot({ path: `${shots}/threads-8-crt-off-panel.png` })
})

test('CRT switched on while signed in: replies leave the feed, "Replies: N" stays', async ({ page }) => {
  await signInWithCRT(page, 'disabled')
  const root = unique('switch root')
  const r1 = unique('switch reply one')
  const r2 = unique('switch reply two')
  await seedThread(page, root, [r1, r2])
  await expect(feed(page).getByText(r2)).toBeVisible()

  await testPost(page, 'fake/crt', { mode: 'always_on' })
  await testPost(page, 'fake/drop', { lose: true })
  await expect(feed(page).getByText(r1)).toHaveCount(0)
  await expect(feed(page).getByText(r2)).toHaveCount(0)
  await expect(repliesLink(page, root, 2)).toBeVisible()
})

test('clicking thread message text opens the panel for roots and inline replies without a context link', async ({ page }) => {
  await signInWithCRT(page, 'disabled')
  const root = unique('text click root')
  const reply = unique('text click reply')
  await seedThread(page, root, [reply])
  const row = rootRow(page, reply)
  // The reply directly follows its root, so it has no "Commented on" link.
  await expect(row.getByRole('button', { name: /Commented on/ })).toHaveCount(0)
  await row.getByText(reply, { exact: true }).click()
  await expect(threadFeed(page).getByText(root, { exact: true })).toBeVisible()
  await expect(threadFeed(page).getByText(reply, { exact: true })).toBeVisible()
  await expect(threadComposer(page)).toBeFocused()
  await page.screenshot({ path: `${shots}/threads-text-click.png` })
  await threadComposer(page).press('Escape')
  await expect(threadPane(page)).toBeHidden()
  await feed(page).getByText(root, { exact: true }).click()
  await expect(threadFeed(page).getByText(reply, { exact: true })).toBeVisible()
})
