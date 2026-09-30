import { expect, test } from '@playwright/test'
import { apiCall, channel, fakePost, fakeURL, removeServerFromMenu, repliesLink, seedThread, signInAlice, threadFeed, threadPane, unique } from './helpers'

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

// file-cards brief (2026-09-30): a card's fixed size (width 320px, min-width
// 204px, matching mm-10.11 webapp's own .post-image__column) sat one level
// too deep in FileCard.tsx's markup — the bordered "group" div, not the item
// the file-cards row (Attachments.tsx) actually lays out — so when the
// thread panel was dragged down to its own 320px minimum (splitter.ts's
// THREAD_MIN), the row's flex item had no width of its own and got sized,
// for shrink-to-fit purposes, by that inner div's *specified* 320px width —
// a percentage clamp (max-w-full) can't apply during that pass (no definite
// containing block yet) and was silently ignored, so two cards overflowed
// the thread panel's own internal virtualized-feed scroller sideways instead
// of wrapping onto separate lines. `noOverflow` above never caught this: it
// only checks document.scrollingElement, which never actually grew — the
// window stayed put while a scrollbar appeared *inside* the thread panel.
// Caught only by an actual screenshot at 320px (FileCard.tsx's own comment
// records the fix); this test pins it at the real layout-engine level no
// unit test can reach (jsdom has no layout engine).
test('two file cards in the thread panel at its 320px minimum wrap instead of overflowing (file-cards brief, 2026-09-30)', async ({ page }) => {
  await signInAlice(page)
  const fake = await fakeURL(page)
  const login = await page.request.post(`${fake}/api/v4/users/login`, { data: { login_id: 'alice', password: 'secret' } })
  expect(login.ok(), await login.text()).toBeTruthy()
  const auth = { Authorization: `Bearer ${login.headers()['token']}` }
  const upload = async (filename: string, contentType: string, data: Buffer) => {
    const r = await page.request.post(`${fake}/api/v4/files?channel_id=c-town&filename=${encodeURIComponent(filename)}`, {
      headers: { ...auth, 'Content-Type': contentType },
      data,
    })
    expect(r.ok(), await r.text()).toBeTruthy()
    return ((await r.json()).file_infos[0] as { id: string }).id
  }
  const rootText = unique('narrow panel file cards')
  const fileIds = await Promise.all([
    upload('quarterly-report-final.zip', 'application/zip', Buffer.alloc(58 * 1024, 7)),
    upload('deck.pptx', 'application/vnd.openxmlformats-officedocument.presentationml.presentation', Buffer.alloc(12 * 1024, 1)),
  ])
  const post = await page.request.post(`${fake}/api/v4/posts`, {
    headers: auth,
    data: { channel_id: 'c-town', message: rootText, file_ids: fileIds },
  })
  expect(post.ok(), await post.text()).toBeTruthy()
  await fakePost(page, 'c-town', 'bob', 'narrow panel reply', ((await post.json()) as { id: string }).id)

  await channel(page, /Town Square/).click()
  await repliesLink(page, rootText, 1).click()
  const pane = threadPane(page)
  await expect(pane).toBeVisible()

  const threadSep = page.getByRole('separator', { name: 'Resize thread panel' })
  await threadSep.focus()
  await page.keyboard.press('Home') // THREAD_MIN (splitter.ts) — the documented 320px floor
  await expect(pane).toHaveJSProperty('offsetWidth', 320)

  // exact: true — Playwright's default name match is a case-insensitive
  // substring, and "quarterly-report-final.zip" also matches the secondary
  // "Download quarterly-report-final.zip"/"Open quarterly-report-final.zip"
  // buttons right next to it (strict-mode violation without this).
  await expect(threadPane(page).getByRole('button', { name: 'quarterly-report-final.zip', exact: true })).toBeVisible()
  await expect(threadPane(page).getByRole('button', { name: 'deck.pptx', exact: true })).toBeVisible()

  const { scrollWidth, clientWidth } = await threadFeed(page).evaluate((el) => ({ scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }))
  expect(scrollWidth).toBe(clientWidth)

  await removeServerFromMenu(page)
})

test('fractional pointer coordinates save integer widths and survive a reload', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 })
  await signInAlice(page)
  await apiCall(page, 'SetSidebarWidth', { width: 256 })
  await apiCall(page, 'SetThreadWidth', { width: 420 })
  await page.reload()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  const root = unique('fractional resize')
  await seedThread(page, root, ['reply'])
  await repliesLink(page, root, 1).click()
  const resize = async (label: string, endX: number) => {
    await page.getByRole('separator', { name: label }).evaluate((el, x) => {
      el.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0, pointerId: 99, clientX: 800.25 }))
      el.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, button: 0, pointerId: 99, clientX: x }))
    }, endX)
  }
  await resize('Resize sidebar', 857.68) // 256 + 57.43 -> 313
  await resize('Resize thread panel', 682.2811279296875) // 420 + 117.9688720703125 -> 538
  await expect.poll(() => apiCall(page, 'GetLayout')).toMatchObject({ sidebar_width: 313, thread_width: 538 })
  await page.reload()
  await expect(page.getByRole('heading', { name: /Town Square/ })).toBeVisible()
  await repliesLink(page, root, 1).click()
  await expect(page.getByRole('complementary', { name: 'Server channels' })).toHaveJSProperty('offsetWidth', 313)
  await expect(threadPane(page)).toHaveJSProperty('offsetWidth', 538)
  await page.screenshot({ path: `${process.env.E2E_SHOTS ?? 'test-results'}/fractional-resize.png` })
  await removeServerFromMenu(page)
})
