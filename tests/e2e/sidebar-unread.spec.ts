import { expect, test } from '@playwright/test'
import { channel, removeServerFromMenu, serverId, signInAlice, testPost, unique } from './helpers'

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
  // Short enough that, scrolled to the top (the default), a second Unreads
  // row is below the fold (measured: header 52px, so a 53px-tall scroller
  // shows only the Unreads header + its first row, each 32px).
  // Reserve the new 40px product header: the sidebar scroller stays 53px.
  await page.setViewportSize({ width: 1280, height: 145 })
  const bottomPill = page.getByRole('button', { name: /More (unreads|mentions) below/ })
  await expect(bottomPill).toBeHidden()

  // With the Unreads section (sidebar-sections-brief.md) sitting above every
  // other category, a lone newly-unread channel lands as Unreads' very
  // first — and so already-visible — row; posting an explicit "@alice"
  // mention to the group DM *after* bob's DM sorts it above bob's DM
  // (mentions first, then most-recently-active), pushing bob's row below
  // the fold instead. (A plain, "@"-less group message would not mention
  // alice at all — only a DM auto-mentions the other member regardless of
  // "@"; internal/mmfake/chat.go mentionsLocked.)
  //
  // A plain DM message (no "@") still carries a mention count of 1 — a real
  // Mattermost DM notifies like a mention regardless of "@" — so this pill
  // is expected to come up in its "mentions" style/wording, not "unreads".
  await testPost(page, 'fake/post', { channel_id: 'c-dm-bob', username: 'bob', message: unique('below the fold') })
  await testPost(page, 'fake/post', { channel_id: 'c-gm', username: 'carol', message: unique('@alice keeps the group on top') })
  await expect(page.getByRole('button', { name: 'More mentions below' })).toBeVisible()

  await bottomPill.click()
  const bobDm = channel(page, /^bob(,|$)/)
  await expect(bobDm).toBeInViewport()
  await expect(bottomPill).toBeHidden()

  // Read both: the fake server is shared across the whole e2e run, and an
  // unread mention left on alice's account here would otherwise leak into a
  // later, unrelated test's mention-count assertion (same rule as
  // chat.spec.ts's "a mention from someone else... reading clears it" test —
  // removeServerFromMenu only forgets the server locally, it doesn't reset
  // server-side unread/mention state).
  await bobDm.click()
  await expect(bobDm).not.toHaveAccessibleName(/Mentions:/)
  const gm = channel(page, /alice, bob, carol/)
  await gm.click()
  await expect(gm).not.toHaveAccessibleName(/Mentions:/)
})

test('top pill: a plain unread channel above the fold shows the "more unreads" variant; clicking scrolls the channel into view and reading it clears both', async ({ page }) => {
  await signInAlice(page)
  await page.setViewportSize({ width: 1280, height: 230 })
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

// unreadsList: the Unreads section's own <ul> (sidebar-sections-brief.md) —
// its heading text is exact ("Unreads", uppercased only by CSS), so scoping
// through it disambiguates "is this row under Unreads" from "is this row
// somewhere in the sidebar at all" (channel() alone can't tell).
function unreadsList(page: import('@playwright/test').Page) {
  return page.getByText('Unreads', { exact: true }).locator('xpath=following-sibling::ul')
}

test('Unreads: an unread channel appears there and leaves its own category; opening it keeps it there until switching away', async ({ page }) => {
  await signInAlice(page)
  // Scoped to Off-Topic itself throughout — not "Unreads is absent" as a
  // precondition, which the shared fake server (workers: 1, one backend for
  // the whole e2e run) can't guarantee: an unrelated channel left unread by
  // another spec file would make the section already exist without this
  // test's own channel being in it.
  const channelsSection = page.getByRole('button', { name: 'Channels' }).locator('xpath=ancestor::section')
  await expect(channelsSection.getByRole('button', { name: /Off-Topic/ })).toBeVisible()
  await expect(unreadsList(page).getByRole('button', { name: /Off-Topic/ })).toHaveCount(0)

  await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: unique('unreads section') })

  await expect(unreadsList(page).getByRole('button', { name: /Off-Topic/ })).toBeVisible()
  await expect(channelsSection.getByRole('button', { name: /Off-Topic/ })).toHaveCount(0)

  // Open it: the row is read almost immediately, but stays under Unreads
  // (the held active channel) rather than jumping back to Channels.
  await unreadsList(page).getByRole('button', { name: /Off-Topic/ }).click()
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
  await expect(unreadsList(page).getByRole('button', { name: /Off-Topic/ })).toBeVisible()
  await expect(channelsSection.getByRole('button', { name: /Off-Topic/ })).toHaveCount(0)

  // Switch away: it returns to Channels and leaves Unreads for good.
  await channel(page, 'Town Square').click()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await expect(unreadsList(page).getByRole('button', { name: /Off-Topic/ })).toHaveCount(0)
  await expect(channelsSection.getByRole('button', { name: /Off-Topic/ })).toBeVisible()
})

// Fix round 1 (sidebar-sections-review.md): the held-channel capture used to
// live only in Sidebar.tsx's own row onClick, so opening an unread channel
// any other way — a notification click, a ~channel link — never held it: it
// would drop out of Unreads the instant it was marked read instead of
// staying until the user switched away. Moved to chat.ts's openChannel, the
// single gateway every origin funnels through. This covers the notification
// origin end to end; the ~channel link origin is covered at the unit level
// in Markdown.test.tsx (clicking it drives the real chat.ts openChannel,
// not a mock).
test('Unreads: opening an unread channel via a notification click holds it too, not just a sidebar click', async ({ page }) => {
  await signInAlice(page)
  const channelsSection = page.getByRole('button', { name: 'Channels' }).locator('xpath=ancestor::section')
  await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: unique('via notification') })
  await expect(unreadsList(page).getByRole('button', { name: /Off-Topic/ })).toBeVisible()

  await testPost(page, 'notification-click', { server_id: await serverId(page), channel_id: 'c-offtopic' })
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
  await expect(unreadsList(page).getByRole('button', { name: /Off-Topic/ })).toBeVisible()
  await expect(channelsSection.getByRole('button', { name: /Off-Topic/ })).toHaveCount(0)

  await channel(page, 'Town Square').click()
  await expect(unreadsList(page).getByRole('button', { name: /Off-Topic/ })).toHaveCount(0)
  await expect(channelsSection.getByRole('button', { name: /Off-Topic/ })).toBeVisible()
})
