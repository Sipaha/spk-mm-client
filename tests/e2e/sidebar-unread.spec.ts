import { expect, test } from '@playwright/test'
import { channel, removeServerFromMenu, signInAlice, testPost, unique } from './helpers'

// sidebar-unread-brief.md ruling 2: "More unreads"/"More mentions" overflow
// pills. Only 3 real channels + a couple of DMs exist in the seed, so a
// short window (rather than a long channel list) is what pushes a row out
// of view — same alternative the brief names. A failed test must not leave
// its server behind (same rule as chat.spec.ts/feed-scroll.spec.ts).
test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
})

test('bottom pill: an unread DM below the fold shows it; clicking scrolls it into view and hides the pill', async ({ page }) => {
  await signInAlice(page)
  // Short enough that, scrolled to the top (the default), the last row
  // (bob's DM, at the bottom of the list — Favorites/Channels/Direct
  // messages in that order) is below the fold.
  await page.setViewportSize({ width: 1280, height: 190 })
  const bottomPill = page.getByRole('button', { name: /More unreads below/ })
  await expect(bottomPill).toBeHidden()

  await testPost(page, 'fake/post', { channel_id: 'c-dm-bob', username: 'bob', message: unique('below the fold') })
  await expect(bottomPill).toBeVisible()

  await bottomPill.click()
  await expect(channel(page, /^bob(,|$)/)).toBeInViewport()
  await expect(bottomPill).toBeHidden()
})

test('top pill: a mention above the fold shows the "more mentions" variant; clicking scrolls the channel into view and hides the pill', async ({ page }) => {
  await signInAlice(page)
  await page.setViewportSize({ width: 1280, height: 190 })
  const list = page.getByRole('complementary', { name: 'Server channels' })

  // Scroll the channel list itself to the bottom so Off-Topic (the first
  // row, alphabetically first of Channels) goes above the fold.
  await list.locator('div.overflow-y-auto').evaluate((el) => { el.scrollTop = el.scrollHeight })

  const topPill = page.getByRole('button', { name: /More mentions above/ })
  await expect(topPill).toBeHidden()

  // bob (a member) mentions alice in Off-Topic, which alice isn't viewing.
  await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: unique('@alice above the fold') })
  await expect(topPill).toBeVisible()

  await topPill.click()
  await expect(channel(page, /Off-Topic/)).toBeInViewport()
  await expect(topPill).toBeHidden()
})
