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

const build = spawnSync(
  'pnpm',
  ['--dir', '../../apps/web', 'build'],
  {
    cwd: featureDirectory,
    env: { ...process.env, PRELAUNCH_MODE: 'true' },
    stdio: 'inherit',
  },
)
if (build.status !== 0) {
  process.exit(build.status ?? 1)
}

const children = [
  spawn(process.execPath, [serverEntrypoint], {
    cwd: featureDirectory,
    env: {
      ...process.env,
      HOST: '127.0.0.1',
      PORT: '41734',
      PRELAUNCH_MODE: 'true',
    },
    stdio: 'inherit',
  }),
  spawn(process.execPath, [serverEntrypoint], {
    cwd: featureDirectory,
    env: {
      ...process.env,
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
