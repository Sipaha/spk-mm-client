import { expect, test } from '@playwright/test'
import { channel, removeServerFromMenu, signInAlice } from './helpers'

// header-markdown-brief 2026-09-30 (user report, screenshot: the channel
// header showed literal "[Сприит](https://…)" instead of a clickable link).
// Off-Topic's seed (internal/mmfake/seed.go) carries a markdown header with
// a link for exactly this — no seeded channel had a header at all before
// this brief.
test('a channel header with a markdown link renders a real link, not raw markdown', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  // ChannelPane's <section aria-label={channel.name}> scopes us to this
  // channel's own pane (the sidebar has its own "Off-Topic" text elsewhere
  // on the page).
  const pane = page.getByRole('region', { name: 'Off-Topic' })
  const link = pane.getByRole('link', { name: 'Sprint' })
  await expect(link).toBeVisible()
  await expect(link).toHaveAttribute('href', 'https://example.com/sprint')
  await expect(pane.getByText('[Sprint]', { exact: false })).toHaveCount(0)
  await removeServerFromMenu(page)
})
