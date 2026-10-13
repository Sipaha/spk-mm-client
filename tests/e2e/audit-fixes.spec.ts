import { expect, test } from '@playwright/test'
import { apiCall, feed, screenshotDir, serverId, signInAlice } from './helpers'

test('cached media URLs belong to the current signed-in account', async ({ page }) => {
 await signInAlice(page)
 const id = await serverId(page)
 const servers = () => apiCall(page, 'ListServers') as Promise<{ id: number; media_scope: string }[]>
 const alice = (await servers()).find(s => s.id === id)!.media_scope
 const oldURL = `/media/${id}/thumb/f-build?account=${alice}`
 expect((await page.request.get(oldURL)).status()).toBe(200)
 try {
  await apiCall(page, 'Logout', { id })
  expect((await page.request.get(oldURL)).status()).toBe(404)
  await apiCall(page, 'LoginWithPassword', { id, login: 'bob', password: 'secret' })
  const bob = (await servers()).find(s => s.id === id)!.media_scope
  expect(bob).not.toBe(alice)
  expect((await page.request.get(oldURL)).status()).toBe(404)
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await expect.poll(async () => feed(page).locator('img[src*="account="]').count()).toBeGreaterThan(0)
  await expect.poll(async () => feed(page).locator(`img[src*="account=${alice}"]`).count()).toBe(0)
  await expect(page.locator('aside')).toHaveCount(1)
  await expect(page.locator('main')).toHaveCount(1)
  await page.screenshot({ path: `${screenshotDir}/account-isolation.png` })
 } finally {
  await apiCall(page, 'RemoveServer', { id })
 }
})
