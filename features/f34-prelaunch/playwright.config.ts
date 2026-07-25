import { defineConfig } from '@playwright/test'

const enabledPort = 41734

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  timeout: 30_000,
  use: {
    baseURL: `http://127.0.0.1:${enabledPort}`,
    browserName: 'chromium',
    channel: 'chrome',
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'node scripts/e2e-server.mjs',
    url: `http://127.0.0.1:${enabledPort}/prelaunch`,
    reuseExistingServer: false,
    timeout: 180_000,
  },
})
