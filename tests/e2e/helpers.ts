import { expect, type Locator, type Page } from '@playwright/test'

export async function apiToken(page: Page) {
  return (await page.locator('meta[name="spk-mm-client-api-token"]').getAttribute('content'))!
}

async function headers(page: Page) {
  return { Authorization: `Bearer ${await apiToken(page)}`, Origin: new URL(page.url()).origin }
}

export async function testPost(page: Page, path: string, data: unknown = {}) {
  const r = await page.request.post(`/api/_test/${path}`, { headers: await headers(page), data })
  expect(r.ok(), `${path}: ${await r.text()}`).toBeTruthy()
  return r.json()
}

export async function testGet(page: Page, path: string) {
  const r = await page.request.get(`/api/_test/${path}`, { headers: await headers(page) })
  expect(r.ok()).toBeTruthy()
  return r.json()
}

export async function apiCall(page: Page, method: string, data: unknown = {}) {
  const r = await page.request.post(`/api/${method}`, { headers: await headers(page), data })
  expect(r.ok(), `${method}: ${await r.text()}`).toBeTruthy()
  return r.json()
}

export async function fakeURL(page: Page) {
  return ((await testGet(page, 'fake-url')) as { url: string }).url
}

export async function addFakeServer(page: Page) {
  await page.goto('/')
  await page.getByLabel('Server address').fill(await fakeURL(page))
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Fake MM' })).toBeVisible()
}

export async function signInAlice(page: Page) {
  await addFakeServer(page)
  await page.getByLabel('Login or email').fill('alice')
  await page.getByLabel('Password').fill('secret')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
}

export async function serverId(page: Page): Promise<number> {
  const list = (await apiCall(page, 'ListServers')) as { id: number }[]
  return list[list.length - 1].id
}

export function channel(page: Page, name: string | RegExp) {
  return page.getByRole('complementary', { name: 'Server channels' }).getByRole('button', { name })
}

export async function removeServerFromMenu(page: Page) {
  page.once('dialog', (d) => d.accept())
  await page.getByRole('button', { name: 'Server menu' }).click()
  await page.getByRole('menuitem', { name: 'Remove server' }).click()
  await expect(page.getByRole('heading', { name: 'Add a Mattermost server' })).toBeVisible()
}

// feed: the channel's own feed. The thread panel has a "Messages" log of its
// own, so the role and name alone are ambiguous while a panel is open.
export const feed = (page: Page) => page.locator('[role="log"][data-feed="channel"]')
export const unique = (label: string) => `${label} ${Date.now().toString(36)}`

// scrollUntilVisible: the feed is virtualized — a row outside the currently
// loaded/rendered window doesn't just sit off-screen, it isn't in the DOM at
// all, so a plain getByRole(...).click() on it can time out even though the
// post genuinely exists. This matters for Off-Topic's fixed seed posts
// (build.png, spec.pdf, …): the fake server and its channels live for the
// whole multi-file e2e run (one webServer, one DB, workers: 1), so by the
// time a later spec file switches to Off-Topic, enough *other* tests may
// already have posted into it that a seed post from early in its history
// has scrolled out of the feed's initial (bottom-anchored) window — a real,
// pre-existing issue (chat.spec.ts's "history loads up to the first
// message" test exists for exactly this shape of problem). Repeatedly
// scrolling the feed to its current top asks Feed.tsx's onLoadOlder for the
// next page each time (same pattern as that test) until the locator
// resolves, instead of assuming a lookup at the very bottom will always find
// it.
export async function scrollUntilVisible(page: Page, locator: Locator, timeout = 20_000) {
  await expect
    .poll(
      async () => {
        if ((await locator.count()) > 0) return true
        await feed(page).evaluate((el) => el.scrollTo({ top: 0 }))
        return false
      },
      { timeout },
    )
    .toBe(true)
}

// ---- threads ----

export const threadPane = (page: Page) => page.getByRole('complementary', { name: 'Thread', exact: true })
export const threadFeed = (page: Page) => threadPane(page).locator('[role="log"][data-feed="thread"]')
export const threadComposer = (page: Page) => threadPane(page).getByRole('textbox', { name: 'Message' })

// setCRT switches the fake's CollapsedThreads mode; a signed-in client sees
// it only after its next bootstrap (sign-in or fake/drop {lose:true}).
export async function setCRT(page: Page, mode: 'always_on' | 'default_on' | 'default_off' | 'disabled') {
  if (!page.url().startsWith('http')) await page.goto('/')
  await testPost(page, 'fake/crt', { mode })
}

export async function fakePost(page: Page, channelId: string, username: string, message: string, rootId = '') {
  return ((await testPost(page, 'fake/post', { channel_id: channelId, username, message, root_id: rootId })) as { id: string }).id
}

// seedThread: bob posts a root, then each reply alternates carol/bob.
export async function seedThread(page: Page, root: string, replies: string[], channelId = 'c-town') {
  const rootId = await fakePost(page, channelId, 'bob', root)
  const replyIds: string[] = []
  for (const [i, text] of replies.entries()) replyIds.push(await fakePost(page, channelId, i % 2 ? 'bob' : 'carol', text, rootId))
  return { rootId, replyIds }
}

// rootRow: the root's row in the channel feed, by its (unique) text.
export const rootRow = (page: Page, text: string) => feed(page).locator('article', { hasText: text })
export const repliesLink = (page: Page, text: string, n: number) =>
  rootRow(page, text).getByRole('button', { name: new RegExp(`^Replies: ${n}( ·|$)`) })

// Pending feedback is intentionally absent for the first 3 seconds. Its
// absence is no longer an acknowledgement: wait until sent-post actions exist.
export async function expectSent(post: Locator) {
  await post.hover()
  await expect(post.getByRole('button', { name: 'More actions' })).toBeVisible()
}

