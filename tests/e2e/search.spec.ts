import { expect, test, type Locator, type Page, type Route } from '@playwright/test'
import {
  apiCall, channel, fakePost, feed, focusedRow, frames, gapLoading, gapOnScreen, hitCard, offsetFromCenter, removeServerFromMenu, search, searchBox, searchPane,
  serverId, setCRT, signInAlice, testPost, threadFeed, threadPane, unique, walkFeed,
} from './helpers'

// Message search and the jump to a result (docs/specs/2026-09-30-search-design.md).
// Town Square (c-town) holds the seed «Message #1…#150»; its window is the
// latest 60 posts, so a jump to #10 builds a history segment around it with
// an open gap to the window. The fake is shared by the whole run: these
// tests only add posts (every other spec that reads Town Square finds its
// posts at the bottom, and chat.spec.ts — which reads the seed's tail — runs
// before this file).

const shots = process.env.E2E_SHOTS ?? 'test-results'

test.describe.configure({ timeout: 120_000 }) // the scroll walks read a few hundred posts step by step

test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
  if (page.url().startsWith('http')) await testPost(page, 'fake/crt', { mode: 'disabled' })
})

const seed = (n: number) => Array.from({ length: n }, (_, i) => String(i + 1))

async function townGap(page: Page) {
  const ch = (await apiCall(page, 'GetChannel', { id: await serverId(page), channel_id: 'c-town' })) as { gap: { open: boolean } }
  return ch.gap.open
}

// hold: the UI's next request to `method` waits until release(). 'request'
// keeps the request itself from Go until then (the UI waits, Go knows
// nothing yet); 'answer' sends it on at once and holds only Go's answer —
// Go has done the work, the UI hears of it late (a slow link).
async function hold(page: Page, method: string, what: 'request' | 'answer') {
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  let taken = false
  let arrived!: () => void
  const seen = new Promise<void>((r) => (arrived = r))
  let finished!: () => void
  const done = new Promise<void>((r) => (finished = r))
  const handler = async (route: Route) => {
    if (taken) return route.continue()
    taken = true
    arrived()
    if (what === 'request') {
      await gate
      await route.continue().catch(() => {}) // the UI may have aborted it meanwhile
      finished()
      return
    }
    const response = await route.fetch().catch(() => null)
    await gate
    if (response) await route.fulfill({ response }).catch(() => {})
    else await route.abort().catch(() => {})
    finished()
  }
  await page.route(`**/api/${method}`, handler)
  return {
    // seen: the UI has asked (within 10 s — a hang here says the UI never did).
    // done: the held call has been handed back to the UI (or dropped, if the
    // UI aborted it meanwhile).
    done,
    seen: () => Promise.race([seen, new Promise<never>((_, reject) => setTimeout(() => reject(new Error(`hold: the UI never called ${method}`)), 10_000))]),
    release: async () => {
      release()
      await page.unroute(`**/api/${method}`, handler)
    },
  }
}

// feedPost: a feed's post by its exact message text.
const feedPost = (log: Locator, text: string) => log.locator('article').filter({ has: log.page().getByText(text, { exact: true }) })

// jumpToTen: Ctrl+F, «Message #10», Enter, a click on its card — the feed
// centres and highlights the post.
async function jumpToTen(page: Page) {
  await page.keyboard.press('Control+f')
  await expect(searchBox(page)).toBeFocused()
  await search(page, 'Message #10')
  const card = hitCard(page, 'Message #10')
  await expect(card).toHaveCount(1)
  await card.click()
  const row = focusedRow(feed(page))
  await expect(row).toContainText('Message #10')
  // The jump keeps correcting its centring for a few frames (Feed.tsx
  // centerOn) and would undo a scripted scroll made meanwhile: wait until
  // it has converged.
  await expect.poll(async () => Math.abs(await offsetFromCenter(feed(page), row))).toBeLessThan(0.02)
  return row
}

