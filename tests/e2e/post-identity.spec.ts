import { expect, test } from '@playwright/test'
import {
  apiCall,
  channel,
  expectSent,
  fakePost,
  feed,
  removeServerFromMenu,
  repliesLink,
  seedThread,
  serverId,
  signInAlice,
  testPost,
  threadComposer,
  threadFeed,
  unique,
} from './helpers'

test.afterEach(async ({ page }) => {
  await testPost(page, 'fake/post-latency', { ms: 0 })
  await removeServerFromMenu(page)
})

async function assertNoDuplicatePostDOM(log: ReturnType<typeof feed>) {
  const snapshot = await log.evaluate((el) => {
    const articles = [...el.querySelectorAll('article[data-post-id]')]
    const counts = new Map<string, number>()
    for (const a of articles) {
      const id = a.getAttribute('data-post-id') ?? ''
      counts.set(id, (counts.get(id) ?? 0) + 1)
    }
    const duplicates = [...counts.entries()].filter(([, n]) => n > 1)
    const toolbars = [...el.querySelectorAll('[data-testid="post-toolbar"]')].map(
      (tb) => tb.closest('article[data-post-id]')?.getAttribute('data-post-id') ?? '',
    )
    return { total: articles.length, duplicates, toolbars }
  })
  expect(snapshot.duplicates).toEqual([])
  expect(snapshot.toolbars.length).toBeLessThanOrEqual(1)
  return snapshot
}

test('post identity & single toolbar across identical-text posts, optimistic send, reconnect, edits, reactions, channel switch, threads, and virtualized scroll', async ({
  page,
}) => {
  await signInAlice(page)
  const srvId = await serverId(page)

  // Seed enough history for virtualized scrolling plus two distinct posts with identical text.
  const prefix = unique('ident')
  for (let i = 1; i <= 28; i++) {
    await fakePost(page, 'c-town', i % 2 === 0 ? 'bob' : 'carol', `${prefix} filler ${i}`)
  }
  const sameBody = `${prefix} repeated exact text`
  const twin1 = await fakePost(page, 'c-town', 'bob', sameBody)
  const twin2 = await fakePost(page, 'c-town', 'bob', sameBody)
  expect(twin1).not.toBe(twin2)

  // Optimistic send -> confirmation.
  const composer = page.getByRole('textbox', { name: 'Message', exact: true })
  const ownText = `${prefix} own message`
  await testPost(page, 'fake/post-latency', { ms: 300 })
  await composer.fill(ownText)
  await composer.press('Enter')
  await testPost(page, 'fake/post-latency', { ms: 0 })

  const ownRow = feed(page).locator('article').filter({ hasText: ownText })
  await expect(ownRow).toHaveCount(1)
  await expectSent(ownRow)

  // Edit own post and react to twin1.
  await ownRow.getByRole('button', { name: 'More actions' }).click()
  await page.getByRole('menuitem', { name: 'Edit' }).click()
  const editBox = ownRow.getByRole('textbox', { name: 'Edit message' })
  await editBox.fill(`${ownText} edited`)
  await editBox.press('Enter')
  await expect(feed(page).locator('article').filter({ hasText: `${ownText} edited` })).toHaveCount(1)

  const twin1Row = feed(page).locator(`article[data-post-id="${twin1}"]`)
  const twin2Row = feed(page).locator(`article[data-post-id="${twin2}"]`)
  await expect(twin1Row).toHaveCount(1)
  await expect(twin2Row).toHaveCount(1)
  await twin1Row.hover()
  await twin1Row.getByRole('button', { name: 'Add reaction' }).click()
  const pickerSearch = page.getByRole('dialog', { name: 'Emoji picker' }).getByRole('textbox')
  await pickerSearch.fill('tada')
  await pickerSearch.press('Enter')
  await expect(twin1Row.getByRole('button', { name: /^🎉/ })).toBeVisible()

  // WS drop + resync catch-up with a new post while disconnected.
  await testPost(page, 'fake/drop', { lose: true })
  const afterDropText = `${prefix} after resync`
  const afterDropId = await fakePost(page, 'c-town', 'carol', afterDropText)
  await expect(feed(page).locator(`article[data-post-id="${afterDropId}"]`)).toHaveCount(1, { timeout: 15_000 })

  // Channel switch away and back preserves unique post identity and both identical-text posts.
  await channel(page, /Off-Topic/).click()
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
  await channel(page, /Town Square/).click()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await expect(feed(page).locator(`article[data-post-id="${twin1}"]`)).toHaveCount(1)
  await expect(feed(page).locator(`article[data-post-id="${twin2}"]`)).toHaveCount(1)

  // Threaded feed: replies + optimistic reply inside Thread panel.
  const threadRootText = `${prefix} thread root`
  const { rootId } = await seedThread(page, threadRootText, [`${prefix} reply 1`, `${prefix} reply 2`], 'c-town')
  await repliesLink(page, threadRootText, 2).click()
  const tLog = threadFeed(page)
  await expect(tLog.locator(`article[data-post-id="${rootId}"]`)).toHaveCount(1)
  await threadComposer(page).fill(`${prefix} own thread reply`)
  await threadComposer(page).press('Enter')
  await expect(tLog.locator('article').filter({ hasText: `${prefix} own thread reply` })).toHaveCount(1)
  await assertNoDuplicatePostDOM(tLog)

  // Virtualized scroll + hover across rows: verify zero duplicate DOM copies and single toolbar aligned to hovered post.
  const chLog = feed(page)
  for (const frac of [0, 0.4, 0.8, 0.2, 1.0]) {
    await chLog.evaluate((el, f) => {
      el.scrollTop = Math.floor((el.scrollHeight - el.clientHeight) * f)
    }, frac)
    await page.waitForTimeout(80)
    await assertNoDuplicatePostDOM(chLog)
  }

  const targetRow = chLog.locator(`article[data-post-id="${afterDropId}"]`)
  await targetRow.scrollIntoViewIfNeeded()
  await targetRow.hover()
  const snap = await assertNoDuplicatePostDOM(chLog)
  expect(snap.toolbars).toEqual([afterDropId])

  // Confirm backend ChannelView also has each post ID once and both identical-text posts intact.
  const chView = (await apiCall(page, 'GetChannel', { id: srvId, channel_id: 'c-town' })) as { posts: { id: string }[] }
  const ids = chView.posts.map((p) => p.id)
  expect(new Set(ids).size).toBe(ids.length)
  expect(ids).toContain(twin1)
  expect(ids).toContain(twin2)
})
