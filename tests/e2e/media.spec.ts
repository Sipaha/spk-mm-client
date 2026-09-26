import { expect, test } from '@playwright/test'
import { existsSync } from 'node:fs'
import { channel, feed, removeServerFromMenu, signInAlice, testGet, testPost } from './helpers'

const naturalWidth = (img: import('@playwright/test').Locator) => img.evaluate((i: HTMLImageElement) => i.naturalWidth)

test('avatars and presence in the sidebar and the feed', async ({ page }) => {
  await signInAlice(page)
  const bob = channel(page, /^bob, Online$/)
  await expect(bob).toBeVisible()
  const avatar = bob.locator('img')
  await expect(avatar).toHaveAttribute('src', /^\/media\/\d+\/avatar\/u-bob\?v=/)
  await expect.poll(() => naturalWidth(avatar)).toBeGreaterThan(0)
  await expect(feed(page).locator('article img[src*="/avatar/u-carol"]').first()).toBeVisible()

  await testPost(page, 'fake/status', { username: 'bob', status: 'dnd' })
  await channel(page, /Off-Topic/).click() // opening a channel polls statuses
  await expect(channel(page, /^bob, Do not disturb$/)).toBeVisible()
  await testPost(page, 'fake/status', { username: 'bob', status: 'online' })

  // the row's name follows bob's status: find it by the name's start from here on
  const picture = channel(page, /^bob,/).locator('img')
  const before = await picture.getAttribute('src')
  await testPost(page, 'fake/picture', { username: 'bob' })
  await expect(picture).not.toHaveAttribute('src', before!)
  await removeServerFromMenu(page)
})

test('image preview, viewer, text snippet, download and open', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  const shot = feed(page).getByRole('button', { name: 'View build.png' })
  await expect(shot).toBeVisible()
  const img = shot.locator('img')
  await expect(img).toHaveAttribute('width', '480')
  await expect(img).toHaveAttribute('height', '270')
  await expect.poll(() => naturalWidth(img)).toBe(960)
  await expect(feed(page).getByRole('button', { name: 'View flow.png' }).locator('img')).toHaveAttribute('src', /\/thumb\/f-diag1$/)
  await expect(feed(page).getByText('request #1 handled', { exact: false })).toBeVisible()

  await shot.click()
  const viewer = page.getByRole('dialog', { name: 'File viewer' })
  await expect(viewer.getByRole('img', { name: 'build.png' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(viewer).toHaveCount(0)

  await feed(page).getByRole('button', { name: 'Download spec.pdf' }).click()
  const savedNotice = page.getByText(/Saved to .*spec\.pdf/)
  await expect(savedNotice).toBeVisible()
  const savedPath = (await savedNotice.textContent())!.replace(/^Saved to /, '')
  expect(existsSync(savedPath), `downloaded file missing on disk: ${savedPath}`).toBe(true)
  await feed(page).getByRole('button', { name: 'Open server.log' }).click()
  await expect
    .poll(async () => ((await testGet(page, 'opened-files')) as string[]).some((p) => p.endsWith('server.log')))
    .toBe(true)
  await removeServerFromMenu(page)
})

test('text viewer: search finds a match beyond 64 KiB, with a counter', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  await feed(page).getByRole('button', { name: 'View big.log' }).click()
  const viewer = page.getByRole('dialog', { name: 'File viewer' })
  // big.log is ~1.3 MiB, over TextFullLimit (1 MiB): the viewer truncates it
  // at 1 MiB. worker #10000 sits at ~578 KiB in, past both the feed
  // snippet's 64 KiB cap and the truncation point — proving the viewer
  // searches the full ?full=1 text, not a short fragment.
  await viewer.getByRole('textbox', { name: 'Search in file' }).fill('worker #10000')
  await expect(viewer.getByText('1 of 1')).toBeVisible()
  await expect(viewer.locator('mark[data-current="true"]')).toHaveText('worker #10000')
  await viewer.getByRole('button', { name: 'Close' }).click()
  await removeServerFromMenu(page)
})

test('image viewer: wheel changes the zoom indicator', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  await feed(page).getByRole('button', { name: 'View build.png' }).click()
  const viewer = page.getByRole('dialog', { name: 'File viewer' })
  const percent = viewer.getByTestId('viewer-zoom-percent')
  await expect(percent).toHaveText('100 %')
  const img = viewer.getByRole('img', { name: 'build.png' })
  const box = (await img.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.wheel(0, -300) // deltaY < 0: zoom in (ImageZoom.tsx)
  await expect(percent).not.toHaveText('100 %')
  await viewer.getByRole('button', { name: 'Close' }).click()
  await removeServerFromMenu(page)
})

test('downloads panel: entry appears, "Show in folder" is recorded, "Remove from list" removes it', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  // big.log, not spec.pdf: spec.pdf is downloaded by the test above, and the
  // fake's downloads directory is shared across this whole file (one app
  // instance, one DB) — downloading it again here would land on a
  // deduplicated "spec (1).pdf" instead.
  await feed(page).getByRole('button', { name: 'Download big.log' }).click()
  await expect(page.getByText(/Saved to .*big\.log/)).toBeVisible()

  // The button's accessible name carries the active-download count ("Downloads"
  // vs "Downloads — active: N", AGENTS.md) — match the stable prefix, not an
  // exact string that a still-active download would miss.
  await page.getByRole('button', { name: /^Downloads/ }).click()
  const panel = page.getByRole('dialog', { name: 'Downloads list' })
  const row = panel.getByRole('listitem').filter({ hasText: 'big.log' })
  await expect(row).toBeVisible()

  await row.getByRole('button', { name: 'Show big.log in folder' }).click()
  await expect
    .poll(async () => ((await testGet(page, 'revealed-files')) as string[]).some((p) => p.endsWith('big.log')))
    .toBe(true)

  await row.getByRole('button', { name: 'Remove big.log from the list' }).click()
  await expect(panel.getByRole('listitem').filter({ hasText: 'big.log' })).toHaveCount(0)

  await page.keyboard.press('Escape')
  await expect(panel).toHaveCount(0)
  await removeServerFromMenu(page)
})

