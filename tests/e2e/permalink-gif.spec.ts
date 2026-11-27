import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { channel, fakePost, fakeURL, feed, focusedRow, removeServerFromMenu, screenshotDir, signInAlice, testPost, unique } from './helpers'

test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
})

test('permalink navigation, quote card and animated GIF previews with lightbox', async ({ page }) => {
  await signInAlice(page)
  const base = await fakeURL(page)

  // 1. Seed a target message in Off-Topic so clicking a permalink from Town Square
  // switches channels, focuses and highlights the target post.
  const targetText = unique('Target permalink post')
  const targetId = await fakePost(page, 'c-offtopic', 'bob', targetText)

  // 2. Post a message in Town Square containing:
  //    - a Mattermost permalink URL
  //    - a server-side type="permalink" quote embed
  const permalinkUrl = `${base}/demo/pl/${targetId}`
  await testPost(page, 'fake/post', {
    channel_id: 'c-town',
    username: 'carol',
    message: `See this update: ${permalinkUrl}`,
    permalink_post_id: targetId,
  })

  // 3. Post a message in Town Square with an inline Markdown GIF + bare GIF URL
  //    served from the fake server's /static/images/sample.gif (valid GIF89a).
  const gifUrl = `${base}/static/images/sample.gif`
  const gifTag = unique('GIF preview post')
  await fakePost(page, 'c-town', 'bob', `${gifTag}\n![party gif](${gifUrl})`)

  // 4. Also upload a .gif attachment with generic application/octet-stream MIME.
  const uploadTag = unique('Uploaded GIF octet')
  await testPost(page, 'fake/gif-file', {
    channel_id: 'c-town',
    username: 'bob',
    message: uploadTag,
    name: 'funny.gif',
    mime: 'application/octet-stream',
  })

  const log = feed(page)

  // Verify the quote card renders with author, channel and quoted message.
  const quoteCard = log.locator(`[data-permalink-card][data-permalink-post-id="${targetId}"]`)
  await expect(quoteCard).toBeVisible()
  await expect(quoteCard).toContainText('bob')
  await expect(quoteCard).toContainText('#Off-Topic')
  await expect(quoteCard).toContainText(targetText)

  // Verify the Markdown GIF renders via /media/.../postgif/ (never raw remote URL)
  // and actually decodes to a real image (naturalWidth > 0).
  const gifArticle = log.locator('article', { hasText: gifTag })
  const gifImg = gifArticle.locator('img[src*="/media/"][src*="/postgif/"]')
  await expect(gifImg).toBeVisible()
  await expect.poll(() => gifImg.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0)).toBe(true)
  expect(await gifImg.getAttribute('src')).toMatch(/^\/media\/\d+\/postgif\//)

  // Verify the uploaded .gif with octet-stream MIME also renders as an inline image preview.
  const uploadArticle = log.locator('article', { hasText: uploadTag })
  const uploadImg = uploadArticle.locator('img[src*="/media/"][src*="/feed/"]')
  await expect(uploadImg).toBeVisible()
  await expect.poll(() => uploadImg.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0)).toBe(true)

  // Screenshot 1: Town Square feed showing permalink quote card + animated GIF previews.
  await page.screenshot({ path: join(screenshotDir, 'permalink-and-gif-feed.png') })

  // Click the inline GIF to open GifLightbox and verify it displays and closes via Escape.
  await gifImg.click()
  const lightbox = page.getByTestId('gif-lightbox')
  await expect(lightbox).toBeVisible()
  const lightboxImg = lightbox.locator('img[src*="/media/"][src*="/postgif/"]')
  await expect.poll(() => lightboxImg.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0)).toBe(true)

  // Screenshot 2: GifLightbox open over the feed.
  await page.screenshot({ path: join(screenshotDir, 'gif-lightbox.png') })

  await page.keyboard.press('Escape')
  await expect(lightbox).toBeHidden()

  // Click the permalink URL inside the message -> switches to Off-Topic and focuses target post.
  await log.getByRole('link', { name: permalinkUrl }).click()
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
  await expect(focusedRow(log)).toContainText(targetText)

  // Screenshot 3: Focused target post in Off-Topic after permalink jump.
  await page.screenshot({ path: join(screenshotDir, 'permalink-jumped.png') })

  // Go back to Town Square and verify clicking the permalink quote card also jumps to Off-Topic!
  await channel(page, /Town Square/).click()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await expect(quoteCard).toBeVisible()
  await quoteCard.click()
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
  await expect(focusedRow(log)).toContainText(targetText)
})
