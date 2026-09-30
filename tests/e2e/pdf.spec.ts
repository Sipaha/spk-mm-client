import { expect, test } from '@playwright/test'
import { existsSync } from 'node:fs'
import { channel, feed, removeServerFromMenu, serverId, signInAlice, testGet, testPost } from './helpers'

// A failed test must not leave its server behind (chat.spec.ts rule); every
// test below also closes the viewer (Escape) before it ends, but a failed
// assertion can still leave it open — press Escape here too (PDF final
// review M2), so a stuck-open dialog never blocks the "Server menu" click
// for its own 30 s timeout, which used to cascade into the next test/file.
test.afterEach(async ({ page }) => {
  if (await page.getByRole('dialog', { name: 'File viewer' }).isVisible().catch(() => false)) await page.keyboard.press('Escape')
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
    // Scoped to f-manual only, not every plain file (PDF final review I2):
    // throttling every file also slows Off-Topic's own text/markdown
    // snippet fetches (f-log, f-biglog, README, …), which go through the
    // same fake endpoint — that can saturate Chromium's 6-connection pool
    // and get the PDF request itself aborted client-side before Go's own
    // media cache ever sees it, so the test would "pass" even against a
    // build that never cancels anything server-side (the vacuous failure
    // mode this test used to have). 100 KB/s: manual.pdf (~4 MB, one JPEG
    // per page) needs ~40 s at this rate, far longer than this test needs.
    await testPost(page, 'fake/throttle-file', { bytes_per_sec: 100_000, file_id: 'f-manual' })
    await feed(page).getByRole('button', { name: 'View manual.pdf' }).click()

    // Wait for the *fake's own record* that Go's media cache actually
    // reached it and started reading — not a browser-side request event,
    // which proves only that the browser sent something, never that the
    // server-side fetch this test is about to cancel ever began.
    await expect
      .poll(async () => ((await testGet(page, 'fake/file-get-status?id=f-manual')) as { started: boolean }).started)
      .toBe(true)

    await page.keyboard.press('Escape')
    await expect(viewer(page)).toHaveCount(0)

    // internal/media/pdf.go cancels the fetch once the last waiter (this
    // request) leaves; that cancellation reaches the fake's own upstream
    // GET, whose handler observes r.Context().Done() (streamThrottled) and
    // records it (internal/mmfake, FileGetStatus — added for this test).
    // This is the non-vacuous proof I2 asked for: a build where
    // internal/media's Cache.get never cancels the abandoned call leaves
    // "cancelled" false forever and this poll times out the test, instead
    // of the old timing-based check, which passed either way.
    await expect
      .poll(async () => ((await testGet(page, 'fake/file-get-status?id=f-manual')) as { cancelled: boolean }).cancelled)
      .toBe(true)

    // Also not negative-cached (Task 1 re-review R1/R2-1): the next request
    // must start a *fresh* fetch, not join a still-running old call. Having
    // already confirmed the cancel server-side above (not just inferred it
    // from timing), this fast-completion check is no longer racing anything.
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
  //
  // Scroll page 2's own top to the scroller's top by reading real rendered
  // geometry (getBoundingClientRect), not scrollIntoViewIfNeeded() (PDF
  // final review M3): that only centres/aligns "as needed", which depends
  // on how much of the scroller's viewport page 1 already fills — true only
  // at some viewport sizes, not the actual contract being tested here.
  await expect(async () => {
    await scroller.evaluate((el) => {
      const p2 = el.querySelector('[data-page="2"]')
      if (!(p2 instanceof HTMLElement)) throw new Error('page 2 is not in the DOM yet')
      el.scrollTop += p2.getBoundingClientRect().top - el.getBoundingClientRect().top
    })
    await expect(pageCounter).toHaveText('2 / 50')
  }).toPass()
  await expect.poll(() => scroller.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)

  expect(await page.locator('iframe, embed, object').count()).toBe(0)
  await page.keyboard.press('Escape')
  await expect(v).toHaveCount(0)
})