test('video, audio and markdown previews: smoke', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()

  // markdown: rendered fragment in the feed, and Rendered/Source toggle in the viewer
  await expect(feed(page).getByRole('heading', { name: 'spk-mm-client' })).toBeVisible()
  await feed(page).getByRole('button', { name: 'View README.md' }).click()
  const viewer = page.getByRole('dialog', { name: 'File viewer' })
  await expect(viewer.getByRole('heading', { name: 'spk-mm-client' })).toBeVisible()
  await viewer.getByRole('group', { name: 'Markdown view' }).getByRole('button', { name: 'Source' }).click()
  await expect(viewer.locator('pre')).toContainText('# spk-mm-client')
  await viewer.getByRole('button', { name: 'Close' }).click()

  // video: feed poster → click plays it. clip.webm (VP9/Opus), not clip.mp4
  // (H.264 may not have a decoder on this host) — AGENTS.md / task-6-brief.
  const playBtn = feed(page).getByRole('button', { name: 'Play clip.webm' })
  await expect(playBtn).toBeVisible()
  await playBtn.click()
  const video = feed(page).locator('video').first()
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.controls)).toBe(true)
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.paused), { timeout: 10_000 }).toBe(false)

  // audio: the compact player is present in the file's row
  await expect(feed(page).locator('audio[controls]')).toHaveCount(1)

  await removeServerFromMenu(page)
})

test('reactions: chips toggle, the picker adds, others arrive live', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()
  const post = feed(page).locator('article', { hasText: 'Welcome to off-topic' })
  await post.getByRole('button', { name: '👍 2' }).click()
  await expect(post.getByRole('button', { name: '👍 3, you reacted' })).toBeVisible()
  await post.getByRole('button', { name: '👍 3, you reacted' }).click()
  await expect(post.getByRole('button', { name: '👍 2' })).toBeVisible()

  await post.hover()
  await post.getByRole('toolbar').getByRole('button', { name: 'Add reaction' }).click()
  const picker = page.getByRole('dialog', { name: 'Emoji picker' })
  await picker.getByRole('textbox', { name: 'Search emoji' }).fill('avocado')
  await page.keyboard.press('Enter')
  await expect(picker).toHaveCount(0)
  await expect(post.getByRole('button', { name: '🥑 1, you reacted' })).toBeVisible()

  await testPost(page, 'fake/react', { channel_id: 'c-offtopic', message: 'Welcome to off-topic', username: 'bob', emoji: 'fire' })
  await expect(post.getByRole('button', { name: '🔥 1' })).toBeVisible()
  await expect(post.locator('img[src*="/emoji/partyparrot"]')).toBeVisible()

  // prove our own toggles actually reached the fake server, not just the optimistic UI:
  // fake/drop {lose:true} drops the dead-letter buffer too, forcing a full resync — the
  // channel's posts (reactions included) are rebuilt from the fake server over REST, so a
  // reaction that survives this is one the server actually has.
  await testPost(page, 'fake/drop', { lose: true })
  await expect(page.getByText('Offline — reconnecting…')).toHaveCount(0, { timeout: 15_000 })
  await expect(post.getByRole('button', { name: '🥑 1, you reacted' })).toBeVisible({ timeout: 15_000 })
  await expect(post.getByRole('button', { name: '🔥 1' })).toBeVisible()
  await expect(post.getByRole('button', { name: '👍 2' })).toBeVisible()

  // the fake is shared by all tests: put things back
  await post.getByRole('button', { name: '🥑 1, you reacted' }).click()
  await expect(post.getByRole('button', { name: /🥑/ })).toHaveCount(0)
  await testPost(page, 'fake/react', { channel_id: 'c-offtopic', message: 'Welcome to off-topic', username: 'bob', emoji: 'fire', remove: true })
  await expect(post.getByRole('button', { name: /🔥/ })).toHaveCount(0)
  await removeServerFromMenu(page)
})
