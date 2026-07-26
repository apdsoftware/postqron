import { spawn, spawnSync } from 'node:child_process'
import process from 'node:process'
import {
  URL,
  fileURLToPath,
} from 'node:url'

const featureDirectory = fileURLToPath(new URL('../', import.meta.url))
const serverEntrypoint = fileURLToPath(
  new URL('../../../apps/web/.output/server/index.mjs', import.meta.url),
)
const catalogFixtureDirectory = fileURLToPath(
  new URL('./catalog-fixture/', import.meta.url),
)
const catalogFixtureBase = 'http://127.0.0.1:41737'
const catalogFixtureUrl = `${catalogFixtureBase}/api/v1/billing/plans`

const build = spawnSync(
  'pnpm',
  ['--dir', '../../apps/web', 'build'],
  {
    cwd: featureDirectory,
    env: {
      ...process.env,
      POSTQRON_API_BASE: catalogFixtureBase,
      PRELAUNCH_MODE: 'true',
    },
    stdio: 'inherit',
  },
)
if (build.status !== 0) {
  process.exit(build.status ?? 1)
}

const catalogFixture = spawn('go', ['run', '.'], {
  cwd: catalogFixtureDirectory,
  env: {
    ...process.env,
    F34_E2E_CATALOG_PORT: '41737',
    F34_E2E_SUPERVISOR_PID: String(process.pid),
    GOWORK: 'off',
  },
  stdio: 'inherit',
})

let catalogReady = false
for (let attempt = 0; attempt < 60; attempt++) {
  try {
    const response = await globalThis.fetch(catalogFixtureUrl)
    catalogReady = response.ok
  } catch {
    catalogReady = false
  }
  if (catalogReady) {
    break
  }
  await new Promise(resolve => globalThis.setTimeout(resolve, 500))
}
if (!catalogReady) {
  catalogFixture.kill('SIGTERM')
  throw new Error('authoritative plan catalog fixture did not become ready')
}

const commonEnvironment = {
  ...process.env,
  POSTQRON_API_BASE: catalogFixtureBase,
}
const children = [
  catalogFixture,
  spawn(process.execPath, [serverEntrypoint], {
    cwd: featureDirectory,
    env: {
      ...commonEnvironment,
      HOST: '127.0.0.1',
      PORT: '41734',
      PRELAUNCH_MODE: 'true',
    },
    stdio: 'inherit',
  }),
  spawn(process.execPath, [serverEntrypoint], {
    cwd: featureDirectory,
    env: {
      ...commonEnvironment,
      HOST: '127.0.0.1',
      PORT: '41735',
      PRELAUNCH_MODE: 'false',
    },
    stdio: 'inherit',
  }),
]

function stop(signal) {
  for (const child of children) {
    if (!child.killed) {
      child.kill(signal)
    }
  }
}

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, () => {
    stop(signal)
    process.exit(0)
  })
}

for (const child of children) {
  child.once('exit', (code) => {
    if (code && code !== 0) {
      stop('SIGTERM')
      process.exit(code)
    }
  })
}

await new Promise(() => {})
