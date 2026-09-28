import { expect, test, type Page } from '@playwright/test'
import { channel, fakeURL, feed, removeServerFromMenu, signInAlice, testPost, unique } from './helpers'

// WebKitGTK animates every wheel notch and cancels that animation on any
// scrollTop written by script, so a compensation write for a picture row
// measured above the fold mid-gesture cut the notch short (jerky, uneven
// steps). The feed keeps that compensation in a pending shift (a negative
// margin on the rows' container) until scrolling is idle — see
// frontend/src/components/scrollShift.ts. Chromium applies Playwright's wheel
// at once and so cannot show the cut itself; these tests check the cause (no
// script write to scrollTop mid-gesture) and everything the pending shift must
// not break: no jump, no blank space past the last row, following a new post
// at the bottom, and loading history.

const POSTS = 120 // two history pages (the client window is 60 posts)
let seeded = false // the fake server lives for the whole run (workers: 1)

// Seeds the Secret channel (no other spec looks at its content) with a mix of
// tall picture posts and text posts, straight through the fake's REST API.
async function seedPictures(page: Page) {
  if (seeded) return
  seeded = true
  const base = await fakeURL(page)
  const login = await page.request.post(`${base}/api/v4/users/login`, { data: { login_id: 'alice', password: 'secret' } })
  expect(login.ok()).toBeTruthy()
  const auth = { Authorization: `Bearer ${login.headers()['token']}` }
  for (let i = 0; i < POSTS; i++) {
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

// Signs in (a fresh feed: nothing measured yet), opens Secret at its end and
// starts the per-frame probe: script writes to scrollTop, wheel events, the
// largest pending shift seen, how far the on-screen rows moved in total, and
// frames where on-screen rows moved out of step (content jumping). Rows above
// the viewport don't count: a row measured there, and the rows above it,
// legitimately move while what is on screen stays put.
async function openSecret(page: Page) {
  await signInAlice(page)
  await seedPictures(page)
  await channel(page, /Secret/).click()
  await expect(page.getByRole('heading', { name: /Secret/ })).toBeVisible()
  const log = feed(page)
  await expect(log.getByText(`scroll post ${POSTS - 1}`, { exact: true })).toBeVisible()
  await page.waitForFunction(() => {
    const el = document.querySelector('[role=log]')!
    return el.scrollHeight - el.scrollTop - el.clientHeight < 5 && [...el.querySelectorAll('img')].every((i) => i.complete)
  })
  await page.waitForTimeout(400) // the opening scroll-to-end settles
  await log.evaluate((el) => {
    const w = window as unknown as Probe
    // The pending shift, whichever way the rows' container carries it
    // (a negative margin; commit 649189b used a translateY transform).
    w.__shift = () => {
      const s = (el.lastElementChild as HTMLElement).style
      const m = parseFloat(s.marginTop) || 0
      const t = parseFloat(/translateY\((-?[\d.]+)px\)/.exec(s.transform)?.[1] ?? '') || 0
      return m || t ? -(m || t) : 0
    }
    Object.assign(w, { __writes: [], __wheels: [], __moved: 0, __torn: 0, __maxShift: 0, __stop: false })
    const d = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop')!
    Object.defineProperty(el, 'scrollTop', {
      configurable: true,
      get() { return d.get!.call(el) },
      set(v) { w.__writes.push(performance.now()); d.set!.call(el, v) },
    })
    const scrollTo = el.scrollTo.bind(el)
    el.scrollTo = ((...a: Parameters<Element['scrollTo']>) => { w.__writes.push(performance.now()); scrollTo(...a) }) as Element['scrollTo']
    el.addEventListener('wheel', () => w.__wheels.push(performance.now()), { capture: true })
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
      w.__maxShift = Math.max(w.__maxShift, Math.abs(w.__shift()))
      prev = cur
      if (!w.__stop) requestAnimationFrame(frame)
    }
    requestAnimationFrame(frame)
  })
  const box = (await log.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  return log
}

interface Probe { __shift: () => number; __writes: number[]; __wheels: number[]; __moved: number; __torn: number; __maxShift: number; __stop: boolean }

const probe = (page: Page) => feed(page).evaluate((el) => {
  const w = window as unknown as Probe
  const first = w.__wheels[0], last = w.__wheels[w.__wheels.length - 1]
  return {
    moved: Math.round(w.__moved),
    torn: w.__torn,
    maxShift: w.__maxShift,
    wheels: w.__wheels.length,
    midGesture: w.__writes.filter((t) => t >= first && t <= last).length,
  }
})

// The feed right now: the pending shift, how far from the end of the scroll
// range, and the gap between the bottom-most post and the viewport bottom
// (the feed's own bottom padding is 8 px).
const bottom = (page: Page) => feed(page).evaluate((el) => {
  const rows = [...el.querySelectorAll<HTMLElement>('[data-kind="post"]')]
  const last = rows.reduce((a, r) => (r.getBoundingClientRect().bottom > a.getBoundingClientRect().bottom ? r : a))
  return {
    shift: (window as unknown as Probe).__shift(),
    toEnd: el.scrollHeight - el.scrollTop - el.clientHeight,
    gapBelowLast: Math.round(el.getBoundingClientRect().bottom - last.getBoundingClientRect().bottom),
    lastKey: last.dataset.key!,
    lastBottom: last.getBoundingClientRect().bottom,
  }
})

const idle = (page: Page) => page.waitForFunction(() => new Promise((res) => {
  const el = document.querySelector('[role=log]')!
  let last = el.scrollTop
  setTimeout(function check() { if (el.scrollTop === last) res(true); else { last = el.scrollTop; setTimeout(check, 300) } }, 300)
}))

// A failed test must not leave its server behind: every later test signs in
// from the empty start screen.
test.afterEach(async ({ page }) => {
  if (await page.getByRole('button', { name: 'Server menu' }).isVisible().catch(() => false)) await removeServerFromMenu(page)
})

async function wheel(page: Page, notches: number, dy: number) {
  for (let i = 0; i < notches; i++) {
    await page.mouse.wheel(0, dy)
    await page.waitForTimeout(20)
  }
}

test('wheel scrolling up through picture posts never writes scrollTop mid-gesture and does not jump', async ({ page }) => {
  await openSecret(page)
  const st = await feed(page).evaluate((el) => el.scrollTop)
  await wheel(page, 60, -100)
  await idle(page)
  const p = await probe(page)
  expect(p.wheels).toBe(60)
  expect(st - 6000, 'the burst must stay clear of the top of the channel').toBeGreaterThan(800)
  expect(p.maxShift, 'rows were measured above the fold mid-gesture (else this tests nothing)').toBeGreaterThan(0)
  expect(p.midGesture, 'scrollTop written by script during the wheel gesture').toBe(0)
  expect(p.torn, 'frames where on-screen rows moved out of step').toBe(0)
  expect(p.moved).toBe(6000)
})

test('up and back down in one gesture: no blank space past the last post, and no jump when the shift lands', async ({ page }) => {
  await openSecret(page)
  await wheel(page, 110, -100)
  await wheel(page, 200, 100)
  const mid = await bottom(page) // still inside the gesture: the shift has not landed
  expect(mid.shift, 'a shift is pending at the bottom (else this tests nothing)').toBeGreaterThan(0)
  expect(mid.toEnd).toBeLessThan(2)
  expect(mid.gapBelowLast, 'the last post sits at the bottom, not above blank space').toBeLessThanOrEqual(9)
  await idle(page)
  const after = await bottom(page)
  expect(after.shift).toBe(0)
  expect(after.lastKey).toBe(mid.lastKey)
  expect(Math.abs(after.lastBottom - mid.lastBottom), 'the shift landed without moving the content').toBeLessThanOrEqual(1)
  expect(after.toEnd).toBeLessThan(2)
  const p = await probe(page)
  expect(p.torn).toBe(0)
})

test('a new post arriving at the bottom while a shift is pending is followed', async ({ page }) => {
  await openSecret(page)
  await wheel(page, 110, -100)
  await wheel(page, 200, 100)
  expect((await bottom(page)).shift, 'a shift is pending at the bottom (else this tests nothing)').toBeGreaterThan(0)
  const text = unique('arrives while shifted')
  await testPost(page, 'fake/post', { channel_id: 'c-secret', username: 'alice', message: text })
  // keep the gesture going (wheel at the end of the range scrolls nothing)
  // until the post shows up
  const post = feed(page).getByText(text, { exact: true })
  for (let i = 0; i < 200 && !(await post.count()); i++) await wheel(page, 1, 100)
  await expect(post).toBeInViewport()
  await idle(page)
  await expect(post).toBeInViewport()
  expect((await bottom(page)).toEnd).toBeLessThan(2)
})

test('history loads while a shift is pending, and scrolling on reaches the first post', async ({ page }) => {
  await openSecret(page)
  const first = feed(page).getByText('scroll post 0', { exact: true })
  for (let i = 0; i < 600 && !(await first.count()); i++) await wheel(page, 1, -100)
  await expect(first).toBeVisible()
  const p = await probe(page)
  expect(p.maxShift, 'a shift was pending on the way up (else this tests nothing)').toBeGreaterThan(0)
})