test('zoom in/out change the percentage; Fit width is its own mode; Ctrl+0 returns to the default zoom', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  await feed(page).getByRole('button', { name: 'View manual.pdf' }).click()
  const v = viewer(page)
  // Wait for the real document first: before it loads, PdfView's toolbar
  // shows the placeholder zoom state (100%, its initial `zoom` default,
  // scaleFor() falls through to it while naturalSizes/base are still
  // empty) — capturing that instead of the settled default scale would
  // race every click below against the async pdf.js load completing.
  await expect(v.getByText(/^\d+ \/ 50$/)).toHaveText('1 / 50')
  const percent = v.getByText(/^\d+%$/)
  // Poll for two consecutive equal reads, not a single read right after
  // "1 / 50" appears (PDF final review M4): a classic, non-overlay
  // scrollbar changes the scroller's clientWidth, and PdfView's
  // ResizeObserver-driven re-fit can still land a tick after the page
  // count first shows up, changing the percentage out from under a
  // one-shot read (only while the pane is narrower than the default zoom's
  // page, which caps at fit-width).
  let lastPercent: string | null = null
  await expect
    .poll(async () => {
      const cur = await percent.textContent()
      const stable = cur !== null && cur === lastPercent
      lastPercent = cur
      return stable
    })
    .toBe(true)
  // The default zoom is the webapp's 175% (pdf-lag report 2026-09-30), not
  // fit-to-width: at the default 1280 px viewport an A4 page at 175% is
  // narrower than the pane.
  const defaultPercent = lastPercent
  expect(defaultPercent).toBe('175%')

  await v.getByRole('button', { name: 'Zoom in' }).click()
  await expect(percent).not.toHaveText(defaultPercent!)

  await v.getByRole('button', { name: 'Zoom out' }).click()
  await v.getByRole('button', { name: 'Zoom out' }).click()
  await expect(percent).not.toHaveText(defaultPercent!)

  // Fit width fills the pane — wider than the default here.
  await v.getByRole('button', { name: 'Fit width' }).click()
  await expect(v.getByRole('button', { name: 'Fit width' })).toHaveAttribute('aria-pressed', 'true')
  await expect.poll(async () => parseInt((await percent.textContent()) ?? '0', 10)).toBeGreaterThan(175)

  await page.keyboard.press('Control+0')
  await expect(percent).toHaveText(defaultPercent!)

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

// Final review R2: I3's fix (shipping wasm/cMap/standard-font assets and
// wiring them into getDocument) was only checked with a screenshot and a
// scratch probe, both one-off. pdf.js decodes ScannedPage's CCITT scan
// (internal/mmfake/pdf.go) through jbig2.wasm and, per I3, silently leaves
// the image out — no rejection, no fallback to the card — if that wiring
// ever breaks (a path change, the copy plugin not running in some build).
// So this samples actual rendered pixels, not just "no error was thrown".
test('the scanned page (3, CCITT) actually paints, not silently blank', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  await feed(page).getByRole('button', { name: 'View manual.pdf' }).click()
  const v = viewer(page)
  const pageCounter = v.getByText(/^\d+ \/ 50$/)
  await expect(pageCounter).toHaveText('1 / 50')

  // Same real-geometry scroll technique as the landscape-page test above
  // (PDF final review M3): page 3's own placeholder div exists from the
  // start (PdfView.tsx renders one per page up front; only the canvas
  // inside is windowed), so this can jump straight to it.
  const scroller = v.locator('.overflow-auto')
  await expect(async () => {
    await scroller.evaluate((el) => {
      const p3 = el.querySelector('[data-page="3"]')
      if (!(p3 instanceof HTMLElement)) throw new Error('page 3 is not in the DOM yet')
      el.scrollTop += p3.getBoundingClientRect().top - el.getBoundingClientRect().top
    })
    await expect(pageCounter).toHaveText('3 / 50')
  }).toPass()

  const canvas = v.locator('[data-page="3"] canvas')
  await expect(canvas).toBeVisible({ timeout: 15_000 })

  // internal/mmfake/pdf.go's manualPage (used for every non-landscape page,
  // including ScannedPage) draws its picture with
  // "q 480 0 0 360 57 40 cm /Im1 Do Q" on a 595x842 portrait page, inside a
  // decorative frame stroked at "50 150 495 300 re S" — that frame's own
  // bottom edge (a thin line at PDF y=150) falls *inside* the image's own y
  // range [40,400]. A correct render draws the (opaque) image over that
  // edge and hides it; a broken one (the image skipped) leaves the bare
  // line exposed — sampling the *whole* image box would then still show a
  // sliver of "dark" pixels from that line alone, only barely below a loose
  // threshold and not a reliable signal (confirmed empirically: a
  // wasmUrl-pointed-nowhere mutant still passed a first draft of this test
  // that used the full box). So this crops a tighter PDF-space rectangle,
  // x:[65,530] y:[155,295], chosen by rendering the real fixture with
  // poppler (pdftoppm): it sits inside "SCAN"'s own glyph area (y roughly
  // 171-263) with margin, and safely above the y=150 frame line. Measured
  // there directly against poppler: ~9.2% dark when the image renders,
  // exactly 0% when the same page has its image-draw operator stripped
  // (simulating the image being skipped) — a wide, clean margin, unlike the
  // full-box crop's 3.5% vs 0.6%. Mapped to a fraction of the canvas's own
  // backing store — pdf.js sizes the canvas to the full page, so this holds
  // at any zoom/DPR/CANVAS_PIXEL_CAP without guessing the actual render
  // scale.
  const stats = await canvas.evaluate((el) => {
    const c = el as HTMLCanvasElement
    const ctx = c.getContext('2d')!
    const fx0 = 65 / 595
    const fx1 = 530 / 595
    const fy0 = (842 - 295) / 842
    const fy1 = (842 - 155) / 842
    const x = Math.round(fx0 * c.width)
    const y = Math.round(fy0 * c.height)
    const w = Math.round((fx1 - fx0) * c.width)
    const h = Math.round((fy1 - fy0) * c.height)
    const { data } = ctx.getImageData(x, y, w, h)
    let dark = 0
    let light = 0
    for (let i = 0; i < data.length; i += 4) {
      const lum = 0.299 * data[i] + 0.587 * data[i + 1] + 0.114 * data[i + 2]
      if (lum < 128) dark++
      else light++
    }
    return { darkFraction: dark / (dark + light), lightFraction: light / (dark + light) }
  })
  // A real render: black "SCAN" strokes on white, ~9% dark in this crop
  // (measured against a poppler reference render of the real fixture —
  // R1/R2). Broken wasm wiring: pdf.js skips the image and this crop (well
  // inside its bounds, clear of the frame's own line) stays exactly white.
  expect(stats.darkFraction, `dark fraction ${stats.darkFraction}`).toBeGreaterThan(0.02)
  expect(stats.darkFraction).toBeLessThan(0.3)
  expect(stats.lightFraction).toBeGreaterThan(0.6)

  await page.keyboard.press('Escape')
  await expect(v).toHaveCount(0)
})

