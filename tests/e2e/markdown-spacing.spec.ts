import { expect, test, type Locator } from '@playwright/test'
import { fakePost, feed, removeServerFromMenu, repliesLink, signInAlice, threadFeed } from './helpers'

async function expectParagraphRhythm(markdown: Locator) {
  await expect(markdown.locator(':scope > p')).toHaveCount(3)
  const metrics = await markdown.evaluate((el) => {
    const paragraphs = [...el.querySelectorAll(':scope > p')]
    const boxes = paragraphs.map((p) => p.getBoundingClientRect())
    return {
      gap: boxes[1].top - boxes[0].bottom,
      lineHeight: parseFloat(getComputedStyle(paragraphs[0]).lineHeight),
      firstHeight: boxes[0].height,
      firstMargin: getComputedStyle(paragraphs[0]).marginTop,
      lastMargin: getComputedStyle(paragraphs[2]).marginBottom,
      breaks: paragraphs[0].querySelectorAll('br').length,
    }
  })
  expect(metrics.gap).toBe(8)
  expect(metrics.breaks).toBe(1)
  expect(metrics.firstHeight).toBe(metrics.lineHeight * 2)
  expect(metrics.firstMargin).toBe('0px')
  expect(metrics.lastMargin).toBe('0px')
}

test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
})

test('blank lines separate message paragraphs in preview, channel and thread', async ({ page }) => {
  await page.setViewportSize({ width: 1376, height: 900 })
  await signInAlice(page)
  const raw = 'Диагностика PostgreSQL\nОкружение: Debian, PostgreSQL 12.7.\n\nПодтверждённый стек обоих процессов: **NFS lookup**. Проверяем состояние процессов и результаты диагностики.\n\nВлияние: очистка старых версий строк задерживается.\nСервер продолжает обслуживать остальные запросы.'
  const box = page.getByRole('textbox', { name: 'Message' })
  await box.fill(raw)
  await page.getByRole('button', { name: 'Show preview' }).click()
  await expectParagraphRhythm(page.getByRole('region', { name: 'Message preview' }).locator('.md'))
  await page.getByRole('button', { name: 'Back to editing' }).click()
  await box.fill('')

  const id = await fakePost(page, 'c-town', 'bob', raw)
  const post = feed(page).locator(`article[data-post-id="${id}"]`)
  await expectParagraphRhythm(post.locator('.md'))
  await fakePost(page, 'c-town', 'carol', 'Короткий ответ.', id)
  await repliesLink(page, 'Диагностика PostgreSQL', 1).click()
  await expectParagraphRhythm(threadFeed(page).locator(`article[data-post-id="${id}"] .md`))
  const short = threadFeed(page).locator('.md', { hasText: 'Короткий ответ.' })
  await expect(short).toBeVisible()
  await expect(short.locator('p')).toHaveCSS('margin-top', '0px')
  await expect(short.locator('p')).toHaveCSS('margin-bottom', '0px')
  await page.screenshot({ path: process.env.PARAGRAPH_SCREENSHOT ?? test.info().outputPath('paragraphs.png'), fullPage: true })
})
