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

test('bottom pill: a DM below the fold shows the "more mentions" variant (every DM counts as a mention); clicking scrolls it into view and reading it clears both', async ({ page }) => {
  await signInAlice(page)
  // Short enough that, scrolled to the top (the default), the last row
  // (bob's DM, at the bottom of the list — Favorites/Channels/Direct
  // messages in that order) is below the fold.
  await page.setViewportSize({ width: 1280, height: 190 })
  const bottomPill = page.getByRole('button', { name: /More (unreads|mentions) below/ })
  await expect(bottomPill).toBeHidden()

  // A plain DM message (no "@") still carries a mention count of 1 — a real
  // Mattermost DM notifies like a mention regardless of "@" — so this pill
  // is expected to come up in its "mentions" style/wording, not "unreads".
  await testPost(page, 'fake/post', { channel_id: 'c-dm-bob', username: 'bob', message: unique('below the fold') })
  await expect(page.getByRole('button', { name: 'More mentions below' })).toBeVisible()

  await bottomPill.click()
  const bobDm = channel(page, /^bob(,|$)/)
  await expect(bobDm).toBeInViewport()
  await expect(bottomPill).toBeHidden()

  // Read it: the fake server is shared across the whole e2e run, and an
  // unread mention left on alice's account here would otherwise leak into a
  // later, unrelated test's mention-count assertion (same rule as
  // chat.spec.ts's "a mention from someone else... reading clears it" test —
  // removeServerFromMenu only forgets the server locally, it doesn't reset
  // server-side unread/mention state).
  await bobDm.click()
  await expect(bobDm).not.toHaveAccessibleName(/Mentions:/)
})

test('top pill: a plain unread channel above the fold shows the "more unreads" variant; clicking scrolls the channel into view and reading it clears both', async ({ page }) => {
  await signInAlice(page)
  await page.setViewportSize({ width: 1280, height: 190 })
  const list = page.getByRole('complementary', { name: 'Server channels' })

  // Scroll the channel list itself to the bottom so Off-Topic (the first
  // row, alphabetically first of Channels) goes above the fold.
  await list.locator('div.overflow-y-auto').evaluate((el) => { el.scrollTop = el.scrollHeight })

  const topPill = page.getByRole('button', { name: /More (unreads|mentions) above/ })
  await expect(topPill).toBeHidden()

  // bob (a member) posts a plain message into Off-Topic, which alice isn't
  // viewing — unread, no mention, so this pill is expected in its plain
  // "unreads" style/wording.
  await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: unique('above the fold') })
  await expect(page.getByRole('button', { name: 'More unreads above' })).toBeVisible()

  const offTopic = channel(page, /Off-Topic/)
  await topPill.click()
  await expect(offTopic).toBeInViewport()
  await expect(topPill).toBeHidden()

  // Read it (same reason as the bottom-pill test above): clicking it opens
  // the channel — the same "view channel" call every channel switch makes
  // elsewhere in the app — clearing its unread state on the shared fake
  // server before this test hands control back.
  await offTopic.click()
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
  await expect(offTopic).toHaveAttribute('aria-current', 'true')
})
