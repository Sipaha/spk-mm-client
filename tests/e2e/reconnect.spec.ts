import { expect, test } from '@playwright/test'
import { channel, feed, removeServerFromMenu, signInAlice, testPost, unique } from './helpers'

for (const lose of [false, true]) {
  test(`reconnect ${lose ? 'after the server lost our events: resync' : 'without loss: replay'} brings the missed post`, async ({ page }) => {
    await signInAlice(page)
    await channel(page, /Off-Topic/).click()
    await testPost(page, 'fake/drop', { lose })
    const text = unique(lose ? 'missed during resync' : 'missed during resume')
    await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: text })
    await expect(feed(page).getByText(text)).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Offline — reconnecting…')).toHaveCount(0)
    await removeServerFromMenu(page)
  })
}
