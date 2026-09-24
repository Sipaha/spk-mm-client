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
