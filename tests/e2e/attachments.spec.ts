import { expect, test, type Page } from '@playwright/test'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { channel, feed, removeServerFromMenu, signInAlice, testPost, unique } from './helpers'

// A real (small, valid) 64x64 PNG, used both as a pasted/dropped in-page
// File and as an on-disk fixture for the 📎 file-picker input. Big enough to
// actually show up in a screenshot — fitBox (Attachments.tsx) never
// upscales, so a 1x1 or 8x8 source image renders as a near-invisible dot.
const PNG_1PX_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAABOklEQVR4nO2a0RHCMAxDU45l2IWBGKy7MA585KcH59R2HCtxrV/A0osaypVs5fUuK+uGDtCrBEArAdBKALTuI4Z+ng/qpW03vu2YATRCU28zgTEAYEanPtiJ0QWgjv4/RI2h38Qm6funaRqwjf4zVlqFuIFB6dXzZQCj0ytcBAA+6aVeXADP9CLH5X9KsAD8l5/vew6ASs90j34JYZefkyF0AzMsf1UjSegGllBcgHk2QBWVJ24DqygB0EoAtBIALRLA/Clsp6g8cRtYRaEB5tkGjSShGyhzlNDOEL2Bgi7h1J3VAIqB43uBS6jKvwSmo6ABTwa+l+wS8mEQuYj3wGgG6XzN36zVw/zBkW5p9N9CtlWop3UdNTCpAnlW4phAgTHLaZWqY5olzwsd5XnHuMxPiWmVAGglAFpfPR9mbwpvJicAAAAASUVORK5CYII='

let fixtures: string
test.beforeAll(() => {
  fixtures = mkdtempSync(join(tmpdir(), 'spk-mm-e2e-attach-'))
  writeFileSync(join(fixtures, 'pick-me.png'), Buffer.from(PNG_1PX_BASE64, 'base64'))
  writeFileSync(join(fixtures, 'pick-me.bin'), Buffer.from([0x00, 0x01, 0x02, 0x03, ...Buffer.from('binary payload, not previewable')]))
})

// openOffTopic: Composer is keyed by channel id (a fresh instance per
// channel), so a raw DOM query for the textarea/drop-target right after
// clicking a channel button can still find the outgoing channel's node for
// a moment — waiting for the heading proves the new channel (and its
// Composer) actually mounted before pasteFiles/dropFiles below query the DOM.
async function openOffTopic(page: Page) {
  await channel(page, /Off-Topic/).click()
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
}

// pasteFiles/dropFiles build a real DataTransfer with real Files entirely
// in the page (Files can't cross the Playwright boundary) and dispatch a
// synthetic paste/drop the way a browser would — Composer.onPaste and
// ChannelPane.onDrop only ever look at clipboardData.files/dataTransfer.files
// (AGENTS.md: desktop never gets real file bytes this way, only browser
// mode does), so this exercises exactly the code path a real user's
// gesture would.
async function makeDataTransfer(page: Page, files: { base64: string; name: string; mime: string }[]) {
  return page.evaluateHandle((fs) => {
    const dt = new DataTransfer()
    for (const f of fs) {
      const bytes = Uint8Array.from(atob(f.base64), (c) => c.charCodeAt(0))
      dt.items.add(new File([bytes], f.name, { type: f.mime }))
    }
    return dt
  }, files)
}

async function pasteFiles(page: Page, files: { base64: string; name: string; mime: string }[]) {
  const dt = await makeDataTransfer(page, files)
  await page.evaluate(
    ([dt, selector]) => {
      const ta = document.querySelector(selector) as HTMLTextAreaElement
      ta.focus()
      const ev = new ClipboardEvent('paste', { bubbles: true, cancelable: true })
      Object.defineProperty(ev, 'clipboardData', { value: dt })
      ta.dispatchEvent(ev)
    },
    [dt, 'textarea[aria-label="Message"]'] as const,
  )
}

async function dropFiles(page: Page, files: { base64: string; name: string; mime: string }[]) {
  const dt = await makeDataTransfer(page, files)
  await page.evaluate(
    ([dt, selector]) => {
      const target = document.querySelector(selector) as HTMLElement
      const ev = new DragEvent('drop', { bubbles: true, cancelable: true })
      Object.defineProperty(ev, 'dataTransfer', { value: dt })
      target.dispatchEvent(ev)
    },
    [dt, '[data-file-drop-target]'] as const,
  )
}

const tray = (page: Page) => page.getByRole('list', { name: 'Attachments' })

test('paste an image: a chip appears, Enter sends a post with the image', async ({ page }) => {
  await signInAlice(page)
  await openOffTopic(page)
  const name = unique('shot') + '.png'
  await pasteFiles(page, [{ base64: PNG_1PX_BASE64, name, mime: 'image/png' }])
  await expect(tray(page).getByText(name)).toBeVisible()
  await page.screenshot({ path: 'test-results/attachments-paste-chip.png' })

  const text = unique('paste image post')
  await page.getByRole('textbox', { name: 'Message' }).fill(text)
  await page.keyboard.press('Enter')
  const post = feed(page).locator('article', { hasText: text })
  // getByRole('img', { name }) — not post.locator('img'), which also matches
  // the post's own avatar image.
  await expect(post.getByRole('img', { name })).toBeVisible()
  await expect(post.getByText('Sending…')).toHaveCount(0)
  await feed(page).evaluate((el) => { el.scrollTop = el.scrollHeight })
  await page.screenshot({ path: 'test-results/attachments-sent-post.png' })
  await removeServerFromMenu(page)
})

