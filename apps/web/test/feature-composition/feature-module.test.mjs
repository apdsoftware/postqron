import assert from 'node:assert/strict'
import { execFile, spawn } from 'node:child_process'
import { mkdir, mkdtemp, writeFile } from 'node:fs/promises'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import process from 'node:process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import {
  composeFeatureModules,
  discoverFeatureComposition,
} from '../../server/utils/feature-module.ts'

const execute = promisify(execFile)
const repositoryRoot = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../..',
)

test('discovers and composes a complete fixture web feature', async () => {
  const root = await mkdtemp(join(tmpdir(), 'postqron-web-feature-'))
  const directory = join(root, 'fixture')
  for (const path of [
    'pages/fixture.vue',
    'layouts/fixture.vue',
    'components/FixtureCard.vue',
    'plugins/fixture.ts',
    'middleware/fixture-auth.ts',
    'runtime.ts',
  ]) {
    const target = join(directory, path)
    await mkdir(join(target, '..'), { recursive: true })
    await writeFile(target, 'export default {}\n')
  }
  await writeFile(
    join(directory, 'feature.yaml'),
    [
      'schema_version: 1',
      'id: fixture-web',
      'kind: web',
      'version: 0.1.0',
      'entrypoints:',
      '  web: ./runtime.ts',
      'dependencies: []',
      'migrations: []',
      'web:',
      '  routes:',
      '    - name: fixture',
      '      path: /fixture',
      '      file: ./pages/fixture.vue',
      '      visibility: private',
      '      middleware: [fixture-auth]',
      '  layouts:',
      '    - name: fixture',
      '      file: ./layouts/fixture.vue',
      '  components: [./components]',
      '  plugins: [./plugins/fixture.ts]',
      '  middleware:',
      '    - name: fixture-auth',
      '      file: ./middleware/fixture-auth.ts',
      '',
    ].join('\n'),
  )

  const composition = await discoverFeatureComposition([root])

  assert.deepEqual(composition.features, [{ id: 'fixture-web', version: '0.1.0' }])
  assert.deepEqual(
    composition.routes.map(route => ({
      name: route.name,
      path: route.path,
      visibility: route.visibility,
      middleware: route.middleware,
      featureId: route.featureId,
    })),
    [{
      name: 'fixture',
      path: '/fixture',
      visibility: 'private',
      middleware: ['fixture-auth'],
      featureId: 'fixture-web',
    }],
  )
  assert.match(composition.components[0], /\/fixture\/components$/)
  assert.match(composition.plugins[0], /\/fixture\/runtime\.ts$/)
  assert.match(composition.plugins[1], /\/fixture\/plugins\/fixture\.ts$/)
  assert.equal(composition.layouts[0].name, 'fixture')
  assert.equal(composition.middleware[0].name, 'fixture-auth')
})

test('composes required roots while an explicitly optional root is absent', async () => {
  const parent = await mkdtemp(join(tmpdir(), 'postqron-web-feature-'))
  const root = join(parent, 'required')
  const directory = join(root, 'fixture')
  await mkdir(directory, { recursive: true })
  await writeFile(join(directory, 'runtime.ts'), 'export default {}\n')
  await writeFile(
    join(directory, 'feature.yaml'),
    [
      'schema_version: 1',
      'id: fixture-web',
      'kind: web',
      'version: 0.1.0',
      'entrypoint: ./runtime.ts',
      'dependencies: []',
      'migrations: []',
      '',
    ].join('\n'),
  )

  const composition = await discoverFeatureComposition([
    root,
    { path: join(parent, 'optional-missing'), optional: true },
  ])

  assert.deepEqual(composition.features, [{ id: 'fixture-web', version: '0.1.0' }])
})

test('rejects route collisions without a central registry', () => {
  const feature = id => ({
    directory: `/features/${id}`,
    manifestPath: `/features/${id}/feature.yaml`,
    manifest: {
      schema_version: 1,
      id,
      kind: 'web',
      version: '0.1.0',
      entrypoint: './runtime.ts',
      dependencies: [],
      migrations: [],
      server: { routes: [] },
      web: {
        routes: [{
          name: id,
          path: '/collision',
          file: './page.vue',
          visibility: 'public',
          middleware: [],
        }],
        layouts: [],
        components: [],
        plugins: [],
        middleware: [],
      },
    },
  })

  assert.throws(
    () => composeFeatureModules([feature('first'), feature('second')]),
    /web route collision "\/collision" between features "first" and "second"/,
  )
})

