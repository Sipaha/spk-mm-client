import { expect, test } from '@playwright/test'
import { existsSync } from 'node:fs'
import { channel, feed, removeServerFromMenu, serverId, signInAlice, testGet, testPost } from './helpers'

// A failed test must not leave its server behind (chat.spec.ts rule); every
// test below also closes the viewer (Escape) before it ends, so the modal
// overlay never sits on top of the "Server menu" button this hook clicks.
test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
})

const viewer = (page: import('@playwright/test').Page) => page.getByRole('dialog', { name: 'File viewer' })

// This must be the first test to open manual.pdf in the whole suite: Go's
// own /media disk cache (internal/media) would otherwise already hold it
// from an earlier test, and fake/throttle-file only slows the *upstream*
// GET /api/v4/files/<id> call that fills that cache — a cache hit would
// finish instantly regardless of the throttle, defeating the point.
test('closing the viewer mid-load cancels the pending PDF fetch', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  const srv = await serverId(page)
  try {
    // 100 KB/s: manual.pdf (~4 MB, one JPEG per page) needs ~40 s at this
    // rate, far longer than the time between opening the viewer and
    // pressing Escape below — the fetch is still in flight when it closes.
    await testPost(page, 'fake/throttle-file', { bytes_per_sec: 100_000 })
    const [req] = await Promise.all([
      page.waitForRequest((r) => r.url().includes(`/media/${srv}/pdf/f-manual`) && r.method() === 'GET'),
      feed(page).getByRole('button', { name: 'View manual.pdf' }).click(),
    ])
    expect(req).toBeTruthy()
    await page.keyboard.press('Escape')
    await expect(viewer(page)).toHaveCount(0)

    // internal/media/pdf.go cancels the fetch once the last waiter (this
    // request) leaves, and does not negative-cache the abandoned call
    // (Task 1 re-review R2-1/R1): the next request must start a *fresh*
    // fetch. Restore full speed and prove that fresh fetch is fast — if the
    // old call were still alive instead, a same-file request would join it
    // and stay bound to the old throttled rate for the rest of its life,
    // timing out well before 8 s.
    await testPost(page, 'fake/throttle-file', { bytes_per_sec: 0 })
    const resp = await page.request.get(`/media/${srv}/pdf/f-manual`, { timeout: 8_000 })
    expect(resp.ok()).toBe(true)
  } finally {
    await testPost(page, 'fake/throttle-file', { bytes_per_sec: 0 })
  }
})

test('manual.pdf: page counter, paging to the landscape page, no horizontal overflow in fit mode', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  await feed(page).getByRole('button', { name: 'View manual.pdf' }).click()
  const v = viewer(page)
  const pageCounter = v.getByText(/^\d+ \/ 50$/)
  await expect(pageCounter).toHaveText('1 / 50')

  const scroller = v.locator('.overflow-auto')
  await expect.poll(() => scroller.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)

  // page 2 is the seeded landscape page (internal/mmfake/pdf.go,
  // LandscapePage) — fit mode must fit *that* page's own width too (Tasks
  // 2+3 fix round 2), not stretch it at page 1's scale.
  await expect(async () => {
    await v.locator('[data-page="2"]').scrollIntoViewIfNeeded()
    await expect(pageCounter).toHaveText('2 / 50')
  }).toPass()
  await expect.poll(() => scroller.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)

  expect(await page.locator('iframe, embed, object').count()).toBe(0)
  await page.keyboard.press('Escape')
  await expect(v).toHaveCount(0)
})

test('zoom in/out change the percentage; Fit width returns to the original scale', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  await feed(page).getByRole('button', { name: 'View manual.pdf' }).click()
  const v = viewer(page)
  // Wait for the real document first: before it loads, PdfView's toolbar
  // shows the placeholder zoom state (100%, its initial `zoom` default,
  // scaleFor() falls through to it while naturalSizes/base are still
  // empty) — capturing that instead of the settled fit-width scale would
  // race every click below against the async pdf.js load completing.
  await expect(v.getByText(/^\d+ \/ 50$/)).toHaveText('1 / 50')
  const percent = v.getByText(/^\d+%$/)
  const fitPercent = await percent.textContent()

  await v.getByRole('button', { name: 'Zoom in' }).click()
  await expect(percent).not.toHaveText(fitPercent!)

  await v.getByRole('button', { name: 'Zoom out' }).click()
  await v.getByRole('button', { name: 'Zoom out' }).click()
  await expect(percent).not.toHaveText(fitPercent!)

  await v.getByRole('button', { name: 'Fit width' }).click()
  await expect(percent).toHaveText(fitPercent!)

  await page.keyboard.press('Escape')
  await expect(v).toHaveCount(0)
})

test('the PDF text layer is really selectable, not just decorative', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  await feed(page).getByRole('button', { name: 'View manual.pdf' }).click()
  const v = viewer(page)
  // Page 2 (the seeded landscape page) is also within the KEEP=1 render
  // window from the moment page 1 is current, and its own heading text
  // ("… (landscape page)") contains this same substring — scope to page
  // 1's own placeholder/canvas host so the locator resolves to one span.
  const heading = v.locator('[data-page="1"] .pdf-text-layer span', { hasText: 'spk-mm-client manual' })
  await expect(heading).toBeVisible({ timeout: 15_000 })

  const box = (await heading.boundingBox())!
  await page.mouse.move(box.x + 2, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width - 2, box.y + box.height / 2)
  await page.mouse.up()
  const selected = await page.evaluate(() => window.getSelection()?.toString() ?? '')
  expect(selected.length).toBeGreaterThan(0)

  await page.keyboard.press('Escape')
  await expect(v).toHaveCount(0)
})

test('a broken PDF (spec.pdf) falls back to the file card, no iframe/embed/object', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  await feed(page).getByRole('button', { name: 'View spec.pdf' }).click()
  const v = viewer(page)
  // pdf.js's getDocument().promise rejects (spec.pdf is a header and
  // nothing else) — Viewer.tsx falls back to the same FileCard a broken
  // image gets, never a partial/broken PdfView toolbar.
  await expect(v.getByRole('button', { name: 'Download spec.pdf' })).toBeVisible()
  await expect(v.getByRole('button', { name: 'Zoom in' })).toHaveCount(0)
  expect(await page.locator('iframe, embed, object').count()).toBe(0)

  await page.keyboard.press('Escape')
  await expect(v).toHaveCount(0)
})

test('Download in the viewer header still downloads the file', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  await feed(page).getByRole('button', { name: 'View manual.pdf' }).click()
  const v = viewer(page)
  await expect(v.getByText(/^\d+ \/ 50$/)).toBeVisible()

  // Download feedback is on the button itself, not a banner above the feed
  // (commit 517287a): a ring while saving, then a "Show in folder" button
  // next to it that stays for the session. The viewer header's DownloadButton
  // is the `text` variant, so its reveal button is the plain "Show in
  // folder" (no filename) — the icon variant elsewhere (e.g. the feed card)
  // uses "Show <name> in folder" instead.
  await v.getByRole('button', { name: 'Download', exact: true }).click()
  const reveal = v.getByRole('button', { name: 'Show in folder', exact: true })
  await expect(reveal).toBeVisible()
  await reveal.click()
  let savedPath = ''
  await expect
    .poll(async () => (savedPath = ((await testGet(page, 'revealed-files')) as string[]).find((p) => p.endsWith('manual.pdf')) ?? ''))
    .not.toBe('')
  expect(existsSync(savedPath), `downloaded file missing on disk: ${savedPath}`).toBe(true)

  await page.keyboard.press('Escape')
  await expect(v).toHaveCount(0)
})