// pdf-lag report (2026-09-30): WebKitGTK repaints the page box on every
// wheel-scroll step, and a blurred box-shadow on it made each step cost
// 125–200 ms (janky scrolling of the everyday one-page receipt). Chromium
// can't show the jank, but the page box must not carry a shadow at all.
test('receipt (one page, vector text): the page box has no box-shadow', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /^bob/).click()
  await feed(page).getByRole('button', { name: 'View Receipt-1234-5678-9012.pdf' }).click()
  const v = viewer(page)
  await expect(v.getByText(/^\d+ \/ \d+$/)).toHaveText('1 / 1')
  await expect(v.locator('[data-page="1"] .pdf-text-layer span').first()).toBeAttached({ timeout: 15_000 })
  expect(await v.locator('[data-page="1"]').evaluate((el) => getComputedStyle(el).boxShadow)).toBe('none')

  await page.keyboard.press('Escape')
  await expect(v).toHaveCount(0)
})

// pdf-lag report (2026-09-30): fitting every page to the pane opened a
// Letter receipt at 301% on a 1920 px window. The default is the webapp's
// zoom (1.75: a 612 pt page is 1071 CSS px wide), centred, capped so the
// page never exceeds the pane; Fit width is an explicit mode.
test('receipt: opens at the webapp\'s 175%, centred; a narrow window caps it at the pane width', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /^bob/).click()
  await feed(page).getByRole('button', { name: 'View Receipt-1234-5678-9012.pdf' }).click()
  const v = viewer(page)
  await expect(v.getByText(/^\d+ \/ \d+$/)).toHaveText('1 / 1')
  // The toolbar's own percentage (right after "Zoom out") — the receipt's
  // text layer has "0%" spans of its own.
  const percent = v.getByRole('button', { name: 'Zoom out' }).locator('xpath=following-sibling::span[1]')
  await expect(percent).toHaveText('175%')
  const box = v.locator('[data-page="1"]')
  const scroller = v.locator('.overflow-auto')
  const geo = async () =>
    box.evaluate((el) => {
      const b = el.getBoundingClientRect()
      const sc = el.parentElement!
      const s = sc.getBoundingClientRect()
      return { w: Math.round(b.width), left: Math.round(b.left - s.left), right: Math.round(s.left + sc.clientWidth - b.right) }
    })
  const wide = await geo()
  expect(wide.w).toBe(1071)
  expect(Math.abs(wide.left - wide.right)).toBeLessThanOrEqual(2) // centred

  await page.setViewportSize({ width: 800, height: 700 })
  await expect.poll(async () => parseInt((await percent.textContent()) ?? '0', 10)).toBeLessThan(175)
  await expect.poll(async () => (await geo()).w).toBeLessThan(800)
  await expect.poll(() => scroller.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)

  await page.keyboard.press('Escape')
  await expect(v).toHaveCount(0)
})

