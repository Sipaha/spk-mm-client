import { mkdirSync } from 'node:fs'
import { createServer, request } from 'node:http'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { channel, fakeURL, feed, removeServerFromMenu, screenshotDir, testPost, unique } from './helpers'

test('online cancels a blackholed reconnect and recovers missed messages', async ({ page }) => {
  await page.goto('/')
  const origin = await fakeURL(page)
  let blackhole = false
  let blocked = false
  let cancelled = false
  // A recovered network accepts new requests, but leaves one old request
  // unanswered. Browser offline mode alone cannot model Go's network.
  const proxy = createServer((req, res) => {
    if (blackhole && req.url === '/api/v4/users/me') {
      blackhole = false
      blocked = true
      res.on('close', () => { cancelled = true })
      return
    }
    const upstream = request(new URL(req.url!, origin), { method: req.method, headers: req.headers }, (answer) => {
      res.writeHead(answer.statusCode!, answer.headers)
      answer.pipe(res)
    })
    upstream.on('error', () => res.destroy())
    res.on('close', () => upstream.destroy())
    req.pipe(upstream)
  })
  const sockets = new Set<import('node:stream').Duplex>()
  proxy.on('connection', (socket) => {
    sockets.add(socket)
    socket.on('error', () => {})
    socket.on('close', () => sockets.delete(socket))
  })
  proxy.on('upgrade', (req, socket, head) => {
    const upstream = request(new URL(req.url!, origin), { headers: req.headers })
    upstream.on('upgrade', (answer, peer, peerHead) => {
      const headers = answer.rawHeaders.map((v, i, a) => i % 2 === 0 ? `${v}: ${a[i + 1]}\r\n` : '').join('')
      socket.write(`HTTP/1.1 101 Switching Protocols\r\n${headers}\r\n`)
      if (peerHead.length) socket.write(peerHead)
      if (head.length) peer.write(head)
      peer.on('error', () => socket.destroy())
      socket.on('close', () => peer.destroy())
      peer.on('close', () => socket.destroy())
      socket.pipe(peer).pipe(socket)
    })
    upstream.on('error', () => socket.destroy())
    socket.on('close', () => upstream.destroy())
    upstream.end()
  })
  await new Promise<void>((resolve) => proxy.listen(0, '127.0.0.1', resolve))
  try {
    const address = proxy.address() as import('node:net').AddressInfo
    await page.getByLabel('Server address').fill(`http://127.0.0.1:${address.port}`)
    await page.getByRole('button', { name: 'Add', exact: true }).click()
    await page.getByLabel('Login or email').fill('alice')
    await page.getByLabel('Password').fill('secret')
    await page.getByRole('button', { name: 'Sign in', exact: true }).click()
    await channel(page, /Off-Topic/).click()
    await expect(page.getByRole('heading', { name: /Off-Topic/ })).toBeVisible()
    blackhole = true
    await testPost(page, 'fake/drop', { lose: true })
    await expect.poll(() => blocked).toBe(true)
    const text = unique('Recovered after internet outage')
    await testPost(page, 'fake/post', { channel_id: 'c-offtopic', username: 'bob', message: text })
    await page.evaluate(() => window.dispatchEvent(new Event('online')))
    await expect.poll(() => cancelled, { timeout: 2000 }).toBe(true)
    await expect(feed(page).getByText(text)).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Connecting…', { exact: true })).toHaveCount(0)
    await expect(page.getByText('Offline — reconnecting…', { exact: true })).toHaveCount(0)
    mkdirSync(screenshotDir, { recursive: true })
    await page.screenshot({ path: join(screenshotDir, 'network-recovered.png') })
    await removeServerFromMenu(page)
  } finally {
    for (const socket of sockets) socket.destroy()
    await new Promise<void>((resolve) => proxy.close(() => resolve()))
  }
})
