import { afterEach, expect, test, vi } from 'vitest'
import { base64, encodeWAV, recordAudio } from './dictationAudio'

afterEach(() => vi.unstubAllGlobals())

test('encodes clipped PCM16 mono16k WAV with exact data and RIFF lengths', () => {
  const bytes = encodeWAV(new Float32Array([-2, 0, 2]))
  const view = new DataView(bytes.buffer as ArrayBuffer)
  expect(bytes.length).toBe(50)
  expect(view.getUint32(4, true)).toBe(42)
  expect(view.getUint32(24, true)).toBe(16000)
  expect(view.getUint32(40, true)).toBe(6)
  expect([44, 46, 48].map((i) => view.getInt16(i, true))).toEqual([-32768, 0, 32767])
  expect(atob(base64(bytes)).length).toBe(bytes.length)
})

test('cancelling a pending permission request closes the context and stops late microphone tracks', async () => {
  let resolve!: (v: MediaStream) => void
  const close = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('AudioContext', class { resume = () => Promise.resolve(); close = close })
  vi.stubGlobal('navigator', { mediaDevices: { getUserMedia: () => new Promise<MediaStream>((r) => { resolve = r }) } })
  const controller = new AbortController()
  const pending = recordAudio(controller.signal, vi.fn(), vi.fn())
  controller.abort()
  expect(close).toHaveBeenCalledTimes(1)
  const stop = vi.fn()
  resolve({ getTracks: () => [{ stop }] } as unknown as MediaStream)
  await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
  expect(stop).toHaveBeenCalledTimes(1)
  expect(close).toHaveBeenCalledTimes(1)
})
