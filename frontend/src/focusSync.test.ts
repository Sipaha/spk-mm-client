import { expect, test } from 'vitest'
import { createFocusSync } from './focusSync'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((r) => {
    resolve = r
  })
  return { promise, resolve }
}

test('serializes rapid blur/focus transitions so slow blur never overwrites newer focus', async () => {
  const calls: boolean[] = []
  const pending: ReturnType<typeof deferred>[] = []
  const sync = createFocusSync((focused) => {
    calls.push(focused)
    const d = deferred()
    pending.push(d)
    return d.promise
  })

  // Blur starts first and takes longer to finish in Go/IPC.
  sync(false)
  expect(calls).toEqual([false])

  // Window immediately regains focus while setFocused(false) is still in flight.
  sync(true)
  expect(calls).toEqual([false])

  // Rapid intermediate flap while still in flight coalesces to the latest state (true).
  sync(false)
  sync(true)
  expect(calls).toEqual([false])

  pending[0].resolve()
  await Promise.resolve()
  await Promise.resolve()

  expect(calls).toEqual([false, true])
  pending[1].resolve()
  await Promise.resolve()
})

test('dispose prevents queued focus updates from firing after unmount', async () => {
  const calls: boolean[] = []
  const d = deferred()
  const sync = createFocusSync((focused) => {
    calls.push(focused)
    return d.promise
  })

  sync(false)
  sync(true)
  sync.dispose()
  d.resolve()
  await Promise.resolve()
  await Promise.resolve()

  expect(calls).toEqual([false])
})
