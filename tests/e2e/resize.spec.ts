import { expect, test } from '@playwright/test'
import { screenshotDir, fakePost, feed, removeServerFromMenu, repliesLink, seedThread, signInAlice, threadFeed } from './helpers'

test.afterEach(async ({ page }) => { await removeServerFromMenu(page) })

test('reflow keeps mounted messages separate throughout pane and window resize', async ({ page }) => {
  await page.setViewportSize({ width: 1450, height: 900 })
  await signInAlice(page)
  const paragraph = 'Пользователи добавлены в группы. Проверяем синхронизацию и настройки приложения: длинное сообщение переносится при изменении ширины панели. '
  for (let i = 0; i < 20; i++) {
    await fakePost(page, 'c-town', i % 2 ? 'bob' : 'carol', `Resize ${i}\n\n${paragraph.repeat(3)}\n\n1. ${paragraph}\n2. https://example.com/dashboard?record=very-long-record-name-and-identifier`)
  }
  const root = 'Resize thread root'
  await seedThread(page, root, [paragraph.repeat(4), paragraph.repeat(3), paragraph.repeat(2)])
  await repliesLink(page, root, 3).click()
  await expect(threadFeed(page).getByText(paragraph.repeat(2), { exact: true })).toBeVisible()
  await page.evaluate(() => document.fonts.ready)
  await feed(page).evaluate(el => { el.scrollTop -= 500 })
  // Exercise the live CSS widths used by both splitters. Read real layout
  // in every intermediate width, not merely after pointerup/settling.
  const result = await page.evaluate(async () => {
    const errors: string[] = []
    const onError = (e: ErrorEvent) => { errors.push(e.message) }
    window.addEventListener('error', onError)
    let overlaps = 0
    let samples = 0
    let worst = 0
    const check = () => {
      for (const feed of document.querySelectorAll('[data-feed]')) {
        const viewport = feed.getBoundingClientRect()
        const rows = [...feed.querySelectorAll<HTMLElement>('[data-index]')].map(el => el.getBoundingClientRect())
        for (let i = 1; i < rows.length; i++) {
          if (rows[i].bottom < viewport.top || rows[i - 1].top > viewport.bottom) continue
          samples++
          const overlap = rows[i - 1].bottom - rows[i].top
          if (overlap > 1) { overlaps++; worst = Math.max(worst, overlap) }
        }
      }
    }
    for (let i = 0; i < 48; i++) {
      await new Promise<void>(resolve => requestAnimationFrame(() => {
        const t = i < 24 ? i / 23 : (47 - i) / 23
        document.documentElement.style.setProperty('--spk-thread-width', `${420 + 330 * t}px`)
        document.documentElement.style.setProperty('--spk-sidebar-width', `${256 + 60 * t}px`)
        check()
        resolve()
      }))
    }
    window.removeEventListener('error', onError)
    return { overlaps, samples, worst, errors }
  })
  expect(result.samples).toBeGreaterThan(100)
  expect(result, `intermediate resize frames: ${JSON.stringify(result)}`).toMatchObject({ overlaps: 0, worst: 0, errors: [] })
  for (const width of [1100, 1450, 1200]) {
    await page.setViewportSize({ width, height: 900 })
    await expect.poll(() => page.evaluate(() => [...document.querySelectorAll('[data-feed]')].every(el => {
      const rows = [...el.querySelectorAll('[data-index]')].map(row => row.getBoundingClientRect())
      return rows.every((r, i) => i === 0 || rows[i - 1].bottom <= r.top + 1)
    }))).toBe(true)
  }
  await page.screenshot({ path: `${screenshotDir}/resize-reflow.png` })
})