// ---- search ----

export const searchBox = (page: Page) => page.getByRole('combobox', { name: 'Search messages' })
export const searchPane = (page: Page) => page.getByRole('complementary', { name: 'Search results', exact: true })
// hitCard: a result card by its exact message text (the markdown's <p>; the
// card's own text also holds the author, the time and the channel).
export const hitCard = (page: Page, text: string) =>
  searchPane(page).locator('article[data-hit-id]').filter({ has: page.getByText(text, { exact: true }) })

// search: submits terms in the header field and waits for the answer.
export async function search(page: Page, terms: string) {
  await searchBox(page).fill(terms)
  await searchBox(page).press('Enter')
  await expect(searchPane(page).locator('[data-search-results][aria-busy="false"]')).toBeVisible()
}

// jumpedPost: the feed row a jump highlighted (Feed.tsx: .post--focus on
// the row around the target's article).
export const focusedRow = (log: Locator) => log.locator('[data-kind="post"].post--focus')

// offsetFromCenter: the row's centre minus the feed viewport's, as a
// fraction of the viewport's height (0 = centred).
export async function offsetFromCenter(log: Locator, row: Locator) {
  const [f, r] = await Promise.all([log.boundingBox(), row.boundingBox()])
  if (!f || !r) return Number.NaN
  return (r.y + r.height / 2 - (f.y + f.height / 2)) / f.height
}

// frames: two animation frames — a scroll's rows are laid out and painted.
export const frames = (page: Page) => page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))))

// walkFeed checks a virtualized feed's completeness the way a reader would:
// from its first post (history loaded by scrolling to the top until `first`
// shows) down to its very end, one step of ~⅔ viewport at a time, merging
// the mounted posts' data-post-id of every step into one ordered list — the
// DOM holds only the mounted range, so a count of rows proves nothing. On
// the way, an open gap row loads itself when it comes on screen (as for a
// user), and every step checks that the mounted range has no post twice and
// keeps the order of what was seen before. `label` extracts a post's label
// (e.g. its number) from the article's text; posts it doesn't match are
// still walked, only not returned.
export async function walkFeed(page: Page, log: Locator, first: string, label: RegExp, timeout = 90_000) {
  await expect
    .poll(
      async () => {
        await log.evaluate((el) => el.scrollTo({ top: 0 }))
        return log.getByText(first, { exact: true }).count()
      },
      { timeout: 30_000 },
    )
    .toBeGreaterThan(0)
  const order: string[] = []
  const labels = new Map<string, string>()
  const problems: string[] = []
  const deadline = Date.now() + timeout
  let still = 0
  for (;;) {
    await frames(page)
    // A reader waits for a page that is loading on screen before reading on.
    await expect.poll(() => gapLoading(log), { timeout: 15_000 }).toBe(false)
    const snap = await log.evaluate((el) => {
      const arts = [...el.querySelectorAll<HTMLElement>('article[data-post-id]')]
      return {
        posts: arts.map((a) => ({ id: a.dataset.postId!, text: a.innerText })),
        top: el.scrollTop,
        end: el.scrollHeight - el.clientHeight,
        gap: el.querySelector('[data-gap-open]') !== null,
      }
    })
    const ids = snap.posts.map((p) => p.id)
    if (new Set(ids).size !== ids.length) problems.push(`duplicate rows mounted: ${ids.filter((id, i) => ids.indexOf(id) !== i).join(',')}`)
    let prev: string | null = null
    let lastAt = -1
    for (const [i, p] of snap.posts.entries()) {
      const at = order.indexOf(p.id)
      if (at >= 0) {
        if (at < lastAt) problems.push(`order changed around ${p.id}`)
        lastAt = at
      } else {
        let insertAt = order.length
        if (prev !== null) insertAt = order.indexOf(prev) + 1
        else {
          const next = snap.posts.slice(i + 1).find((q) => order.includes(q.id))
          if (next) insertAt = order.indexOf(next.id)
        }
        order.splice(insertAt, 0, p.id)
        lastAt = insertAt
        const m = label.exec(p.text)
        if (m) labels.set(p.id, m[1])
      }
      prev = p.id
    }
    const atEnd = snap.top >= snap.end - 1 && !snap.gap
    still = atEnd ? still + 1 : 0
    if (still >= 3) break // the end, three steps in a row: nothing more comes
    if (Date.now() > deadline) throw new Error(`walkFeed: no end within ${timeout} ms (top ${snap.top}/${snap.end}, gap ${snap.gap})`)
    if (!atEnd) await log.evaluate((el) => el.scrollBy({ top: Math.round(el.clientHeight * 0.66) }))
    else await page.waitForTimeout(100)
  }
  expect(problems, problems.join('\n')).toEqual([])
  return order.filter((id) => labels.has(id)).map((id) => labels.get(id)!)
}

// gapOnScreen: the feed's open gap row is inside its viewport (Playwright's
// isVisible() is true for a row mounted in the overscan, off screen).
export const gapOnScreen = (log: Locator) =>
  log.evaluate((el) => {
    const row = el.querySelector('[data-gap-open]')
    if (!row) return false
    const v = el.getBoundingClientRect()
    const b = row.getBoundingClientRect()
    return b.bottom > v.top && b.top < v.bottom
  })

// gapLoading: the gap row on screen is loading its page (a gap row that
// comes on screen loads on its own within a frame — Feed.tsx checkGap).
export const gapLoading = (log: Locator) =>
  log.evaluate((el) => {
    const row = el.querySelector('[data-gap-open]')
    if (!row) return false
    const v = el.getBoundingClientRect()
    const b = row.getBoundingClientRect()
    return b.bottom > v.top && b.top < v.bottom && row.querySelector('button[disabled]') !== null
  })
