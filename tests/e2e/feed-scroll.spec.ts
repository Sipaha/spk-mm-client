import { expect, test, type Page } from '@playwright/test'
import { channel, fakeURL, feed, removeServerFromMenu, signInAlice } from './helpers'

// Seeds the Secret channel (no other spec looks at its content) with a mix
// of tall picture posts and text posts, straight through the fake server's
// REST API as alice.
async function seedPictures(page: Page, posts: number) {
  const base = await fakeURL(page)
  const login = await page.request.post(`${base}/api/v4/users/login`, { data: { login_id: 'alice', password: 'secret' } })
  expect(login.ok()).toBeTruthy()
  const auth = { Authorization: `Bearer ${login.headers()['token']}` }
  for (let i = 0; i < posts; i++) {
    let fileIds: string[] = []
    if (i % 3 !== 2) {
      const png = await page.evaluate(async (n) => {
        const c = new OffscreenCanvas(600, 400)
        const g = c.getContext('2d')!
        g.fillStyle = `hsl(${(n * 37) % 360} 60% 45%)`
        g.fillRect(0, 0, 600, 400)
        const bytes = new Uint8Array(await (await c.convertToBlob({ type: 'image/png' })).arrayBuffer())
        return btoa(String.fromCharCode(...bytes))
      }, i)
      const up = await page.request.post(`${base}/api/v4/files?channel_id=c-secret&filename=scroll${i}.png`, {
        headers: { ...auth, 'Content-Type': 'image/png' },
        data: Buffer.from(png, 'base64'),
      })
      expect(up.ok(), await up.text()).toBeTruthy()
      fileIds = [(await up.json()).file_infos[0].id]
    }
    const post = await page.request.post(`${base}/api/v4/posts`, {
      headers: auth,
      data: { channel_id: 'c-secret', message: `scroll post ${i}`, file_ids: fileIds },
    })
    expect(post.ok(), await post.text()).toBeTruthy()
  }
}

// WebKitGTK animates every wheel notch and cancels that animation on any
// scrollTop written by script, so a compensation write for a picture row
// measured above the fold mid-gesture cut the notch short (jerky, uneven
// steps). Chromium applies Playwright's wheel at once and so cannot show the
// cut itself; what it can show is the cause: no script write to the feed's
// scrollTop while the wheel gesture is going on — and, once it settles, the
// content moved by exactly what the wheel asked for (no jump).
test('wheel scrolling up through picture posts never writes scrollTop mid-gesture and does not jump', async ({ page }) => {
  await signInAlice(page)
  await seedPictures(page, 120)
  await channel(page, /Secret/).click()
  await expect(page.getByRole('heading', { name: /Secret/ })).toBeVisible()
  const log = feed(page)
  await expect(log.getByText('scroll post 119', { exact: true })).toBeVisible()
  await page.waitForFunction(() => {
    const el = document.querySelector('[role=log]')!
    return el.scrollHeight - el.scrollTop - el.clientHeight < 5 && [...el.querySelectorAll('img')].every((i) => i.complete)
  })
  await page.waitForTimeout(400) // the opening scroll-to-end settles

  const before = await log.evaluate((el) => {
    const w = window as unknown as { __writes: number[]; __wheels: number[]; __moved: number; __torn: number; __stop: boolean }
    Object.assign(w, { __writes: [], __wheels: [], __moved: 0, __torn: 0, __stop: false })
    const d = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop')!
    Object.defineProperty(el, 'scrollTop', {
      configurable: true,
      get() { return d.get!.call(el) },
      set(v) { w.__writes.push(performance.now()); d.set!.call(el, v) },
    })
    const scrollTo = el.scrollTo.bind(el)
    el.scrollTo = ((...a: Parameters<Element['scrollTo']>) => { w.__writes.push(performance.now()); scrollTo(...a) }) as Element['scrollTo']
    el.addEventListener('wheel', () => w.__wheels.push(performance.now()), { capture: true })
    // Every frame: how far the post rows on screen in both this and the
    // previous frame moved. They must all move together (a row out of step
    // is content jumping), and the moves must add up to what the wheel asked.
    // Rows above the viewport don't count: a row measured there, and the
    // rows above it, legitimately move while what is on screen stays put.
    const rows = () => {
      const top = el.getBoundingClientRect().top
      return new Map(
        [...el.querySelectorAll<HTMLElement>('[data-kind="post"]')]
          .map((r) => { const b = r.getBoundingClientRect(); return [r.dataset.key!, b.top - top, b.bottom - top] as const })
          .filter(([, t, b]) => b > 0 && t < el.clientHeight)
          .map(([k, t]) => [k, t]),
      )
    }
    let prev = rows()
    const frame = () => {
      const cur = rows()
      const moves = [...cur].filter(([k]) => prev.has(k)).map(([k, y]) => y - prev.get(k)!)
      if (moves.length) {
        moves.sort((a, b) => a - b)
        w.__moved += moves[moves.length >> 1]
        if (moves[moves.length - 1] - moves[0] > 1) w.__torn++
      }
      prev = cur
      if (!w.__stop) requestAnimationFrame(frame)
    }
    requestAnimationFrame(frame)
    return { st: el.scrollTop }
  })

  const box = (await log.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  const steps = 60
  for (let i = 0; i < steps; i++) {
    await page.mouse.wheel(0, -100)
    await page.waitForTimeout(20)
  }
  // settled: no scroll for a while (any deferred correction has landed)
  await page.waitForFunction(() => new Promise((res) => {
    const el = document.querySelector('[role=log]')!
    let last = el.scrollTop
    setTimeout(function check() { if (el.scrollTop === last) res(true); else { last = el.scrollTop; setTimeout(check, 300) } }, 300)
  }))

  const after = await log.evaluate((el) => {
    const w = window as unknown as { __writes: number[]; __wheels: number[]; __moved: number; __torn: number; __stop: boolean }
    w.__stop = true
    const first = w.__wheels[0], last = w.__wheels[w.__wheels.length - 1]
    return {
      moved: w.__moved,
      torn: w.__torn,
      wheels: w.__wheels.length,
      midGesture: w.__writes.filter((t) => t >= first && t <= last).length,
    }
  })
  expect(after.wheels).toBe(steps)
  expect(before.st - steps * 100, 'the burst must stay clear of the top of the channel').toBeGreaterThan(800)
  expect(after.midGesture, 'scrollTop written by script during the wheel gesture').toBe(0)
  expect(after.torn, 'frames where on-screen rows moved out of step').toBe(0)
  expect(Math.round(after.moved)).toBe(steps * 100)
  await removeServerFromMenu(page)
})
