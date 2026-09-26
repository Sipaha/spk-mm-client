import { registerMediaElement, releaseMediaElement } from './mediaSession'

function fakeMedia(): HTMLMediaElement {
  // jsdom's play()/pause()/load() are stubs that just log "not implemented"
  // and never change .paused; spying on them is enough to assert calls
  // without a real media pipeline.
  return document.createElement('video')
}

test('starting one registered player pauses every other one', () => {
  const a = fakeMedia()
  const b = fakeMedia()
  const c = fakeMedia()
  const pauseB = vi.spyOn(b, 'pause')
  const pauseC = vi.spyOn(c, 'pause')
  const offA = registerMediaElement(a)
  const offB = registerMediaElement(b)
  const offC = registerMediaElement(c)

  a.dispatchEvent(new Event('play'))
  expect(pauseB).toHaveBeenCalledTimes(1)
  expect(pauseC).toHaveBeenCalledTimes(1)

  offA()
  offB()
  offC()
})

test('unregistering stops a player from being paused by others, and from pausing others', () => {
  const a = fakeMedia()
  const b = fakeMedia()
  const pauseB = vi.spyOn(b, 'pause')
  const offA = registerMediaElement(a)
  const offB = registerMediaElement(b)
  offB()
  a.dispatchEvent(new Event('play'))
  expect(pauseB).not.toHaveBeenCalled()
  offA()
})

test('releaseMediaElement pauses and lets go of the source (Task 13b: a removed player must not keep a decoder alive)', () => {
  const el = fakeMedia()
  el.setAttribute('src', 'https://example.invalid/f')
  const pause = vi.spyOn(el, 'pause')
  const load = vi.spyOn(el, 'load')
  releaseMediaElement(el)
  expect(pause).toHaveBeenCalledTimes(1)
  expect(el.hasAttribute('src')).toBe(false)
  expect(load).toHaveBeenCalledTimes(1)
})