test('search → results with highlight → jump to an old post: centred, highlighted, a gap row; scrolling down fills it; the walk finds all 150', async ({ page }) => {
  await signInAlice(page)
  await page.keyboard.press('Control+f')
  await expect(searchBox(page)).toBeFocused()
  await search(page, 'Message #10')
  const card = hitCard(page, 'Message #10')
  await expect(card).toHaveCount(1)
  await expect(card.locator('mark')).toHaveText(['Message', '#10'])
  await expect(card).toContainText('~town-square')
  await page.screenshot({ path: `${shots}/search-1-results.png` })

  await card.click()
  const log = feed(page)
  const row = focusedRow(log)
  await expect(row).toContainText('Message #10')
  await expect(row).toBeInViewport()
  expect(Math.abs(await offsetFromCenter(log, row))).toBeLessThan(0.15)
  expect(await townGap(page)).toBe(true)
  await page.screenshot({ path: `${shots}/search-2-jump-highlight.png` })

  // Down to the gap row, its page held: the row shows, then the fill closes the gap.
  const held = await hold(page, 'LoadNewer', 'request')
  const gapRow = log.locator('[data-gap-open]')
  await expect
    .poll(async () => {
      if (await gapOnScreen(log)) return true
      await log.evaluate((el) => el.scrollBy({ top: Math.round(el.clientHeight * 0.8) }))
      return false
    }, { timeout: 30_000, intervals: [50] })
    .toBe(true)
  await held.seen()
  await expect(gapRow.getByRole('button', { name: 'Loading…' })).toBeVisible()
  await page.screenshot({ path: `${shots}/search-3-gap-row.png` })
  await held.release()
  await expect(gapRow.getByRole('button', { name: 'Loading…' })).toHaveCount(0)

  // Reading on down fills the rest of the gap (how many pages depends on
  // what earlier specs posted); the walk proves nothing is lost or repeated.
  const seen = await walkFeed(page, log, 'Message #1', /^Message #(\d+)$/m)
  expect(seen).toEqual(seed(150))
  expect(await townGap(page)).toBe(false)
  await expect(gapRow).toHaveCount(0)
})

test('an open segment and 70 new posts: the gap fills without losing or repeating a post', async ({ page }) => {
  await signInAlice(page)
  await jumpToTen(page)
  expect(await townGap(page)).toBe(true)
  const tag = unique('burst')
  for (let i = 1; i <= 70; i++) await fakePost(page, 'c-town', 'bob', `${tag} ${i}`)
  // The window keeps its latest 60: the 10 it drops fall into the gap.
  const escaped = tag.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const seen = await walkFeed(page, feed(page), 'Message #1', new RegExp(`^(Message #\\d+|${escaped} \\d+)$`, 'm'))
  expect(seen.filter((l) => l.startsWith('Message #'))).toEqual(seed(150).map((n) => `Message #${n}`))
  expect(seen.filter((l) => l.startsWith(tag))).toEqual(seed(70).map((n) => `${tag} ${n}`))
  expect(seen.indexOf(`${tag} 1`)).toBeGreaterThan(seen.indexOf('Message #150'))
  expect(await townGap(page)).toBe(false)
})

test('a gap reached from below, a live post while its page loads: the post on screen does not move', async ({ page }, info) => {
  await signInAlice(page)
  await jumpToTen(page)
  const log = feed(page)
  const held = await hold(page, 'LoadNewer', 'request')
  // From the window's end upwards until the gap row shows.
  await expect
    .poll(() => log.evaluate((el) => {
      el.scrollTo({ top: el.scrollHeight })
      return el.scrollHeight - el.clientHeight - el.scrollTop
    }))
    .toBeLessThan(2)
  // Up with the mouse wheel, as a reader does (a wheel gesture is what
  // the feed must hold the posts through — review, fix round 1).
  const box = (await log.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await expect
    .poll(async () => {
      if (await gapOnScreen(log)) return true
      await page.mouse.wheel(0, -Math.round(box.height * 0.6))
      await frames(page)
      return false
    }, { timeout: 30_000, intervals: [50] })
    .toBe(true)
  await held.seen()
  // The row in the upper part of the viewport: the reader came from the
  // window below, so the first post under the row is what must stay put.
  const rowAt = () => log.evaluate((el) => Math.round(el.querySelector('[data-gap-open]')!.getBoundingClientRect().top - el.getBoundingClientRect().top))
  await page.mouse.wheel(0, (await rowAt()) - Math.round(box.height * 0.2))
  const scrollTop = () => log.evaluate((el) => el.scrollTop)
  let last = -1
  await expect.poll(async () => { const t = await scrollTop(); const same = t === last; last = t; await frames(page); return same }).toBe(true)
  expect(await rowAt()).toBeLessThan(box.height / 2)
  // The probe: a window post under the row, mid-screen — not the window's
  // first post, which the live post below pushes out of the window (with the
  // gap open it goes into the gap and comes back with the page).
  const probe = () =>
    log.evaluate((el) => {
      const view = el.getBoundingClientRect()
      const mid = view.top + el.clientHeight * 0.6
      const arts = [...el.querySelectorAll<HTMLElement>('article[data-post-id]')]
      const a = arts.reduce((best, x) => (Math.abs(x.getBoundingClientRect().top - mid) < Math.abs(best.getBoundingClientRect().top - mid) ? x : best))
      return { id: a.dataset.postId!, top: Math.round(a.getBoundingClientRect().top - view.top) }
    })
  const before = await probe()
  const at = (id: string) =>
    log.evaluate((el, id) => {
      const a = el.querySelector<HTMLElement>(`article[data-post-id="${id}"]`)
      return a ? Math.round(a.getBoundingClientRect().top - el.getBoundingClientRect().top) : null
    }, id)

  // A live post while the page is on its way: the feed's rows change
  // (the jump-to-latest badge counts it) before the page's revision.
  await fakePost(page, 'c-town', 'bob', unique('live during the gap load'))
  await expect(page.getByRole('button', { name: 'Jump to latest messages — 1 new' })).toBeVisible()
  await frames(page)
  expect(await at(before.id)).toBe(before.top)

  // Rows on screen, for the report if the probe moved.
  const onScreen = () =>
    log.evaluate((el) => {
      const v = el.getBoundingClientRect()
      return [...el.querySelectorAll<HTMLElement>('[data-kind]')]
        .filter((r) => r.getBoundingClientRect().bottom > v.top && r.getBoundingClientRect().top < v.bottom)
        .map((r) => `${r.dataset.kind}@${Math.round(r.getBoundingClientRect().top - v.top)}+${Math.round(r.getBoundingClientRect().height)} ${r.innerText.replace(/\n/g, '|').slice(0, 40)}`)
        .join('\n')
    })
  const rowsBefore = await onScreen()
  const rev = async () => ((await apiCall(page, 'GetChannel', { id: await serverId(page), channel_id: 'c-town' })) as { hist_rev: number }).hist_rev
  const revBefore = await rev()
  await held.release()
  // The held page lands; the row, still on screen, may load a few more on
  // its own (how many depends on what earlier specs posted) — wait until
  // nothing is loading on screen, twice across a frame.
  await expect.poll(rev).toBeGreaterThan(revBefore)
  await expect
    .poll(async () => {
      if (await gapLoading(log)) return 'loading'
      await frames(page)
      return (await gapLoading(log)) ? 'loading' : 'idle'
    }, { timeout: 20_000 })
    .toBe('idle')
  await frames(page)
  const moved = await expect.poll(() => at(before.id)).toBe(before.top).then(() => null, (e: unknown) => e)
  if (moved) {
    const rows = `before:\n${rowsBefore}\nafter:\n${await onScreen()}`
    info.annotations.push({ type: 'rows', description: rows })
    console.log(`the probe ${JSON.stringify(before)} moved; rows on screen ${rows}`)
    throw moved
  }
  await page.screenshot({ path: `${shots}/search-4-gap-from-below.png` })
})

test('opening the right panel and resizing the window keep the jumped-to post where it was', async ({ page }) => {
  await signInAlice(page)
  const log = feed(page)
  await jumpToTen(page)
  await searchPane(page).getByRole('button', { name: 'Close search' }).click()
  await expect(searchPane(page)).toBeHidden()
  const post = feedPost(log, 'Message #10')
  const top = () => post.evaluate((a) => Math.round(a.getBoundingClientRect().top - a.closest('[role="log"]')!.getBoundingClientRect().top))
  await frames(page)
  const was = await top()
  const moved = async () => Math.abs((await top()) - was)

  await post.hover()
  await post.getByRole('button', { name: 'Reply in thread' }).click()
  await expect(threadPane(page)).toBeVisible()
  await expect(threadFeed(page).getByText('Message #10', { exact: true })).toBeVisible()
  await expect.poll(moved).toBeLessThanOrEqual(2)
  await page.screenshot({ path: `${shots}/search-5-panel-keeps-position.png` })

  for (const width of [1100, 1500, 1280]) {
    await page.setViewportSize({ width, height: 720 })
    await expect.poll(moved).toBeLessThanOrEqual(2)
  }
  expect(await townGap(page)).toBe(true) // still the segment, not reset to the window
})

test('races: of two quick jumps the second is shown; a second click on the same result centres it again', async ({ page }) => {
  await signInAlice(page)
  await search(page, 'from:bob 1*')
  const pane = searchPane(page)
  const results = pane.locator('[data-search-results]')
  // #10 is on a later page: scroll the results until it arrives.
  await expect
    .poll(async () => {
      if ((await hitCard(page, 'Message #10').count()) > 0) return true
      await results.evaluate((el) => el.scrollTo({ top: el.scrollHeight }))
      return false
    }, { timeout: 20_000 })
    .toBe(true)
  await expect(hitCard(page, 'Message #150')).toHaveCount(1)

  const log = feed(page)
  const held = await hold(page, 'JumpToPost', 'answer')
  await hitCard(page, 'Message #10').click()
  await held.seen()
  await hitCard(page, 'Message #150').click()
  await expect(focusedRow(log)).toContainText('Message #150')
  await held.release()
  await held.done // the late answer of the first jump is back (or dropped: the UI aborted it) and must change nothing
  await frames(page)
  await expect(focusedRow(log)).toContainText('Message #150')
  await expect(focusedRow(log)).toBeInViewport()

  await hitCard(page, 'Message #10').click()
  const row = focusedRow(log)
  await expect(row).toContainText('Message #10')
  expect(Math.abs(await offsetFromCenter(log, row))).toBeLessThan(0.15)
  // The reader scrolls away with the wheel (a real gesture: it also ends
  // the jump's own centring corrections).
  const box = (await log.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.wheel(0, 400)
  await expect.poll(async () => Math.abs(await offsetFromCenter(log, row))).toBeGreaterThan(0.15)
  await hitCard(page, 'Message #10').click()
  await expect.poll(async () => Math.abs(await offsetFromCenter(log, focusedRow(log)))).toBeLessThan(0.15)
})

test('suggestions: from:bo → bob, in:off → off-topic', async ({ page }) => {
  await signInAlice(page)
  const box = searchBox(page)
  const list = page.getByRole('listbox', { name: 'Search suggestions' })
  await box.click()
  await box.pressSequentially('from:bo')
  await expect(list.getByRole('option', { name: /bob/ })).toBeVisible()
  await expect(box).toHaveAttribute('aria-expanded', 'true')
  await page.screenshot({ path: `${shots}/search-6-from-suggestions.png` })
  await box.press('Enter')
  await expect(box).toHaveValue('from:bob ')
  await expect(list).toBeHidden()

  await box.pressSequentially('in:off')
  await expect(list.getByRole('option', { name: /Off-Topic/ })).toBeVisible()
  await box.press('Enter')
  await expect(box).toHaveValue('from:bob in:off-topic ')
})

test('CRT: a reply found beyond the 200 latest opens the thread around it, highlighted; scrolling down loads the newer replies', async ({ page }) => {
  await setCRT(page, 'always_on')
  await signInAlice(page)
  await testPost(page, 'fake/thread', { channel_id: 'c-town', username: 'bob', replies: 250 })
  await search(page, 'Reply 5')
  const card = hitCard(page, 'Reply 5')
  await expect(card).toHaveCount(1)
  await card.click()
  const pane = threadPane(page)
  await expect(pane).toBeVisible()
  const tf = threadFeed(page)
  await expect(focusedRow(tf)).toContainText('Reply 5')
  await expect(pane.getByText('Showing the replies around the one found')).toBeVisible()
  await expect(pane.getByRole('button', { name: 'Back to results' })).toBeVisible()
  await page.screenshot({ path: `${shots}/search-7-thread-focus.png` })

  const seen = await walkFeed(page, tf, 'Thread root', /^Reply (\d+)$/m)
  expect(seen).toEqual(seed(250))
  await expect(tf.locator('[data-gap-open]')).toHaveCount(0)
})

test('a channel switch keeps the results; «Back to results» from a thread; Esc closes them', async ({ page }) => {
  await signInAlice(page)
  const text = unique('keeps results')
  await fakePost(page, 'c-offtopic', 'bob', text)
  await search(page, 'Message #10')
  await expect(hitCard(page, 'Message #10')).toHaveCount(1)

  await channel(page, /Off-Topic/).click()
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
  await expect(searchPane(page)).toBeVisible()
  await expect(hitCard(page, 'Message #10')).toHaveCount(1)

  const post = feed(page).locator('article', { hasText: text })
  await expect(post).toBeInViewport()
  await post.hover()
  await post.getByRole('button', { name: 'Reply in thread' }).click()
  await expect(threadPane(page)).toBeVisible()
  await expect(searchPane(page)).toBeHidden()
  await threadPane(page).getByRole('button', { name: 'Back to results' }).click()
  await expect(searchPane(page)).toBeVisible()
  await expect(threadPane(page)).toBeHidden()
  await expect(hitCard(page, 'Message #10')).toHaveCount(1)
  await page.screenshot({ path: `${shots}/search-8-back-to-results.png` })

  await searchPane(page).getByRole('heading', { name: /Search results/ }).click()
  await page.keyboard.press('Escape')
  await expect(searchPane(page)).toBeHidden()
})

test('Cyrillic: «Привет мир» is found by «привет» and highlighted', async ({ page }) => {
  await signInAlice(page)
  // Into the channel on screen: read at once. A post left unread in another
  // channel would stay unread on the shared fake for sidebar-unread.spec.ts.
  const text = `Привет мир ${unique('cyr')}`
  await fakePost(page, 'c-town', 'bob', text)
  await search(page, 'привет')
  const card = hitCard(page, text)
  await expect(card).toHaveCount(1)
  await expect(card.locator('mark')).toHaveText(['Привет'])
  await expect(card).toContainText('~town-square')
  await page.screenshot({ path: `${shots}/search-9-cyrillic.png` })
})

// A long result list (the session holds up to 500 hits; the list is not
// virtualized): 450 hits render, scroll and come back from a thread without
// a stall. Bounds are loose (a headless Chromium on a loaded CI host); the
// numbers go to the test's annotations and the console for the report.
test('a long result list: 450 hits load page by page, scroll smoothly and remount quickly after a thread', async ({ page }, info) => {
  await signInAlice(page)
  const tag = `lng${Date.now().toString(36)}`
  for (let i = 0; i < 450; i += 25) {
    await Promise.all(Array.from({ length: 25 }, (_, j) => fakePost(page, 'c-town', 'bob', `${tag} row ${i + j + 1} with **some** markdown and a [link](https://example.com/${i + j})`)))
  }
  const cards = searchPane(page).locator('article[data-hit-id]')
  const results = searchPane(page).locator('[data-search-results]')
  const t0 = Date.now()
  await search(page, tag)
  await expect(cards).toHaveCount(20)
  const firstPage = Date.now() - t0
  const t1 = Date.now()
  await expect
    .poll(async () => {
      await results.evaluate((el) => el.scrollTo({ top: el.scrollHeight }))
      return cards.count()
    }, { timeout: 60_000, intervals: [100] })
    .toBe(450)
  const allPages = Date.now() - t1

  // Scroll the whole list in 120 px steps, one per frame: the longest frame.
  const scroll = await results.evaluate(async (el) => {
    el.scrollTo({ top: 0 })
    await new Promise((r) => requestAnimationFrame(r))
    const frames: number[] = []
    let last = performance.now()
    while (el.scrollTop + el.clientHeight < el.scrollHeight - 1) {
      el.scrollTop += 120
      await new Promise((r) => requestAnimationFrame(r))
      const now = performance.now()
      frames.push(now - last)
      last = now
    }
    frames.sort((a, b) => a - b)
    return { steps: frames.length, max: Math.round(frames.at(-1)!), p95: Math.round(frames[Math.floor(frames.length * 0.95)]) }
  })

  // A thread replaces the results; «Back to results» mounts all 450 cards again.
  // Town Square may have opened at an unread line (earlier specs' posts), not
  // at its bottom: go down to the latest post.
  const last = feed(page).locator('article', { hasText: `${tag} row 450 ` })
  await expect
    .poll(async () => {
      await feed(page).evaluate((el) => el.scrollTo({ top: el.scrollHeight }))
      return (await last.isVisible()) && (await last.evaluate((a) => {
        const f = a.closest('[role="log"]')!.getBoundingClientRect()
        const b = a.getBoundingClientRect()
        return b.top >= f.top && b.bottom <= f.bottom
      }).catch(() => false))
    }, { timeout: 15_000 })
    .toBe(true)
  await last.hover()
  await last.getByRole('button', { name: 'Reply in thread' }).click()
  await expect(searchPane(page)).toBeHidden()
  const back = threadPane(page).getByRole('button', { name: 'Back to results' })
  await expect(back).toBeVisible()
  const remount = await back.evaluate(async (button) => {
    const long: number[] = []
    const obs = new PerformanceObserver((l) => { for (const e of l.getEntries()) long.push(Math.round(e.duration)) })
    obs.observe({ type: 'longtask' })
    const t = performance.now()
    ;(button as HTMLElement).click()
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
    const ms = Math.round(performance.now() - t)
    obs.disconnect()
    return { ms, cards: document.querySelectorAll('[data-search-results] article[data-hit-id]').length, longest: Math.max(0, ...long) }
  })
  const numbers = { firstPageMs: firstPage, allPagesMs: allPages, scroll, remount }
  console.log(`long result list: ${JSON.stringify(numbers)}`)
  info.annotations.push({ type: 'long-list', description: JSON.stringify(numbers) })
  await page.screenshot({ path: `${shots}/search-10-long-list.png` })

  expect(remount.cards).toBe(450)
  expect(remount.ms).toBeLessThan(1500)
  expect(scroll.p95).toBeLessThan(100)
})
