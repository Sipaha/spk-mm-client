import '@testing-library/jest-dom/vitest'
import { vi } from 'vitest'

// The real @wailsio/runtime package installs window-level mouse listeners and
// a window.setInterval poll as an import-time side effect (drag.js, for the
// desktop title-bar drag/resize gesture). jsdom tears its `window` down
// between test files, so that pending interval later throws "window is not
// defined" as an unhandled error and fails the whole run. Nothing under test
// here exercises the real Wails bridge, so stub the two exports client.ts
// uses instead of loading the real module.
vi.mock('@wailsio/runtime', () => ({
  Call: { ByName: vi.fn() },
  Events: { On: vi.fn(() => () => {}) },
}))

// @testing-library/react's asyncWrapper (used to await interactions from
// userEvent, e.g. inside act()) drains the microtask queue with a bare
// `setTimeout(..., 0)` that it only pairs with an explicit fake-timer
// advance when it detects *Jest's* fake timers (dist/pure.js
// jestFakeTimersAreEnabled: `typeof jest !== 'undefined' && ...`). This
// project uses Vitest, so with `vi.useFakeTimers()` active and no `jest`
// global, that setTimeout(0) is never advanced and any `await
// userEvent.type(...)/click(...)` hangs forever. Aliasing a minimal `jest`
// global to Vitest's fake-timer API makes RTL's own detection branch fire,
// closing the gap between the two fake-timer implementations.
;(globalThis as unknown as { jest?: { advanceTimersByTime(ms: number): void } }).jest = {
  advanceTimersByTime: (ms) => vi.advanceTimersByTime(ms),
}
