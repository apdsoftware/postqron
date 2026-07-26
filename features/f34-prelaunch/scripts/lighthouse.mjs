import { spawn, spawnSync } from 'node:child_process'
import {
  mkdir,
  readFile,
} from 'node:fs/promises'
import process from 'node:process'
import {
  URL,
  fileURLToPath,
} from 'node:url'

const featureDirectory = fileURLToPath(new URL('../', import.meta.url))
const reportDirectory = fileURLToPath(new URL('../.lighthouse/', import.meta.url))
const reportPath = `${reportDirectory}report.json`
const serverEntrypoint = fileURLToPath(
  new URL('../../../apps/web/.output/server/index.mjs', import.meta.url),
)
const url = 'http://127.0.0.1:41736/en/prelaunch'

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

const server = spawn(process.execPath, [serverEntrypoint], {
  cwd: featureDirectory,
  env: {
    ...process.env,
    HOST: '127.0.0.1',
    PORT: '41736',
    PRELAUNCH_MODE: 'true',
  },
  stdio: 'inherit',
})

try {
  let ready = false
  for (let attempt = 0; attempt < 60; attempt++) {
    try {
      const response = await globalThis.fetch(url)
      ready = response.ok
    } catch {
      ready = false
    }
    if (ready) {
      break
    }
    await new Promise(resolve => globalThis.setTimeout(resolve, 500))
  }
  if (!ready) {
    throw new Error('pre-launch preview did not become ready')
  }

  await mkdir(reportDirectory, { recursive: true })
  const lighthouse = spawnSync(
    'pnpm',
    [
      'exec',
      'lighthouse',
      url,
      '--only-categories=performance,accessibility,seo',
      '--form-factor=mobile',
      '--screenEmulation.mobile=true',
      '--chrome-flags=--headless --no-sandbox',
      '--output=json',
      `--output-path=${reportPath}`,
      '--quiet',
    ],
    {
      cwd: featureDirectory,
      env: {
        ...process.env,
        CHROME_PATH: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
      },
      stdio: 'inherit',
    },
  )
  if (lighthouse.status !== 0) {
    process.exit(lighthouse.status ?? 1)
  }

  const report = JSON.parse(await readFile(reportPath, 'utf8'))
  const scores = {
    performance: report.categories.performance.score,
    accessibility: report.categories.accessibility.score,
    seo: report.categories.seo.score,
  }
  const minimum = { performance: 0.8, accessibility: 0.9, seo: 1 }
  for (const [category, score] of Object.entries(scores)) {
    if (score < minimum[category]) {
      throw new Error(
        `${category} Lighthouse score ${score} is below ${minimum[category]}`,
      )
    }
  }
  process.stdout.write(`Lighthouse mobile scores: ${JSON.stringify(scores)}\n`)
} finally {
  server.kill('SIGTERM')
}
