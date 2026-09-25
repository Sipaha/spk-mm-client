import { defineConfig } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const port = Number(process.env.E2E_PORT ?? 5181)
const home = mkdtempSync(join(tmpdir(), 'spk-mm-e2e-'))

export default defineConfig({
  testDir: '.',
  workers: 1, // one app instance, shared DB — tests clean up after themselves
  use: { baseURL: `http://127.0.0.1:${port}`, locale: 'en-US' },
  webServer: {
    command: `../../build/bin/spk-mm-client --browser --port ${port} --mm-fake --test-api`,
    url: `http://127.0.0.1:${port}/`,
    env: { SPK_MM_CLIENT_HOME: home },
    reuseExistingServer: false,
  },
})
