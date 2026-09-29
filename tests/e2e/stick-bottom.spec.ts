import { expect, test, type Page } from '@playwright/test'
import { chmodSync, existsSync, rmSync } from 'node:fs'
import { dirname } from 'node:path'
import { channel, fakeURL, feed, removeServerFromMenu, signInAlice, testGet } from './helpers'

// Bottom-stick on a viewport resize: a feed sitting at its bottom stays there
// when its own height changes for a reason that is not a new row — a download
// banner above it (removed 2026-09-29), the composer growing with a multi-line
// draft, the window shrinking. Before the fix only a rows change re-pinned:
// shrinking the scroller keeps scrollTop, fires no scroll event, and the last
// post slid under the composer.

let seeded = false // the fake lives for the whole run (workers: 1)
const shots = process.env.STICK_SHOT_DIR // optional: where to write the screenshots

// Secret (alice is its only member) gets a screenful of text posts and, last,
// a post with a plain file card — all by alice, so nothing is unread and the
// channel opens at its end.
async function seed(page: Page) {
  if (seeded) return
  seeded = true
  const base = await fakeURL(page)
  const login = await page.request.post(`${base}/api/v4/users/login`, { data: { login_id: 'alice', password: 'secret' } })
  expect(login.ok()).toBeTruthy()
  const auth = { Authorization: `Bearer ${login.headers()['token']}` }
  for (let i = 0; i < 30; i++) {
    const r = await page.request.post(`${base}/api/v4/posts`, { headers: auth, data: { channel_id: 'c-secret', message: `stick filler ${i}` } })
    expect(r.ok()).toBeTruthy()
  }
  const up = await page.request.post(`${base}/api/v4/files?channel_id=c-secret&filename=stick-report.zip`, {
    headers: { ...auth, 'Content-Type': 'application/zip' },
    data: Buffer.alloc(64 * 1024, 7),
  })
  expect(up.ok(), await up.text()).toBeTruthy()
  const fileId = (await up.json()).file_infos[0].id
  const post = await page.request.post(`${base}/api/v4/posts`, {
    headers: auth,
    data: { channel_id: 'c-secret', message: 'stick last post with a file', file_ids: [fileId] },
  })
  expect(post.ok()).toBeTruthy()
}

async function openSecretAtBottom(page: Page) {
  await signInAlice(page)
  await seed(page)
  await channel(page, /Secret/).click()
  await expect(page.getByRole('heading', { name: /Secret/ })).toBeVisible()
  await expect(feed(page).getByText('stick last post with a file')).toBeVisible()
  await page.waitForFunction(() => {
    const el = document.querySelector('[role=log][data-feed=channel]')!
    return el.scrollHeight > el.clientHeight + 200 && el.scrollHeight - el.scrollTop - el.clientHeight < 2
  })
  // Let the opening's own rows updates (several arrive right after sign-in)
  // settle: a late one re-pins the feed and would hide a missed resize.
  await page.waitForTimeout(800)
}

// How far the last post's bottom edge sits below the feed's visible bottom
// (<= 0: fully visible), and the feed's distance from its scroll end.
const lastPostOverflow = (page: Page) =>
  feed(page).evaluate((el) => {
    const posts = el.querySelectorAll('[data-kind="post"]')
    const last = posts[posts.length - 1] as HTMLElement
    return {
      below: Math.round(last.getBoundingClientRect().bottom - el.getBoundingClientRect().bottom),
      distance: Math.round(el.scrollHeight - el.scrollTop - el.clientHeight),
      text: last.textContent ?? '',
    }
  })