test('paste a non-image file: a file card appears in the sent post', async ({ page }) => {
  await signInAlice(page)
  await openOffTopic(page)
  const name = unique('doc') + '.bin'
  await pasteFiles(page, [{ base64: btoa('not an image'), name, mime: 'application/octet-stream' }])
  await expect(tray(page).getByText(name)).toBeVisible()

  const text = unique('paste file post')
  await page.getByRole('textbox', { name: 'Message' }).fill(text)
  await page.keyboard.press('Enter')
  const post = feed(page).locator('article', { hasText: text })
  await expect(post.getByRole('button', { name: `Download ${name}` })).toBeVisible()
  await removeServerFromMenu(page)
})

test('drag-and-drop a file over the channel: a chip appears', async ({ page }) => {
  await signInAlice(page)
  await openOffTopic(page)
  const name = unique('dropped') + '.png'
  await dropFiles(page, [{ base64: PNG_1PX_BASE64, name, mime: 'image/png' }])
  await expect(tray(page).getByText(name)).toBeVisible()
  await removeServerFromMenu(page)
})

test('📎 opens the file picker: a chip appears', async ({ page }) => {
  await signInAlice(page)
  await openOffTopic(page)
  await page.getByRole('button', { name: 'Attach files' }).click()
  await page.locator('input[type=file]').setInputFiles(join(fixtures, 'pick-me.png'))
  await expect(tray(page).getByText('pick-me.png')).toBeVisible()

  const text = unique('picked file post')
  await page.getByRole('textbox', { name: 'Message' }).fill(text)
  await page.keyboard.press('Enter')
  // getByRole with the file name: a bare locator('img') also matches the post's avatar.
  await expect(feed(page).locator('article', { hasText: text }).getByRole('img', { name: 'pick-me.png' })).toBeVisible()
  await removeServerFromMenu(page)
})

test('a failed upload shows Retry; retrying sends it', async ({ page }) => {
  await signInAlice(page)
  await openOffTopic(page)
  await testPost(page, 'fake/fail-uploads', { n: 1 })
  await page.locator('input[type=file]').setInputFiles(join(fixtures, 'pick-me.bin'))
  const chip = tray(page).getByRole('listitem').filter({ hasText: 'pick-me.bin' })
  await expect(chip.getByRole('alert')).toContainText('Error')
  await chip.getByRole('button', { name: /Retry/ }).click()
  await expect(chip.getByRole('alert')).toHaveCount(0)

  const text = unique('retried upload post')
  await page.getByRole('textbox', { name: 'Message' }).fill(text)
  await page.keyboard.press('Enter')
  const post = feed(page).locator('article', { hasText: text })
  await expect(post.getByRole('button', { name: 'Download pick-me.bin' })).toBeVisible()
  await removeServerFromMenu(page)
})

test('MaxFileSize limit: attaching an over-limit file shows an error, nothing is attached', async ({ page }) => {
  // Set the tiny limit before this server is even added, so the very first
  // bootstrap (at sign-in) already knows it — no resync/race needed, unlike
  // changing it under an already-connected session (AGENTS.md "Things that
  // bite": the client only sees a limit change after its next metadata
  // refresh, and until then a big file can still be staged locally and only
  // fail later, when the actual upload hits the fake's now-stricter limit).
  await page.goto('/')
  await testPost(page, 'fake/max-file-size', { bytes: 10 })
  try {
    await signInAlice(page)
    await openOffTopic(page)

    await page.locator('input[type=file]').setInputFiles(join(fixtures, 'pick-me.png')) // well over 10 bytes
    await expect(page.getByRole('alert').filter({ hasText: 'too large' })).toBeVisible()
    await expect(tray(page)).toHaveCount(0)
    await removeServerFromMenu(page)
  } finally {
    // Restore the default even when the test fails: later tests share the fake.
    await testPost(page, 'fake/max-file-size', { bytes: 0 })
  }
})

test('attachments only, no text, is a valid post', async ({ page }) => {
  await signInAlice(page)
  await openOffTopic(page)
  const name = unique('onlyfile') + '.png'
  await pasteFiles(page, [{ base64: PNG_1PX_BASE64, name, mime: 'image/png' }])
  await expect(tray(page).getByText(name)).toBeVisible()
  await page.keyboard.press('Enter') // empty textarea, attachment only
  await expect(tray(page)).toHaveCount(0) // the tray clears once the post is sent
  const img = feed(page).getByRole('img', { name })
  await expect(img).toBeVisible()
  // The server created it: no longer pending, and the picture is the post's
  // file (/feed or /thumb of its file id), not the staged attachment (/staged/<id>).
  const post = feed(page).locator('article').filter({ has: img })
  await expect(post.getByText('Sending…')).toHaveCount(0)
  await expect(img).toHaveAttribute('src', /\/(feed|thumb)\//)
  await removeServerFromMenu(page)
})
