import { expect, test } from '@playwright/test'
import { feed, removeServerFromMenu, signInAlice, testPost, unique } from './helpers'

// The reported bug (2026-09-28 live check): a GitLab bot post shows
// ":white_check_mark:"/":arrows_counterclockwise:" as literal text instead
// of the emoji the official client renders. Town Square (c-town), a fresh
// post per test run (unique()) so this doesn't depend on any other test's
// channel history.
test('a ":name:" shortcode in a post renders as the emoji, not literal text', async ({ page }) => {
  await signInAlice(page)
  const heading = page.getByRole('heading', { name: /Town Square/ })
  await expect(heading).toBeVisible()

  const text = unique('done')
  await testPost(page, 'fake/post', { channel_id: 'c-town', username: 'bob', message: `:white_check_mark: ${text}` })

  const post = feed(page).locator('article', { hasText: text })
  await expect(post).toBeVisible()
  await expect(post).toContainText(`✅ ${text}`)
  await expect(post).not.toContainText(':white_check_mark:')
  await page.screenshot({ path: 'test-results/emoji-text.png' })

  await removeServerFromMenu(page)
})
