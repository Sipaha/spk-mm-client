import { expect, test } from '@playwright/test'
import { feed, removeServerFromMenu, signInAlice, unique } from './helpers'

// Composer brief 2026-09-29: the Mattermost-style composer (formatting
// toolbar, Aa toggle, emoji and send buttons). A failed test must not leave
// its server behind (same rule as chat.spec.ts).
test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
})

test('bold via the toolbar button, and via Ctrl+B, both render bold once sent', async ({ page }) => {
  await signInAlice(page)
  const box = page.getByRole('textbox', { name: 'Message' })

  const viaButton = unique('bold via button')
  await box.fill(viaButton)
  await box.press('Control+a')
  await page.getByRole('button', { name: 'Bold (Ctrl+B)' }).click()
  await expect(box).toHaveValue(`**${viaButton}**`)
  await box.press('Enter')
  await expect(feed(page).locator('article', { hasText: viaButton }).locator('strong', { hasText: viaButton })).toBeVisible()

  const viaShortcut = unique('bold via ctrl b')
  await box.fill(viaShortcut)
  await box.press('Control+a')
  await box.press('Control+b')
  await expect(box).toHaveValue(`**${viaShortcut}**`)
  await box.press('Enter')
  await expect(feed(page).locator('article', { hasText: viaShortcut }).locator('strong', { hasText: viaShortcut })).toBeVisible()
})

test('the emoji picker inserts :name: at the caret, and the sent post shows the real glyph', async ({ page }) => {
  await signInAlice(page)
  const box = page.getByRole('textbox', { name: 'Message' })
  const text = unique('say')
  await box.fill(`${text} `)

  await page.getByRole('button', { name: 'Emoji' }).click()
  const dialog = page.getByRole('dialog', { name: 'Emoji picker' })
  await expect(dialog).toBeVisible()
  await dialog.getByRole('textbox', { name: 'Search emoji' }).fill('grinning')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect(dialog).toBeHidden()
  await expect(box).toHaveValue(`${text} :grinning: `)
  await expect(box).toBeFocused()

  await box.press('Enter')
  const post = feed(page).locator('article', { hasText: text })
  await expect(post).toContainText('😀')
  await expect(post).not.toContainText(':grinning:')
})

// Coordination (composer brief 2026-09-29): the feed's "stay at the bottom
// on any size change" (not just new rows) is a separate, concurrently
// landing fix (Feed.tsx's ResizeObserver, AGENTS.md "Bottom-stick"). This
// only proves the composer's own half — it visibly auto-grows with more
// lines, up to a cap, then scrolls internally — and notes the feed-bottom
// interaction for a re-check once both pieces are on main together.
test('the composer auto-grows with several lines, then scrolls internally past its cap', async ({ page }) => {
  await signInAlice(page)
  const box = page.getByRole('textbox', { name: 'Message' })
  const before = (await box.boundingBox())!.height

  const lines = Array.from({ length: 8 }, (_, i) => `line ${i}`)
  for (const [i, line] of lines.entries()) {
    await box.type(line)
    if (i < lines.length - 1) await box.press('Shift+Enter')
  }
  const grown = (await box.boundingBox())!.height
  expect(grown).toBeGreaterThan(before)

  // Past the ~40%-of-pane cap the box itself stops growing further even as
  // more lines are added — it scrolls internally instead.
  for (let i = 0; i < 20; i++) await box.press('Shift+Enter')
  const capped = (await box.boundingBox())!.height
  expect(capped).toBeLessThanOrEqual(grown + 2) // +2: sub-pixel rounding
  await expect(feed(page)).toBeVisible() // the feed pane is still present, not squeezed to nothing

  await box.press('Control+a')
  await box.press('Delete')
})
