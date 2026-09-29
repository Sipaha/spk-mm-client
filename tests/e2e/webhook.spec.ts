import { expect, test } from '@playwright/test'
import { feed, removeServerFromMenu, serverId, signInAlice, testPost, unique } from './helpers'

// Webhook posts (the fake allows EnablePostUsernameOverride and
// EnablePostIconOverride): the webhook's own name with the account in a
// tooltip, BOT, and its own icon — fetched by Go and addressed by post id.
// Never the owner's avatar: without an icon, or when it is refused, the
// generic webhook icon.
test('a webhook post shows its own name and icon; without one or a refused one, the generic webhook icon', async ({ page }) => {
  await signInAlice(page)
  const srv = await serverId(page)

  const gitlab = unique('pipeline #42 passed')
  const { id } = (await testPost(page, 'fake/webhook', {
    channel_id: 'c-town', username: 'bob', message: gitlab,
    override_username: 'GitLab', override_icon_url: '/static/images/webhook-icon.png',
  })) as { id: string }
  // An icon on the server's host but another port (and a loopback address
  // at that): Go refuses it — the token must not go there, and the LAN is
  // off limits — and the UI shows the account's avatar instead.
  const lan = unique('deploy finished')
  await testPost(page, 'fake/webhook', {
    channel_id: 'c-town', username: 'bob', message: lan,
    override_username: 'Deploy', override_icon_url: 'http://127.0.0.1:9/fox.png',
  })
  const bare = unique('nightly build done')
  await testPost(page, 'fake/webhook', {
    channel_id: 'c-town', username: 'bob', message: bare, override_username: 'Nightly',
  })
  const party = unique('release tagged')
  await testPost(page, 'fake/webhook', {
    channel_id: 'c-town', username: 'bob', message: party,
    override_username: 'Release', override_icon_emoji: ':rocket:',
  })

  const post = feed(page).locator('article', { hasText: gitlab })
  await expect(post.getByText('GitLab', { exact: true })).toHaveAttribute('title', 'bob')
  await expect(post.getByText('BOT', { exact: true })).toBeVisible()
  const icon = post.locator('img').first()
  await expect(icon).toHaveAttribute('src', new RegExp(`^/media/${srv}/posticon/${id}\\?v=[0-9a-f]{16}$`))
  await expect.poll(() => icon.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth)).toBe(64)

  const refused = feed(page).locator('article', { hasText: lan })
  await expect(refused.getByText('Deploy', { exact: true })).toBeVisible()
  await expect(refused.locator('[data-webhook-icon]')).toBeVisible()
  await expect(refused.locator('img')).toHaveCount(0)

  const plain = feed(page).locator('article', { hasText: bare })
  await expect(plain.getByText('Nightly', { exact: true })).toHaveAttribute('title', 'bob')
  await expect(plain.locator('[data-webhook-icon]')).toBeVisible()
  await expect(plain.locator('img')).toHaveCount(0)

  const rocket = feed(page).locator('article', { hasText: party })
  await expect(rocket.getByText('Release', { exact: true })).toBeVisible()
  await expect(rocket.getByText('🚀')).toBeVisible()

  await expect(feed(page).locator('article', { hasText: party })).toBeInViewport()
  await page.screenshot({ path: 'test-results/webhook-override.png' })
  await removeServerFromMenu(page)
})
