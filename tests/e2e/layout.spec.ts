import { expect, test } from '@playwright/test'
import { apiCall, channel, removeServerFromMenu, repliesLink, seedThread, signInAlice, threadPane } from './helpers'

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

  // The built-in default (256px), not whatever the sidebar currently is —
  // fix round 3 (coordinator review): a previous version passed the current
  // width itself as the "default", making this a no-op.
  await sep.dblclick()
  await expect(sep).toHaveAttribute('aria-valuenow', '256')

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
  await repliesLink(page, 'layout root', 1).click()
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
  await repliesLink(page, 'layout root', 1).click()
  await expect(threadPane(page)).toHaveJSProperty('offsetWidth', Math.round(before) - 60)
  await removeServerFromMenu(page)
})

// noOverflow: document.scrollingElement.scrollWidth must never exceed
// clientWidth -- a flex item missing min-width:0 (ChannelPane's own bug,
// 2026-09-29) refuses to shrink past its intrinsic content width once a
// splitter grows far enough, so the row overflows the actual window instead
// of the feed shrinking.
async function noOverflow(page: Parameters<typeof signInAlice>[0]) {
  const { scrollWidth, clientWidth } = await page.evaluate(() => ({
    scrollWidth: document.scrollingElement!.scrollWidth,
    clientWidth: document.scrollingElement!.clientWidth,
  }))
  expect(scrollWidth).toBe(clientWidth)
}

// Splitter-drag bug (coordinator report, 2026-09-29, real WebKitGTK desktop
// build): dragging the feed|thread-panel splitter to widen the panel froze
// the splitter line in place while the panel's content shifted right and
// clipped at the window's edge -- ChannelPane (the feed) was missing
// min-w-0, so it refused to shrink below its intrinsic content width
// (~448px in that repro) once the thread panel grew past the point where
// there was still "naturally" enough room, and the row overflowed the
// window instead. Root-caused via the WebKit inspector (Runtime.evaluate
// over its remote-debugging socket) and a real XTest pointer drag on a
// throwaway Xvfb display -- this Chromium e2e drags to the actual clamped
// maximum (not a small, safe delta) so it exercises the same overflow
// regardless of engine. Covers both splitters, both drag directions.
test('dragging either splitter to its max never overflows the window (splitter-drag bug, 2026-09-29)', async ({ page }) => {
  await signInAlice(page)
  await noOverflow(page)

  // Sidebar to its max, by keyboard (deterministic — no drag-distance guessing).
  const sidebarSep = page.getByRole('separator', { name: 'Resize sidebar' })
  await sidebarSep.focus()
  await page.keyboard.press('End')
  const sidebarMax = await sidebarSep.getAttribute('aria-valuemax')
  await expect(sidebarSep).toHaveAttribute('aria-valuenow', sidebarMax!)
  await noOverflow(page)

  // Open the thread panel and drag its splitter far to the left (widen it)
  // — the original report's exact scenario. The drag distance deliberately
  // overshoots any legitimate width increase: the clamp, not the drag
  // distance, must be what stops it, and even right at the clamped max
  // nothing may overflow.
  await seedThread(page, 'overflow check root', ['overflow check reply'])
  await channel(page, /Town Square/).click()
  // By the root's own text: "layout root" (the test above, same fake, same
  // channel) also shows "Replies: 1" once this root's post event lands.
  await repliesLink(page, 'overflow check root', 1).click()
  const pane = threadPane(page)
  await expect(pane).toBeVisible()

  const threadSep = page.getByRole('separator', { name: 'Resize thread panel' })
  const box = (await threadSep.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2 - 1000, box.y + box.height / 2, { steps: 20 })
  await page.mouse.up()

  const threadMax = await threadSep.getAttribute('aria-valuemax')
  await expect(threadSep).toHaveAttribute('aria-valuenow', threadMax!)
  await noOverflow(page)

  // The splitter must have visibly followed the pointer (not frozen in
  // place, the bug's other symptom): the panel's right edge sits exactly on
  // the window's right edge, not past it.
  const paneBox = (await pane.boundingBox())!
  const viewport = page.viewportSize()!
  expect(Math.round(paneBox.x + paneBox.width)).toBe(viewport.width)

  await removeServerFromMenu(page)
})
