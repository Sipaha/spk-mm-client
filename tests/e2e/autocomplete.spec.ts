import { expect, test, type Page } from '@playwright/test'
import { channel, feed, fakePost, removeServerFromMenu, rootRow, signInAlice, testPost, threadComposer, threadFeed, unique } from './helpers'

// Composer autocomplete brief 2026-09-29: @users, ~channels, :emoji: and
// /commands in the composer's popup, and slash commands run on send. A
// failed test must not leave its server behind (same rule as chat.spec.ts).
// The fake is shared by every spec: this one posts only into the private
// "Secret" channel (other specs expect Town Square's and Off-Topic's latest
// posts on screen) and puts alice back online after /away.
test.afterEach(async ({ page }) => {
  if (page.url().startsWith('http')) await testPost(page, 'fake/status', { username: 'alice', status: 'online' }).catch(() => {})
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
})

async function signInToSecret(page: Page) {
  await signInAlice(page)
  await channel(page, /Secret/).click()
  await expect(page.getByRole('heading', { name: /Secret/ })).toBeVisible()
}

const popup = (page: Page) => page.getByRole('listbox', { name: 'Suggestions' })
const composer = (page: Page) => page.getByRole('textbox', { name: 'Message' })

test('@b → pick bob → send: the mention renders', async ({ page }) => {
  await signInToSecret(page)
  const box = composer(page)
  const text = unique('ping')
  await box.fill(`${text} `)
  await box.press('End')
  await box.pressSequentially('@b')
  await expect(popup(page).getByRole('option', { name: /@bob/ })).toBeVisible()
  await expect(box).toHaveAttribute('aria-expanded', 'true')
  await popup(page).getByRole('option', { name: /@bob/ }).click()
  await expect(box).toHaveValue(`${text} @bob `)
  await expect(popup(page)).toBeHidden()
  await box.press('Enter')
  await expect(feed(page).locator('article', { hasText: text }).locator('[data-mention="bob"]')).toHaveText('@bob')
})

test(':thu → Enter inserts :thumbsup: and the post shows 👍; Enter never sends while the popup is open', async ({ page }) => {
  await signInToSecret(page)
  const box = composer(page)
  const text = unique('thumbs')
  await box.fill(`${text} `)
  await box.press('End')
  await box.pressSequentially(':thu')
  await expect(popup(page).getByRole('option').first()).toContainText(':thumbsup:')
  await box.press('Enter')
  await expect(box).toHaveValue(`${text} :thumbsup: `)
  await expect(feed(page).locator('article', { hasText: text })).toHaveCount(0)
  await box.press('Enter')
  await expect(feed(page).locator('article', { hasText: text })).toContainText('👍')
})

test('~off → my channel first, another one below; the sent link opens the channel', async ({ page }) => {
  await signInToSecret(page)
  const box = composer(page)
  const text = unique('see')
  await box.fill(`${text} `)
  await box.press('End')
  await box.pressSequentially('~off')
  const options = popup(page).getByRole('option')
  await expect(options).toHaveCount(2)
  await expect(options.nth(0)).toContainText('Off-Topic')
  await expect(options.nth(1)).toContainText('Offices')
  await expect(popup(page).getByText('My Channels')).toBeVisible()
  await expect(popup(page).getByText('Other Channels')).toBeVisible()
  await box.press('ArrowDown')
  await box.press('ArrowUp')
  await box.press('Tab')
  await expect(box).toHaveValue(`${text} ~off-topic `)
  await box.press('Enter')
  const link = feed(page).locator('article', { hasText: text }).getByRole('link', { name: '~Off-Topic' })
  await expect(link).toBeVisible()
  await link.click()
  await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
})

test('/echo executes the command (the fake posts its text), /away answers with an ephemeral post', async ({ page }) => {
  await signInToSecret(page)
  const box = composer(page)
  await box.pressSequentially('/ec')
  await expect(popup(page).getByRole('option', { name: /\/echo/ })).toContainText('Echo back text from your account')
  await box.press('Enter')
  await expect(box).toHaveValue('/echo ')
  const text = unique('echoed')
  await box.pressSequentially(text)
  await box.press('Enter')
  await expect(box).toHaveValue('')
  const echoed = feed(page).locator('article', { hasText: text })
  await expect(echoed).toBeVisible()
  await expect(echoed).not.toContainText('/echo')

  await box.pressSequentially('/aw')
  await expect(popup(page).getByRole('option', { name: /\/away/ })).toBeVisible()
  await box.press('Enter')
  await expect(box).toHaveValue('/away ')
  await box.press('Enter')
  const eph = feed(page).locator('article', { hasText: 'You are now away' }).last()
  await expect(eph).toContainText('(Only visible to you)')
  await expect(eph).toContainText('System') // the server's answer, not alice's
})

