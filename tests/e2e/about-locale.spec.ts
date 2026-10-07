import { expect, test } from '@playwright/test'
import { join } from 'node:path'
import { screenshotDir } from './helpers'

test.use({ locale: 'ru-RU' })
test('Russian About uses approved identity and explicit localized destinations', async ({ page }) => {
  await page.goto('/')
  const trigger = page.getByRole('button', { name: 'О приложении', exact: true })
  await expect(trigger).toBeVisible()
  await trigger.click()
  const dialog = page.getByRole('dialog', { name: 'О SPK MM Client' })
  await expect(dialog).toContainText('Павел Симонов')
  await expect(dialog).toContainText('Apache License 2.0')
  await expect(dialog.getByRole('link', { name: 'Сайт приложения' })).toHaveAttribute('href', 'https://sipaha.github.io/spk-mm-client/?lang=ru')
  await expect(dialog.getByRole('link', { name: 'Об авторе' })).toHaveAttribute('href', 'https://sipaha.github.io/about/?lang=ru')
  await expect(dialog.getByRole('button', { name: 'Закрыть' })).toBeFocused()
  await page.screenshot({ path: join(screenshotDir, 'about-ru.png') })
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(trigger).toBeFocused()
})