// Download feedback lives on the file's own card — no banner above the feed
// (it pushed the conversation down: the same user report). A failure is a
// floating toast, out of the layout flow.
test('downloading the last post\'s attachment: feedback on the card, an error toast, and the last post stays fully visible', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  await openSecretAtBottom(page)
  const download = feed(page).getByRole('button', { name: 'Download stick-report.zip' })
  await download.click()
  const reveal = feed(page).getByRole('button', { name: 'Show stick-report.zip in folder' })
  await expect(reveal).toBeVisible()
  await expect(reveal).toHaveAttribute('title', 'Show in folder')
  await expect(page.getByText(/^Saved to/)).toHaveCount(0) // no banner
  if (shots) await feed(page).locator('[data-kind="post"]').last().screenshot({ path: `${shots}/stick-card-saved.png` })
  let o = await lastPostOverflow(page)
  expect(o.text).toContain('stick last post with a file')
  expect(o.below, JSON.stringify(o)).toBeLessThanOrEqual(0)
  expect(o.distance, JSON.stringify(o)).toBeLessThan(2)

  // "Show in folder" reveals the saved file (the fake opener records it).
  await reveal.click()
  let saved = ''
  await expect
    .poll(async () => (saved = ((await testGet(page, 'revealed-files')) as string[]).find((p) => p.endsWith('stick-report.zip')) ?? ''))
    .not.toBe('')
  expect(existsSync(saved), `downloaded file missing on disk: ${saved}`).toBe(true)

  // A failed download: a toast. The copy saved this session is gone (Go
  // would hand it back without fetching) and the downloads directory is
  // read-only, so the new copy cannot be written.
  const dir = dirname(saved)
  rmSync(saved)
  chmodSync(dir, 0o500)
  try {
    await download.click()
    const toast = page.getByTestId('toast-region')
    await expect(toast).toContainText('Could not download stick-report.zip')
    await expect(toast).toHaveAttribute('aria-live', 'polite')
    if (shots) await page.screenshot({ path: `${shots}/stick-error-toast.png` })
    o = await lastPostOverflow(page)
    expect(o.below, JSON.stringify(o)).toBeLessThanOrEqual(0)
    expect(o.distance, JSON.stringify(o)).toBeLessThan(2)
    await toast.getByRole('button', { name: 'Dismiss' }).click()
    await expect(toast).toBeEmpty()
  } finally {
    chmodSync(dir, 0o700)
  }
  await removeServerFromMenu(page)
})

test('at the bottom, a composer growing with a multi-line draft keeps the last post fully visible', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  await openSecretAtBottom(page)
  const box = page.getByRole('textbox', { name: 'Message' })
  await box.click()
  const before = (await box.boundingBox())!.height
  for (let i = 0; i < 5; i++) {
    await page.keyboard.type(`line ${i}`)
    await page.keyboard.press('Shift+Enter')
  }
  await expect.poll(async () => (await box.boundingBox())!.height).toBeGreaterThan(before + 40)
  if (shots) await page.screenshot({ path: `${shots}/stick-composer.png` })
  const o = await lastPostOverflow(page)
  expect(o.below, JSON.stringify(o)).toBeLessThanOrEqual(0)
  expect(o.distance, JSON.stringify(o)).toBeLessThan(2)
  await box.fill('')
  await removeServerFromMenu(page)
})

test('at the bottom, a shorter window keeps the last post fully visible', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await openSecretAtBottom(page)
  await page.setViewportSize({ width: 1280, height: 600 })
  await expect.poll(async () => (await lastPostOverflow(page)).distance).toBeLessThan(2)
  const o = await lastPostOverflow(page)
  expect(o.below, JSON.stringify(o)).toBeLessThanOrEqual(0)
  await removeServerFromMenu(page)
})

test('away from the bottom, a composer growing keeps the top visible post where it was', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  await openSecretAtBottom(page)
  const log = feed(page)
  // Scroll away (retry: a rows update right after opening can undo it — AGENTS.md).
  await expect
    .poll(async () => {
      await log.evaluate((el) => (el.scrollTop = el.scrollHeight - el.clientHeight - 400))
      await page.waitForTimeout(100)
      return log.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight)
    })
    .toBeGreaterThan(300)
  const topOf = () =>
    log.evaluate((el) => {
      const view = el.getBoundingClientRect().top
      const rows = [...el.querySelectorAll<HTMLElement>('[data-kind="post"]')].filter((r) => r.getBoundingClientRect().bottom > view)
      rows.sort((a, b) => a.getBoundingClientRect().top - b.getBoundingClientRect().top)
      return { key: rows[0].dataset.key, offset: Math.round(rows[0].getBoundingClientRect().top - view) }
    })
  const before = await topOf()
  const box = page.getByRole('textbox', { name: 'Message' })
  await box.click()
  for (let i = 0; i < 5; i++) {
    await page.keyboard.type(`line ${i}`)
    await page.keyboard.press('Shift+Enter')
  }
  await page.waitForTimeout(200)
  expect(await topOf()).toEqual(before)
  await box.fill('')
  await removeServerFromMenu(page)
})