// User request 2026-09-30: a click on the empty dark area around the page
// closes the viewer (like the image viewer); a click on the page, or a
// text-selection drag from the page onto the dark area, does not.
test('receipt: a click on the dark area closes the viewer; a click or selection drag on the page does not', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /^bob/).click()
  await feed(page).getByRole('button', { name: 'View Receipt-1234-5678-9012.pdf' }).click()
  const v = viewer(page)
  await expect(v.getByText(/^\d+ \/ \d+$/)).toHaveText('1 / 1')
  const span = v.locator('[data-page="1"] .pdf-text-layer span', { hasText: 'Receipt' }).first()
  await expect(span).toBeVisible({ timeout: 15_000 })
  const pageBox = (await v.locator('[data-page="1"]').boundingBox())!

  // A plain click on the page (its text).
  const sb = (await span.boundingBox())!
  await page.mouse.click(sb.x + sb.width / 2, sb.y + sb.height / 2)
  await expect(v).toBeVisible()
  // A selection drag from the page's text out onto the dark area left of it.
  await page.mouse.move(sb.x + sb.width - 2, sb.y + sb.height / 2)
  await page.mouse.down()
  await page.mouse.move(pageBox.x - 20, sb.y + sb.height / 2, { steps: 5 })
  await page.mouse.up()
  expect((await page.evaluate(() => window.getSelection()?.toString() ?? '')).length).toBeGreaterThan(0)
  await expect(v).toBeVisible()

  // A plain click on the dark area left of the page.
  await page.mouse.click(pageBox.x - 20, pageBox.y + 200)
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
