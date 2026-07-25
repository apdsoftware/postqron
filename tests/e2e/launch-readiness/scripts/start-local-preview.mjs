import { spawn, spawnSync } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

const suiteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const repositoryRoot = resolve(suiteRoot, '../../..')
const output = resolve(repositoryRoot, 'apps/web/.output/server/index.mjs')
const fixture = resolve(suiteRoot, 'fixtures/fixture-api.mjs')
const proxy = resolve(suiteRoot, 'fixtures/preview-proxy.mjs')
const common = {
  ...process.env,
  NODE_ENV: 'production',
  POSTQRON_API_BASE: 'http://127.0.0.1:41797',
  NUXT_PUBLIC_API_BASE: 'http://127.0.0.1:41795',
  NUXT_PUBLIC_SITE_URL: 'http://127.0.0.1:41795',
  NUXT_PUBLIC_APP_URL: '/app',
  NUXT_PUBLIC_PADDLE_CLIENT_TOKEN: 'test_fixturepublic',
  NUXT_PUBLIC_SUPPORT_EMAIL: 'help@postqron.com',
}

const build = spawnSync('pnpm', ['--filter', '@postqron/web', 'build'], {
  cwd: repositoryRoot,
  env: { ...common, PRELAUNCH_MODE: 'false' },
  stdio: 'inherit',
})
if (build.status !== 0) {
  process.exit(build.status ?? 1)
}

const supervised = {
  ...common,
  LAUNCH_SUPERVISOR_PID: String(process.pid),
}
const children = [
  spawn(process.execPath, [fixture], {
    cwd: repositoryRoot,
    env: supervised,
    stdio: 'inherit',
  }),
  spawn(process.execPath, [output], {
    cwd: repositoryRoot,
    env: {
      ...common,
      HOST: '127.0.0.1',
      PORT: '41805',
      PRELAUNCH_MODE: 'false',
    },
    stdio: 'inherit',
  }),
  spawn(process.execPath, [output], {
    cwd: repositoryRoot,
    env: {
      ...common,
      HOST: '127.0.0.1',
      PORT: '41806',
      PRELAUNCH_MODE: 'true',
      NUXT_PUBLIC_SITE_URL: 'http://127.0.0.1:41796',
    },
    stdio: 'inherit',
  }),
  spawn(process.execPath, [proxy], {
    cwd: repositoryRoot,
    env: {
      ...supervised,
      LAUNCH_PROXY_PORT: '41795',
      LAUNCH_WEB_PORT: '41805',
    },
    stdio: 'inherit',
  }),
  spawn(process.execPath, [proxy], {
    cwd: repositoryRoot,
    env: {
      ...supervised,
      LAUNCH_PROXY_PORT: '41796',
      LAUNCH_WEB_PORT: '41806',
    },
    stdio: 'inherit',
  }),
]

let stopping = false
let exitCode = 0
let remainingChildren = children.length
function stop(signal = 'SIGTERM') {
  if (stopping) {
    return
  }
  stopping = true
  for (const child of children) {
    if (!child.killed) {
      child.kill(signal)
    }
  }
}
for (const signal of ['SIGHUP', 'SIGINT', 'SIGTERM']) {
  process.once(signal, () => {
    exitCode = 0
    stop(signal)
    setTimeout(() => process.exit(exitCode), 2_000)
  })
}
process.once('exit', () => stop('SIGTERM'))
for (const child of children) {
  child.once('exit', code => {
    remainingChildren -= 1
    if (!stopping) {
      exitCode = code ?? 0
      stop('SIGTERM')
    }
    if (stopping && remainingChildren === 0) {
      process.exit(exitCode)
    }
  })
}
await new Promise(() => {})
