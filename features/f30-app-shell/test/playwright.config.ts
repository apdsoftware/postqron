import { createRequire } from 'node:module'
import { resolve } from 'node:path'

const require = createRequire(
  new URL('../../../tests/e2e/launch-readiness/package.json', import.meta.url),
)
const { defineConfig } = require('@playwright/test') as typeof import('@playwright/test')

const repositoryRoot = resolve(import.meta.dirname, '../../..')

export default defineConfig({
  testDir: '.',
  testMatch: 'editorial-runtime.spec.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [['line']],
  use: {
    baseURL: 'http://127.0.0.1:41795',
    browserName: 'chromium',
    locale: 'en-US',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    video: 'off',
  },
  webServer: {
    command: 'node tests/e2e/launch-readiness/scripts/start-local-preview.mjs',
    cwd: repositoryRoot,
    url: 'http://127.0.0.1:41795/prezzi',
    reuseExistingServer: false,
    timeout: 240_000,
  },
})