test('an unknown command keeps the text and can be sent as a message', async ({ page }) => {
  await signInToSecret(page)
  const box = composer(page)
  const text = `/nope ${unique('x')}`
  await box.fill(text)
  await box.press('Enter')
  await expect(page.getByRole('alert')).toContainText("Command with a trigger of '/nope' not found.")
  await expect(box).toHaveValue(text)
  await page.getByRole('button', { name: 'Click here to send as a message.' }).click()
  await expect(feed(page).locator('article', { hasText: text })).toBeVisible()
  await expect(box).toHaveValue('')
})

test('the thread composer: @ suggestions and a command with the thread root', async ({ page }) => {
  await signInToSecret(page)
  const root = unique('ac root')
  await fakePost(page, 'c-secret', 'alice', root)
  await rootRow(page, root).hover()
  await rootRow(page, root).getByRole('button', { name: 'Reply in thread' }).click()
  const box = threadComposer(page)
  await expect(box).toBeFocused()
  const text = unique('thread ping')
  await box.fill(`${text} `)
  await box.press('End')
  await box.pressSequentially('@car')
  await expect(page.getByRole('listbox', { name: 'Suggestions' }).getByRole('option', { name: /@carol/ })).toBeVisible()
  await box.press('Enter')
  await box.press('Enter')
  await expect(threadFeed(page).locator('article', { hasText: text }).locator('[data-mention="carol"]')).toBeVisible()

  await box.pressSequentially('/aw')
  await expect(page.getByRole('listbox', { name: 'Suggestions' }).getByRole('option', { name: /\/away/ })).toBeVisible()
  await box.press('Enter')
  await box.press('Enter')
  await expect(box).toHaveValue('')
  await expect(threadFeed(page).locator('article', { hasText: 'You are now away' })).toContainText('(Only visible to you)')
})

// Fix round 1 (security review): /leave never leaves by accident — a
// private channel asks first, a thread refuses it — and /logout signs out.
test('/leave: a private channel asks first, a thread refuses it', async ({ page }) => {
  await signInToSecret(page)
  const box = composer(page)
  await box.fill('/leave')
  await expect(popup(page)).toBeVisible()
  await box.press('Escape')
  await box.press('Enter')
  const ask = page.getByRole('alert')
  await expect(ask).toContainText('Are you sure you wish to leave the private channel Secret?')
  await ask.getByRole('button', { name: 'Cancel' }).click()
  await expect(ask).toBeHidden()
  await expect(box).toHaveValue('/leave')
  await box.fill('')

  const root = unique('leave root')
  await fakePost(page, 'c-secret', 'alice', root)
  await rootRow(page, root).hover()
  await rootRow(page, root).getByRole('button', { name: 'Reply in thread' }).click()
  const reply = threadComposer(page)
  await reply.fill('/leave')
  await expect(page.getByRole('listbox', { name: 'Suggestions' })).toBeVisible()
  await reply.press('Escape')
  await reply.press('Enter')
  await expect(page.getByRole('alert')).toContainText('/leave is not supported in reply threads')
  await expect(reply).toHaveValue('/leave')
  await expect(channel(page, /Secret/)).toBeVisible()
})

test('/logout signs the server out', async ({ page }) => {
  await signInToSecret(page)
  const box = composer(page)
  await box.fill('/logout')
  await expect(popup(page)).toBeVisible()
  await box.press('Escape')
  await box.press('Enter')
  await expect(page.getByLabel('Login or email')).toBeVisible()
  // Sign back in so afterEach removes the server the usual way.
  await page.getByLabel('Login or email').fill('alice')
  await page.getByLabel('Password').fill('secret')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('heading', { name: /Town Square|Secret/ })).toBeVisible()
})
