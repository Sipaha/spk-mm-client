import { act, fireEvent, render, screen } from '@testing-library/react'
import { createRef } from 'react'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { ImageZoom, type ImageZoomHandle } from './ImageZoom'

beforeEach(() => setLocale('en'))

const img: FileView = { id: 'f-build', name: 'build.png', ext: 'png', size: 5000, mime: 'image/png', width: 1280, height: 720, has_preview: true }
const noPreview: FileView = { id: 'f-plain', name: 'plain.png', ext: 'png', size: 5000, mime: 'image/png', width: 800, height: 600 }
const noop = () => {}

// jsdom never computes real layout/transforms: getBoundingClientRect always
// returns zeros. Simulate what the browser would report for an element
// carrying `transform: translate(...) scale(s)` on top of a fitted box of
// fitW×fitH — the tests below only rely on the resulting width/height
// growing with scale and the box staying centered at (200, 150).
function stubImageRect(fitW = 400, fitH = 300) {
  const orig = HTMLImageElement.prototype.getBoundingClientRect
  HTMLImageElement.prototype.getBoundingClientRect = function (this: HTMLElement) {
    const m = /scale\(([-\d.eE]+)\)/.exec(this.style.transform || '')
    const s = m ? parseFloat(m[1]) : 1
    const w = fitW * s
    const h = fitH * s
    return { width: w, height: h, left: 200 - w / 2, top: 150 - h / 2, right: 200 + w / 2, bottom: 150 + h / 2, x: 200 - w / 2, y: 150 - h / 2, toJSON: () => ({}) } as DOMRect
  }
  return () => {
    HTMLImageElement.prototype.getBoundingClientRect = orig
  }
}

afterEach(() => vi.restoreAllMocks())

function renderZoom(file: FileView) {
  const onPercent = vi.fn()
  const onLoaded = vi.fn()
  const onFail = vi.fn()
  const onDragEnd = vi.fn()
  const ref = createRef<ImageZoomHandle>()
  render(<ImageZoom ref={ref} serverId={1} file={file} loaded={false} onLoaded={onLoaded} onFail={onFail} onDragEnd={onDragEnd} onPercent={onPercent} />)
  return { onPercent, onLoaded, onFail, onDragEnd, ref }
}

test('the original is requested immediately, and the preview is shown as a placeholder until it loads', () => {
  const { container } = render(<ImageZoom serverId={1} file={img} loaded={false} onLoaded={noop} onFail={noop} onDragEnd={noop} onPercent={noop} />)
  const preview = container.querySelector('img[src="/media/1/full/f-build?src=preview"]')
  const original = container.querySelector('img[src="/media/1/full/f-build?src=file"]')
  expect(preview).not.toBeNull()
  expect(original).not.toBeNull() // requested right away, not deferred until the preview fails
  expect(preview).toHaveAttribute('alt', 'build.png')
  expect(original).toHaveAttribute('alt', '')
})

test('a file without a preview requests only the original', () => {
  const { container } = render(<ImageZoom serverId={1} file={noPreview} loaded={false} onLoaded={noop} onFail={noop} onDragEnd={noop} onPercent={noop} />)
  expect(container.querySelector('img[src$="src=preview"]')).toBeNull()
  const original = screen.getByRole('img', { name: 'plain.png' })
  expect(original).toHaveAttribute('src', '/media/1/full/f-plain?src=file')
})

test('a working original replacing a broken preview does not mark the file as failed', () => {
  const restore = stubImageRect()
  try {
    const { onFail } = renderZoom(img)
    const imgs = document.querySelectorAll('img')
    const originalEl = Array.from(imgs).find((el) => el.getAttribute('src')?.includes('src=file'))!
    const previewEl = Array.from(imgs).find((el) => el.getAttribute('src')?.includes('src=preview'))!
    fireEvent.error(previewEl)
    fireEvent.load(originalEl)
    expect(onFail).not.toHaveBeenCalled()
    expect(originalEl).toHaveAttribute('alt', 'build.png')
  } finally {
    restore()
  }
})

test('wheel zooms in around the cursor and reports a growing percentage; wheel out returns toward it', () => {
  const restore = stubImageRect(400, 300)
  try {
    const { onPercent } = renderZoom(img)
    const stage = document.querySelector('.relative.flex.h-full.w-full') as HTMLElement
    const before = onPercent.mock.calls.at(-1)![0] as number
    fireEvent.wheel(stage, { deltaY: -100, clientX: 250, clientY: 150 })
    const afterIn = onPercent.mock.calls.at(-1)![0] as number
    expect(afterIn).toBeGreaterThan(before)
    fireEvent.wheel(stage, { deltaY: 100, clientX: 250, clientY: 150 })
    const afterOut = onPercent.mock.calls.at(-1)![0] as number
    expect(afterOut).toBeLessThan(afterIn)
  } finally {
    restore()
  }
})

