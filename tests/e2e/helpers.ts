import { expect, type Page } from '@playwright/test'

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
