import { defineConfig } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const port = Number(process.env.E2E_PORT ?? 5181)
const home = mkdtempSync(join(tmpdir(), 'spk-mm-e2e-'))
// E2E_BIN runs the suite against another browser-mode build (e.g. a scratch one) instead of build/bin.
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-mm-client'

export default defineConfig({
  testDir: '.',
  outputDir: join(home, 'results'),
  workers: 1, // one app instance, shared DB — tests clean up after themselves
  use: { baseURL: `http://127.0.0.1:${port}`, locale: 'en-US' },
  webServer: {
    command: `${bin} --browser --port ${port} --mm-fake --test-api`,
    url: `http://127.0.0.1:${port}/`,
    env: { SPK_MM_CLIENT_HOME: home, SPK_MM_CLIENT_DOWNLOADS: join(home, 'downloads') },
    reuseExistingServer: false,
  },
})