test('double-click zooms from fit to natural size, and again back to fit', () => {
  const restore = stubImageRect(400, 300)
  try {
    const { onPercent } = renderZoom(img)
    const stage = document.querySelector('.relative.flex.h-full.w-full') as HTMLElement
    const fitPercent = onPercent.mock.calls.at(-1)![0] as number
    fireEvent.doubleClick(stage, { clientX: 200, clientY: 150 })
    const naturalPercent = onPercent.mock.calls.at(-1)![0] as number
    expect(naturalPercent).toBeGreaterThan(fitPercent)
    fireEvent.doubleClick(stage, { clientX: 200, clientY: 150 })
    const backToFit = onPercent.mock.calls.at(-1)![0] as number
    expect(backToFit).toBe(fitPercent)
  } finally {
    restore()
  }
})

test('+/- and 0 keys zoom and reset; they are ignored while typing in an input', () => {
  const restore = stubImageRect(400, 300)
  try {
    const { onPercent } = renderZoom(img)
    const fitPercent = onPercent.mock.calls.at(-1)![0] as number
    fireEvent.keyDown(window, { key: '+' })
    const zoomedPercent = onPercent.mock.calls.at(-1)![0] as number
    expect(zoomedPercent).toBeGreaterThan(fitPercent)
    fireEvent.keyDown(window, { key: '-' })
    const backDown = onPercent.mock.calls.at(-1)![0] as number
    expect(backDown).toBeLessThan(zoomedPercent)
    fireEvent.keyDown(window, { key: '0' })
    expect(onPercent.mock.calls.at(-1)![0]).toBe(fitPercent)

    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    const before = onPercent.mock.calls.length
    fireEvent.keyDown(window, { key: '+' })
    expect(onPercent.mock.calls.length).toBe(before) // ignored: focus is in an input
    input.remove()
  } finally {
    restore()
  }
})

test('the Fit imperative handle resets zoom back to fit', () => {
  const restore = stubImageRect(400, 300)
  try {
    const { onPercent, ref } = renderZoom(img)
    const fitPercent = onPercent.mock.calls.at(-1)![0] as number
    fireEvent.keyDown(window, { key: '+' })
    expect(onPercent.mock.calls.at(-1)![0]).toBeGreaterThan(fitPercent)
    act(() => ref.current?.fit())
    expect(onPercent.mock.calls.at(-1)![0]).toBe(fitPercent)
  } finally {
    restore()
  }
})

test('dragging with the left mouse button pans once zoomed in, and reports a drag end (not a plain click)', () => {
  const restore = stubImageRect(400, 300)
  try {
    const { onDragEnd } = renderZoom(img)
    const stage = document.querySelector('.relative.flex.h-full.w-full') as HTMLElement
    fireEvent.keyDown(window, { key: '+' }) // zoom in so there's room to pan
    fireEvent.mouseDown(stage, { button: 0, clientX: 100, clientY: 100 })
    fireEvent.mouseMove(window, { clientX: 140, clientY: 130 })
    fireEvent.mouseUp(window)
    expect(onDragEnd).toHaveBeenCalledTimes(1)
  } finally {
    restore()
  }
})

test('a plain click (no movement) does not report a drag end', () => {
  const restore = stubImageRect(400, 300)
  try {
    const { onDragEnd } = renderZoom(img)
    const stage = document.querySelector('.relative.flex.h-full.w-full') as HTMLElement
    fireEvent.keyDown(window, { key: '+' })
    fireEvent.mouseDown(stage, { button: 0, clientX: 100, clientY: 100 })
    fireEvent.mouseUp(window, { clientX: 100, clientY: 100 })
    expect(onDragEnd).not.toHaveBeenCalled()
  } finally {
    restore()
  }
})

test('dragging is a no-op at fit (nothing to pan)', () => {
  const restore = stubImageRect(400, 300)
  try {
    const { onDragEnd } = renderZoom(img)
    const stage = document.querySelector('.relative.flex.h-full.w-full') as HTMLElement
    fireEvent.mouseDown(stage, { button: 0, clientX: 100, clientY: 100 })
    fireEvent.mouseMove(window, { clientX: 140, clientY: 130 })
    fireEvent.mouseUp(window)
    expect(onDragEnd).not.toHaveBeenCalled()
  } finally {
    restore()
  }
})

test('switching files resets zoom to fit (a fresh ImageZoom keyed by file id)', () => {
  const restore = stubImageRect(400, 300)
  try {
    const onPercent = vi.fn()
    const { rerender } = render(<ImageZoom key="a" serverId={1} file={img} loaded={false} onLoaded={noop} onFail={noop} onDragEnd={noop} onPercent={onPercent} />)
    fireEvent.keyDown(window, { key: '+' })
    const zoomed = onPercent.mock.calls.at(-1)![0] as number
    onPercent.mockClear()
    rerender(<ImageZoom key="b" serverId={1} file={noPreview} loaded={false} onLoaded={noop} onFail={noop} onDragEnd={noop} onPercent={onPercent} />)
    const afterSwitch = onPercent.mock.calls.at(0)![0] as number
    expect(afterSwitch).not.toBe(zoomed)
  } finally {
    restore()
  }
})
