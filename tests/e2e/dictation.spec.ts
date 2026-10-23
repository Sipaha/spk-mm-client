import { expect, test } from '@playwright/test'
import { join } from 'node:path'
import { mkdirSync } from 'node:fs'
import { channel, feed, removeServerFromMenu, screenshotDir, seedThread, signInAlice, threadPane, unique } from './helpers'

const live = process.env.E2E_DICTATION_LIVE === '1'
const audioFile = process.env.E2E_DICTATION_AUDIO
// Chromium supplies a synthetic microphone; application capture/resampling stays real.
test.use({ launchOptions: { args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', ...(audioFile ? [`--use-file-for-fake-audio-capture=${audioFile}`] : [])] } })

test('dictation adds editable text in channel and thread; cancel and navigation release capture', async ({ page }) => {
  test.setTimeout(150_000)
  mkdirSync(screenshotDir, { recursive: true })
  await signInAlice(page)
  try {
    if (!live) await page.route('**/api/TranscribeAudio', (route) => route.fulfill({ contentType: 'application/json', body: JSON.stringify('привет это проверка голосового ввода сообщений') }))
    const box = page.getByRole('textbox', { name: 'Message', exact: true })
    await box.fill('Черновик:')
    await page.getByRole('button', { name: 'Dictate message', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Finish dictation' })).toBeVisible()
    // Record enough of the known Russian fixture to exercise real local inference.
    await page.waitForTimeout(live ? 8000 : 350)
    await page.screenshot({ path: join(screenshotDir, 'dictation-recording.png') })
    await page.getByRole('button', { name: 'Finish dictation' }).click()
    await expect(box).toHaveValue(/Черновик: .*проверка/, { timeout: 90_000 })
    await expect(page.getByRole('button', { name: 'Send message', exact: true })).toBeEnabled()
    await page.screenshot({ path: join(screenshotDir, 'dictation-draft.png') })
    // Recognition does not post. The text survives through the normal saved draft.
    const text = await box.inputValue()
    await page.reload()
    await expect(page.getByRole('textbox', { name: 'Message', exact: true })).toHaveValue(text)
    const root = unique('Dictation thread')
    await seedThread(page, root, ['reply'])
    // Open through the UI so the panel owns its composer.
    const row = feed(page).locator('article', { hasText: root })
    await row.getByRole('button', { name: /^Replies:/ }).click()
    const pane = threadPane(page)
    await pane.getByRole('button', { name: 'Dictate message' }).click()
    await expect(pane.getByRole('button', { name: 'Finish dictation' })).toBeVisible()
    await pane.getByRole('button', { name: 'Cancel dictation' }).click()
    await expect(pane.getByRole('textbox', { name: 'Message' })).toHaveValue('')
    await expect(page.getByRole('textbox', { name: 'Message' }).first()).toHaveValue(text)
    await expect(pane.getByRole('button', { name: 'Dictate message' })).toBeEnabled()
    await pane.getByRole('button', { name: 'Dictate message' }).click()
    await expect(pane.getByRole('button', { name: 'Finish dictation' })).toBeVisible()
    await page.waitForTimeout(live ? 8000 : 350)
    await pane.getByRole('button', { name: 'Finish dictation' }).click()
    await expect(pane.getByRole('textbox', { name: 'Message' })).toHaveValue(/проверка/, { timeout: 90_000 })
    await pane.getByRole('button', { name: 'Dictate message' }).click()
    await expect(pane.getByRole('button', { name: 'Finish dictation' })).toBeVisible()
    await channel(page, /Off-Topic/).click()
    await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Cancel dictation' })).toHaveCount(0)
  } finally { await removeServerFromMenu(page) }
})
