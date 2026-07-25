import { mkdir, mkdtemp, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { describe, expect, it } from 'vitest'
import {
  discoverFeatures,
  filterFeaturesByKind,
  resolveFeatureOrder,
  type DiscoveredFeature,
  type FeatureKind,
} from '../src/index.js'

async function createFeature(
  root: string,
  id: string,
  dependencies: string[] = [],
  kind: FeatureKind = 'web',
): Promise<void> {
  const directory = join(root, id)
  await mkdir(directory, { recursive: true })
  await writeFile(join(directory, 'entry.ts'), 'export default {}\\n')
  await writeFile(
    join(directory, 'feature.yaml'),
    [
      'schema_version: 1',
      `id: ${id}`,
      `kind: ${kind}`,
      'version: 0.1.0',
      'entrypoint: ./entry.ts',
      `dependencies: [${dependencies.join(', ')}]`,
      'migrations: []',
      '',
    ].join('\n'),
  )
}

describe('discoverFeatures', () => {
  it('discovers nested manifests without a registry', async () => {
    const root = await mkdtemp(join(tmpdir(), 'postqron-features-'))
    await createFeature(root, 'foundation')
    await createFeature(root, 'shell', ['foundation'])

    const features = await discoverFeatures([root])

    expect(features.map(feature => feature.manifest.id)).toEqual(['foundation', 'shell'])
    expect(resolveFeatureOrder(features).map(feature => feature.manifest.id)).toEqual([
      'foundation',
      'shell',
    ])
  })

  it('rejects missing dependencies', async () => {
    const root = await mkdtemp(join(tmpdir(), 'postqron-features-'))
    await createFeature(root, 'shell', ['missing'])

    await expect(discoverFeatures([root])).rejects.toThrow(
      'shell depends on missing feature missing',
    )
  })

  it('rejects dependency cycles', () => {
    const feature = (id: string, dependencies: string[]): DiscoveredFeature => ({
      directory: '/tmp',
      manifestPath: `/tmp/${id}/feature.yaml`,
      manifest: {
        schema_version: 1,
        id,
        kind: 'web',
        version: '0.1.0',
        entrypoint: './entry.ts',
        dependencies,
        migrations: [],
        server: { routes: [] },
        web: {
          routes: [],
          layouts: [],
          components: [],
          plugins: [],
          middleware: [],
        },
      },
    })

    expect(() =>
      resolveFeatureOrder([
        feature('first', ['second']),
        feature('second', ['first']),
      ])).toThrow('feature dependency cycle')
  })

  it('returns dependency order and filters host kinds without reordering', async () => {
    const root = await mkdtemp(join(tmpdir(), 'postqron-features-'))
    await createFeature(root, 'api-child', ['web-foundation'], 'api')
    await createFeature(root, 'web-foundation')
    await createFeature(root, 'worker-child', ['api-child'], 'worker')

    const features = await discoverFeatures([root])

    expect(features.map(feature => feature.manifest.id)).toEqual([
      'web-foundation',
      'api-child',
      'worker-child',
    ])
    expect(
      filterFeaturesByKind(features, ['api', 'worker'])
        .map(feature => feature.manifest.id),
    ).toEqual(['api-child', 'worker-child'])
  })

  it('validates and discovers a feature shared by server and web hosts', async () => {
    const root = await mkdtemp(join(tmpdir(), 'postqron-features-'))
    const directory = join(root, 'fixture')
    await mkdir(join(directory, 'pages'), { recursive: true })
    await mkdir(join(directory, 'layouts'), { recursive: true })
    await mkdir(join(directory, 'components'), { recursive: true })
    await mkdir(join(directory, 'plugins'), { recursive: true })
    await mkdir(join(directory, 'middleware'), { recursive: true })
    await Promise.all([
      writeFile(join(directory, 'server.go'), 'package fixture\n'),
      writeFile(join(directory, 'runtime.ts'), 'export default {}\n'),
      writeFile(join(directory, 'pages', 'fixture.vue'), '<template>fixture</template>\n'),
      writeFile(join(directory, 'layouts', 'default.vue'), '<template><slot /></template>\n'),
      writeFile(join(directory, 'components', 'Fixture.vue'), '<template>fixture</template>\n'),
      writeFile(join(directory, 'plugins', 'fixture.ts'), 'export default {}\n'),
      writeFile(join(directory, 'middleware', 'auth.ts'), 'export default {}\n'),
    ])
    await writeFile(
      join(directory, 'feature.yaml'),
      [
        'schema_version: 1',
        'id: fixture',
        'kind: api',
        'version: 0.1.0',
        'entrypoints:',
        '  server: ./server.go',
        '  web: ./runtime.ts',
        'dependencies: []',
        'migrations: []',
        'server:',
        '  routes:',
        '    - path: /fixture',
        '      handler: fixture',
        '      methods: [GET]',
        '      visibility: public',
        'web:',
        '  routes:',
        '    - name: fixture',
        '      path: /fixture',
        '      file: ./pages/fixture.vue',
        '      visibility: private',
        '      middleware: [auth]',
        '  layouts:',
        '    - name: default',
        '      file: ./layouts/default.vue',
        '  components: [./components]',
        '  plugins: [./plugins/fixture.ts]',
        '  middleware:',
        '    - name: auth',
        '      file: ./middleware/auth.ts',
        '',
      ].join('\n'),
    )

    const features = await discoverFeatures([root])
    expect(features).toHaveLength(1)
    const feature = features[0]!

    expect(filterFeaturesByKind([feature], ['api'])).toEqual([feature])
    expect(filterFeaturesByKind([feature], ['web'])).toEqual([feature])
    expect(feature.manifest.web.routes[0]).toMatchObject({
      path: '/fixture',
      visibility: 'private',
      middleware: ['auth'],
    })
  })
})
