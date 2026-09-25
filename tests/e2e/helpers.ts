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

export const feed = (page: Page) => page.getByRole('log', { name: 'Messages' })
export const unique = (label: string) => `${label} ${Date.now().toString(36)}`
