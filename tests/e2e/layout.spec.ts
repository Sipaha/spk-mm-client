import { expect, test } from '@playwright/test'
import { apiCall, channel, removeServerFromMenu, seedThread, signInAlice, threadPane } from './helpers'

// Theme brief 2026-09-28, scope 3a: resizable sidebar/thread-panel
// splitters, app-wide persistence via internal/api's GetLayout/
// SetSidebarWidth/SetThreadWidth (not per server).

test('dragging the sidebar splitter resizes it and the width survives a reload', async ({ page }) => {
  await signInAlice(page)
  const sidebar = page.getByRole('complementary', { name: 'Server channels' })
  const before = (await sidebar.boundingBox())!.width

  const sep = page.getByRole('separator', { name: 'Resize sidebar' })
  await expect(sep).toHaveAttribute('aria-orientation', 'vertical')
  const box = (await sep.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2 + 80, box.y + box.height / 2, { steps: 5 })
  await page.mouse.up()

  await expect(sidebar).toHaveJSProperty('offsetWidth', Math.round(before) + 80)

  await page.reload()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await expect(sidebar).toHaveJSProperty('offsetWidth', Math.round(before) + 80)
  await removeServerFromMenu(page)
})

test('keyboard steps and double-click reset the sidebar splitter', async ({ page }) => {
  await signInAlice(page)
  const sep = page.getByRole('separator', { name: 'Resize sidebar' })
  await sep.focus()
  const initial = Number(await sep.getAttribute('aria-valuenow'))
  await page.keyboard.press('ArrowRight')
  await expect(sep).toHaveAttribute('aria-valuenow', String(initial + 16))
  await page.keyboard.press('ArrowLeft')
  await expect(sep).toHaveAttribute('aria-valuenow', String(initial))

  await sep.dblclick()
  await expect(sep).toHaveAttribute('aria-valuenow', String(initial)) // sidebar's "default" is its current width

  await page.keyboard.press('Home')
  await expect(sep).toHaveAttribute('aria-valuenow', await sep.getAttribute('aria-valuemin'))
  await page.keyboard.press('End')
  await expect(sep).toHaveAttribute('aria-valuenow', await sep.getAttribute('aria-valuemax'))
  await removeServerFromMenu(page)
})

test('dragging the thread-panel splitter resizes it', async ({ page }) => {
  await signInAlice(page)
  // The sidebar width is app-wide and may carry over from another test in
  // this file (e.g. the keyboard test above leaves it at its max via End) —
  // reset it so the thread panel's own room (and this test's fixed drag
  // delta) doesn't depend on run order.
  await apiCall(page, 'SetSidebarWidth', { width: 256 })
  await page.reload()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await seedThread(page, 'layout root', ['layout reply 1'])
  await channel(page, /Town Square/).click()
  await page.getByRole('button', { name: /^Replies: 1/ }).click()
  const pane = threadPane(page)
  await expect(pane).toBeVisible()
  const before = (await pane.boundingBox())!.width

  const sep = page.getByRole('separator', { name: 'Resize thread panel' })
  const box = (await sep.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  // The handle is the panel's left edge: dragging right shrinks it.
  await page.mouse.move(box.x + box.width / 2 + 60, box.y + box.height / 2, { steps: 5 })
  await page.mouse.up()

  await expect(pane).toHaveJSProperty('offsetWidth', Math.round(before) - 60)

  await page.reload()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await page.getByRole('button', { name: /^Replies: 1/ }).click()
  await expect(threadPane(page)).toHaveJSProperty('offsetWidth', Math.round(before) - 60)
  await removeServerFromMenu(page)
})