test('serves a discovered fixture through a generated Nuxt route', {
  timeout: 60_000,
}, async (context) => {
  const root = await mkdtemp(join(tmpdir(), 'postqron-web-full-stack-'))
  const directory = join(root, 'fixture')
  const files = {
    'runtime.ts': 'export default defineNuxtPlugin(() => {})\n',
    'pages/fixture.vue': [
      '<script setup lang="ts">',
      "definePageMeta({ layout: 'fixture' })",
      '</script>',
      '<template><main>runtime fixture reached <FixtureCard /></main></template>',
      '',
    ].join('\n'),
    'layouts/fixture.vue': '<template><div data-fixture-layout><slot /></div></template>\n',
    'components/FixtureCard.vue': '<template><span>component active</span></template>\n',
    'plugins/fixture.ts': 'export default defineNuxtPlugin(() => {})\n',
    'middleware/fixture-auth.ts': 'export default defineNuxtRouteMiddleware(() => {})\n',
  }
  for (const [path, source] of Object.entries(files)) {
    const target = join(directory, path)
    await mkdir(join(target, '..'), { recursive: true })
    await writeFile(target, source)
  }
  await writeFile(
    join(directory, 'feature.yaml'),
    [
      'schema_version: 1',
      'id: fixture-web',
      'kind: web',
      'version: 0.1.0',
      'entrypoints:',
      '  web: ./runtime.ts',
      'dependencies: []',
      'migrations: []',
      'web:',
      '  routes:',
      '    - name: fixture',
      '      path: /runtime-fixture',
      '      file: ./pages/fixture.vue',
      '      visibility: private',
      '      middleware: [fixture-auth]',
      '  layouts:',
      '    - name: fixture',
      '      file: ./layouts/fixture.vue',
      '  components: [./components]',
      '  plugins: [./plugins/fixture.ts]',
      '  middleware:',
      '    - name: fixture-auth',
      '      file: ./middleware/fixture-auth.ts',
      '',
    ].join('\n'),
  )

  const environment = {
    ...process.env,
    NUXT_TELEMETRY_DISABLED: '1',
    POSTQRON_FEATURE_ROOTS: root,
  }
  await execute(
    'pnpm',
    ['--filter', '@postqron/web', 'build'],
    { cwd: repositoryRoot, env: environment, maxBuffer: 20 * 1024 * 1024 },
  )

  const port = await availablePort()
  const server = spawn(
    process.execPath,
    ['apps/web/.output/server/index.mjs'],
    {
      cwd: repositoryRoot,
      env: {
        ...environment,
        HOST: '127.0.0.1',
        PORT: String(port),
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  )
  context.after(() => {
    if (!server.killed) {
      server.kill('SIGTERM')
    }
  })

  const response = await waitForResponse(
    `http://127.0.0.1:${port}/runtime-fixture`,
    server,
  )
  assert.equal(response.status, 200)
  const html = await response.text()
  assert.match(html, /runtime fixture reached/)
  assert.match(html, /component active/)
  assert.match(html, /data-fixture-layout/)
})

async function availablePort() {
  const server = createServer()
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  const address = server.address()
  assert(address && typeof address === 'object')
  await new Promise((resolve, reject) => {
    server.close(error => error ? reject(error) : resolve())
  })
  return address.port
}

async function waitForResponse(url, server) {
  let lastError
  for (let attempt = 0; attempt < 50; attempt += 1) {
    if (server.exitCode !== null) {
      throw new Error(`Nuxt fixture server exited with code ${server.exitCode}`)
    }
    try {
      return await globalThis.fetch(url)
    } catch (error) {
      lastError = error
      await new Promise(resolve => globalThis.setTimeout(resolve, 100))
    }
  }
  throw lastError
}
