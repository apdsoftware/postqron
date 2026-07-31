import { spawnSync } from 'node:child_process'
import { resolve } from 'node:path'
import process from 'node:process'

const featureRoot = resolve(import.meta.dirname, '..')
const repositoryRoot = resolve(featureRoot, '../..')
const launchReadiness = resolve(repositoryRoot, 'tests/e2e/launch-readiness')

function run(command, arguments_, cwd = repositoryRoot) {
  const result = spawnSync(command, arguments_, {
    cwd,
    env: process.env,
    stdio: 'inherit',
  })
  if (result.status !== 0) {
    process.exit(result.status ?? 1)
  }
}

run(process.execPath, [
  '--experimental-strip-types',
  '--test',
  ...[
    'api',
    'calendar-range',
    'contracts-email',
    'editorial-blockers',
    'editorial-fixture-e2e',
    'editorial-navigation',
    'editorial-thread',
    'idempotency',
    'navigation-guard',
    'preferences',
    'slice',
    'social-api',
    'social-callback',
    'social-channels',
    'social-connections',
    'unavailable-state',
    'verify-email',
    'workspace',
  ].map(name => resolve(featureRoot, `test/${name}.test.ts`)),
], featureRoot)

if (process.env.CI !== 'true' && process.env.F30_PLAYWRIGHT !== '1') {
  process.exit(0)
}

run('pnpm', ['install', '--frozen-lockfile'], launchReadiness)
run('pnpm', ['exec', 'playwright', 'install', 'chromium'], launchReadiness)
run('pnpm', [
  'exec',
  'playwright',
  'test',
  '--config',
  '../../../features/f30-app-shell/test/playwright.config.ts',
], launchReadiness)
