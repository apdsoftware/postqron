import { mkdir, mkdtemp, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { describe, expect, it } from 'vitest'
import { discoverFeatures, resolveFeatureOrder, type DiscoveredFeature } from '../src/index.js'

async function createFeature(
  root: string,
  id: string,
  dependencies: string[] = [],
): Promise<void> {
  const directory = join(root, id)
  await mkdir(directory, { recursive: true })
  await writeFile(join(directory, 'entry.ts'), 'export default {}\\n')
  await writeFile(
    join(directory, 'feature.yaml'),
    [
      'schema_version: 1',
      `id: ${id}`,
      'kind: web',
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
      },
    })

    expect(() =>
      resolveFeatureOrder([
        feature('first', ['second']),
        feature('second', ['first']),
      ])).toThrow('feature dependency cycle')
  })
})
