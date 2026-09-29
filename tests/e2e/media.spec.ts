import { expect, test } from '@playwright/test'
import { existsSync } from 'node:fs'
import { channel, fakePost, fakeURL, feed, removeServerFromMenu, signInAlice, testGet, testPost, unique } from './helpers'

const naturalWidth = (img: import('@playwright/test').Locator) => img.evaluate((i: HTMLImageElement) => i.naturalWidth)

// A failed test must not leave its server behind (chat.spec.ts rule): without
// this, a test that fails before its own removeServerFromMenu() cascades
// into the next spec file's first test (PDF final review I1 — this is what
// the reactions-test regression below actually looked like before its fix).
test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
})

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

  // Download feedback is on the card itself (no banner): "Show in folder"
  // appears once saved, and reveals the saved file.
  await feed(page).getByRole('button', { name: 'Download spec.pdf' }).click()
  const reveal = feed(page).getByRole('button', { name: 'Show spec.pdf in folder' })
  await expect(reveal).toBeVisible()
  await reveal.click()
  let savedPath = ''
  await expect
    .poll(async () => (savedPath = ((await testGet(page, 'revealed-files')) as string[]).find((p) => p.endsWith('spec.pdf')) ?? ''))
    .not.toBe('')
  expect(existsSync(savedPath), `downloaded file missing on disk: ${savedPath}`).toBe(true)
  await feed(page).getByRole('button', { name: 'Open server.log' }).click()
  await expect
    .poll(async () => ((await testGet(page, 'opened-files')) as string[]).some((p) => p.endsWith('server.log')))
    .toBe(true)
  await removeServerFromMenu(page)
})

// file-cards brief (2026-09-30): the card's own name/icon area is now the
// primary action — "View <name>" for a previewable kind (a click opens the
// in-app viewer, same as clicking the old dedicated Preview icon button
// used to), or a bare-name-labelled download for everything else (a click
// downloads directly, no separate click on the small download icon
// needed). The bare-name label is deliberate, not an oversight: it must
// never collide with the secondary DownloadButton's own "Download <name>"
// accessible name right next to it (a strict-mode Playwright lookup for
// exactly that name — see this file's other tests, e.g. "Download
// spec.pdf" — must keep matching exactly one button).
test('a card\'s own name/icon area: pdf opens the viewer, a non-previewable kind (zip) downloads directly', async ({ page }) => {
  await signInAlice(page)
  await channel(page, /Off-Topic/).click()

  // spec.pdf: previewable — the primary action is "View", not a download.
  const pdfCard = feed(page).getByRole('button', { name: 'View spec.pdf' })
  await expect(pdfCard).toBeVisible()
  // Only the secondary button is named "Download spec.pdf" — the strict-mode
  // lookup below fails outright if the primary card ever regains that label.
  await expect(feed(page).getByRole('button', { name: 'Download spec.pdf' })).toBeVisible()

  // A fresh zip (not stuck-report/quarterly-report — other specs' own files,
  // and this fake's downloads dir is shared for the whole run): non-previewable,
  // so its card has no "View" action at all, and its own name/icon area is
  // labelled by the bare file name — clicking it downloads directly.
  const fake = await fakeURL(page)
  const login = await page.request.post(`${fake}/api/v4/users/login`, { data: { login_id: 'alice', password: 'secret' } })
  expect(login.ok(), await login.text()).toBeTruthy()
  const auth = { Authorization: `Bearer ${login.headers()['token']}` }
  const up = await page.request.post(`${fake}/api/v4/files?channel_id=c-offtopic&filename=card-click-archive.zip`, {
    headers: { ...auth, 'Content-Type': 'application/zip' },
    data: Buffer.alloc(4096, 7),
  })
  expect(up.ok(), await up.text()).toBeTruthy()
  const fileId = ((await up.json()).file_infos[0] as { id: string }).id
  const post = await page.request.post(`${fake}/api/v4/posts`, {
    headers: auth,
    data: { channel_id: 'c-offtopic', message: 'card click zip test', file_ids: [fileId] },
  })
  expect(post.ok(), await post.text()).toBeTruthy()

  // A card's own name button is never ALSO labelled "View" for a
  // non-previewable kind — exact:true above already proves its accessible
  // name is exactly the bare file name, nothing more.
  const zipCard = feed(page).getByRole('button', { name: 'card-click-archive.zip', exact: true })
  await expect(zipCard).toBeVisible()
  await zipCard.click()
  const reveal = feed(page).getByRole('button', { name: 'Show card-click-archive.zip in folder' })
  await expect(reveal).toBeVisible()
  // Clicking the card only downloads — "Show in folder" is a separate,
  // user-driven action, never auto-triggered by the download completing.
  expect(((await testGet(page, 'revealed-files')) as string[]).some((p) => p.endsWith('card-click-archive.zip'))).toBe(false)
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
  await expect(feed(page).getByRole('button', { name: 'Show big.log in folder' })).toBeVisible()

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
  // A post the test creates itself, not the seeded "Welcome to off-topic"
  // root (PDF final review I1): Off-Topic's history has grown past what the
  // virtualized feed keeps rendered while sitting at the bottom, so the
  // welcome post can fall outside the rendered window and this locator would
  // never resolve — deterministically, not as a flake. A fresh post lands at
  // the bottom, right where the feed already is.
  //
  // In Town Square (c-town, signInAlice's own landing channel), not
  // Off-Topic: Off-Topic's real membership (internal/mmfake/seed.go's
  // `add("c-offtopic", ...)`) is only alice and bob — carol only ever
  // appears there via seed-time data fabrication that bypasses the live
  // membership check, so fake/react as carol on a *live* post fails
  // reactLocked's isMemberLocked with a real (and initially confusing:
  // caught the hard way, by running this test) 403. Town Square's seeded
  // membership includes carol too, giving two independent non-alice
  // reactors to set up a starting "👍 2" that alice hasn't reacted to yet.
  const text = unique('reactions test post')
  await fakePost(page, 'c-town', 'bob', text)
  await testPost(page, 'fake/react', { channel_id: 'c-town', message: text, username: 'bob', emoji: '+1' })
  await testPost(page, 'fake/react', { channel_id: 'c-town', message: text, username: 'carol', emoji: '+1' })
  await testPost(page, 'fake/react', { channel_id: 'c-town', message: text, username: 'bob', emoji: 'partyparrot' })
  const post = feed(page).locator('article', { hasText: text })
  await expect(post.getByRole('button', { name: '👍 2' })).toBeVisible()
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

  await testPost(page, 'fake/react', { channel_id: 'c-town', message: text, username: 'bob', emoji: 'fire' })
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

  // this post is fresh to this test run (nothing else references it), so
  // there's no shared state to restore — still exercise removal, proving it
  // reaches the server too, not just the optimistic UI.
  await post.getByRole('button', { name: '🥑 1, you reacted' }).click()
  await expect(post.getByRole('button', { name: /🥑/ })).toHaveCount(0)
  await testPost(page, 'fake/react', { channel_id: 'c-town', message: text, username: 'bob', emoji: 'fire', remove: true })
  await expect(post.getByRole('button', { name: /🔥/ })).toHaveCount(0)
  await removeServerFromMenu(page)
})
