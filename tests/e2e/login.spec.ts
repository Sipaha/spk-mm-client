import { expect, test } from '@playwright/test'
import { addFakeServer, apiToken, fakeURL, removeServerFromMenu, signInAlice } from './helpers'

async function removeServer(page: Parameters<typeof addFakeServer>[0]) {
  page.once('dialog', (d) => d.accept())
  await page.getByRole('button', { name: 'Remove server' }).click()
  await expect(page.getByRole('heading', { name: 'Add a Mattermost server' })).toBeVisible()
}

test('invalid address shows an error', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('Server address').fill('ftp://nope')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('Invalid server address')
})

test('password sign-in and sign-out', async ({ page }) => {
  await addFakeServer(page)
  await page.getByLabel('Login or email').fill('alice')
  await page.getByLabel('Password').fill('wrong')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('Wrong login or password')

  await page.getByLabel('Password').fill('secret')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()

  await page.getByRole('button', { name: 'Server menu' }).click()
  await page.getByRole('menuitem', { name: 'Sign out' }).click()
  await expect(page.getByText('Not signed in')).toBeVisible()
  await removeServer(page)
})

test('GitLab SSO through mmauth:// callback', async ({ page, context }) => {
  await addFakeServer(page)
  const popupPromise = context.waitForEvent('page')
  await page.getByRole('button', { name: 'Sign in with GitLab' }).click()
  const popup = await popupPromise
  await popup.getByText('Fake GitLab').waitFor()
  await popup.locator('#authorize').click()
  const href = await popup.locator('#mmauth-link').getAttribute('href')
  expect(href).toMatch(/^mmauth:\/\/callback\?/)
  await popup.close()

  // In the desktop app the OS hands this URL to us; here we deliver it
  // through the test API exactly as the single-instance handler would.
  const r = await page.request.post('/api/_test/deeplink', {
    headers: { Authorization: `Bearer ${await apiToken(page)}`, Origin: new URL(page.url()).origin },
    data: { url: href },
  })
  expect(r.ok()).toBeTruthy()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()

  // second delivery of the same callback is accepted silently, not an error
  const again = await page.request.post('/api/_test/deeplink', {
    headers: { Authorization: `Bearer ${await apiToken(page)}`, Origin: new URL(page.url()).origin },
    data: { url: href },
  })
  expect(again.ok()).toBeTruthy()
  expect(await again.json()).toEqual({ status: 'ok' })
  await expect(page.getByRole('alert')).toHaveCount(0)
  await removeServerFromMenu(page)
})
