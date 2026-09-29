import { expect, test } from '@playwright/test'
import { feed, removeServerFromMenu, signInAlice, testPost, unique } from './helpers'

// Town Square (c-town), not Off-Topic: carol isn't a member of c-offtopic
// (AGENTS.md — the fake's ...As test helpers panic on a non-member), and
// this test reacts as both bob and carol. Uses a fresh post of its own so
// its reactor list is exactly what this test puts there.
test('hovering a reaction chip shows who reacted; "and N others" opens the full list', async ({ page }) => {
  await signInAlice(page)
  const heading = page.getByRole('heading', { name: /Town Square/ })
  await expect(heading).toBeVisible()

  const text = unique('reactor tooltip test')
  await testPost(page, 'fake/post', { channel_id: 'c-town', username: 'bob', message: text })
  await expect(feed(page).getByText(text)).toBeVisible()
  await testPost(page, 'fake/react', { channel_id: 'c-town', message: text, username: 'bob', emoji: '+1' })
  await testPost(page, 'fake/react', { channel_id: 'c-town', message: text, username: 'carol', emoji: '+1' })

  const post = feed(page).locator('article', { hasText: text })
  // Matched by prefix, not the exact count — the accessible name (and the
  // count in it) changes once react-extra adds ten more below; a
  // locator pinned to "👍 2" would stop matching anything after that.
  const chip = post.getByRole('button', { name: /^👍/ })
  await expect(chip).toHaveAccessibleName('👍 2')
  await chip.hover()

  const tooltip = page.getByRole('tooltip')
  await expect(tooltip).toBeVisible()
  await expect(tooltip).toHaveText('bob and carol')
  await expect(chip).toHaveAttribute('aria-describedby', await tooltip.getAttribute('id') ?? '')
  await page.screenshot({ path: 'test-results/reactions-tooltip.png' })

  // Ten more real, named fake accounts (mmfake.Options.ExtraUsers — dave,
  // erin, frank, ... — seeded as c-town members precisely so this works)
  // react too: 12 total, every one of them resolvable via POST users/ids.
  // The tooltip truncates to the first 10 shown plus a clickable overflow.
  await testPost(page, 'fake/react-extra', { channel_id: 'c-town', message: text, emoji: '+1', n: 10 })
  await heading.hover() // move the pointer away first so the next hover is a real mouseenter
  await expect(chip).toHaveAccessibleName('👍 12')
  await chip.hover()
  const more = page.getByRole('button', { name: '2 other users' })
  await expect(more).toBeVisible()
  await more.click()

  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  const names = ['bob', 'carol', 'dave', 'erin', 'frank', 'grace', 'heidi', 'ivan', 'judy', 'mallory', 'niaj', 'olivia']
  await expect(dialog.getByRole('listitem')).toHaveCount(names.length)
  for (const name of names) await expect(dialog.getByText(name, { exact: true })).toBeVisible()
  await expect(dialog.getByText('Unknown user')).toHaveCount(0) // every reactor here has a real, resolved profile
  await page.screenshot({ path: 'test-results/reactions-modal.png' })

  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await expect(chip).toBeFocused()

  await removeServerFromMenu(page)
})
