import { expect, test } from '@playwright/test'
import { screenshotDir, apiCall, feed, removeServerFromMenu, repliesLink, seedThread, serverId, signInAlice, testPost, threadComposer, threadFeed, unique } from './helpers'

test.afterEach(async ({ page }) => {
  await testPost(page, 'fake/post-latency', { ms: 0 })
  await removeServerFromMenu(page)
})

for (const variant of ['channel', 'thread'] as const) {
  test(`${variant}: quick confirmation preserves message height; only a slow send shows feedback`, async ({ page }) => {
    await signInAlice(page)
    const id = await serverId(page)
    let rootId = ''
    if (variant === 'thread') {
      const root = unique('optimistic thread')
      rootId = (await seedThread(page, root, ['initial reply'])).rootId
      await repliesLink(page, root, 1).click()
    }
    const log = variant === 'thread' ? threadFeed(page) : feed(page)
    const composer = variant === 'thread' ? threadComposer(page) : page.getByRole('textbox', { name: 'Message', exact: true })
    const view = () => apiCall(page, variant === 'thread' ? 'GetThread' : 'GetChannel', variant === 'thread'
      ? { id, root_id: rootId } : { id, channel_id: 'c-town' }) as Promise<{ posts: { message: string; pending?: boolean }[] }>

    await testPost(page, 'fake/post-latency', { ms: 2000 })
    const text = unique('Отправленное сообщение без скачка')
    await composer.fill(text)
    await composer.press('Enter')
    const row = log.locator('article').filter({ hasText: text })
    await expect(row).toBeVisible()
    expect((await view()).posts.find(p => p.message === text)?.pending).toBe(true)
    await expect(row).toHaveCSS('opacity', '1')
    await expect(row.getByText('Sending…')).toHaveCount(0)
    const before = (await row.boundingBox())!.height
    await expect.poll(async () => (await view()).posts.find(p => p.message === text)?.pending ?? false).toBe(false)
    await expect(row).toHaveCount(1)
    await expect(row.getByText('Sending…')).toHaveCount(0)
    expect((await row.boundingBox())!.height).toBe(before)

    await testPost(page, 'fake/post-latency', { ms: 4500 })
    const slow = unique('Подтверждение задерживается')
    await composer.fill(slow)
    await composer.press('Enter')
    const slowRow = log.locator('article').filter({ hasText: slow })
    await expect(slowRow).toBeVisible()
    await expect(slowRow.getByText('Sending…')).toHaveCount(0)
    await page.screenshot({ path: `${screenshotDir}/optimistic-${variant}.png` })
    await expect(slowRow.getByRole('status')).toHaveText('Sending…', { timeout: 4000 })
    await expect(slowRow.getByRole('alert')).toHaveCount(0)
    await expect.poll(async () => (await view()).posts.find(p => p.message === slow)?.pending ?? false).toBe(false)
    await expect(slowRow.getByText('Sending…')).toHaveCount(0)
    await expect(slowRow).toHaveCount(1)
  })
}
