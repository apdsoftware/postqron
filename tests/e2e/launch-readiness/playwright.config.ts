import { defineConfig } from '@playwright/test'
import { fileURLToPath } from 'node:url'

const suiteRoot = fileURLToPath(new URL('.', import.meta.url))
const useRemotePreview = Boolean(process.env.LAUNCH_BASE_URL)

export default defineConfig({
  testDir: './specs',
  outputDir: './artifacts/test-results',
  globalTeardown: './global-teardown.ts',
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  timeout: 45_000,
  expect: { timeout: 10_000 },
  reporter: [
    ['./reporter.ts'],
    ['line'],
    ['html', {
      outputFolder: './artifacts/html-report',
      open: 'never',
    }],
  ],
  use: {
    baseURL: process.env.LAUNCH_BASE_URL || 'http://127.0.0.1:41795',
    browserName: 'chromium',
    locale: 'en-US',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    video: 'off',
  },
  webServer: useRemotePreview
    ? undefined
    : {
        command: 'node scripts/start-local-preview.mjs',
        cwd: suiteRoot,
        url: 'http://127.0.0.1:41795/prezzi',
        reuseExistingServer: false,
        timeout: 240_000,
      },
})
