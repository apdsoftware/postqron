import { lstat, readdir, readFile, realpath } from 'node:fs/promises'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'
import { parse } from 'yaml'

export const FEATURE_SCHEMA_VERSION = 1

export type FeatureKind = 'api' | 'web' | 'worker'

export interface FeatureManifest {
  schema_version: number
  id: string
  kind: FeatureKind
  version: string
  entrypoint: string
  dependencies: string[]
  migrations: string[]
}

export interface DiscoveredFeature {
  directory: string
  manifest: FeatureManifest
  manifestPath: string
}

const featureIdPattern = /^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$/
const semverPattern = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/
const kinds = new Set<FeatureKind>(['api', 'web', 'worker'])

function stringArray(value: unknown, field: string, manifestPath: string): string[] {
  if (value === undefined) {
    return []
  }
  if (!Array.isArray(value) || value.some(item => typeof item !== 'string')) {
    throw new Error(`${manifestPath}: ${field} must be an array of strings`)
  }
  return [...value]
}

function parseManifest(value: unknown, manifestPath: string): FeatureManifest {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${manifestPath}: manifest must be a YAML mapping`)
  }

  const candidate = value as Record<string, unknown>
  const allowed = new Set([
    'schema_version',
    'id',
    'kind',
    'version',
    'entrypoint',
    'dependencies',
    'migrations',
  ])
  const unknown = Object.keys(candidate).filter(key => !allowed.has(key))
  if (unknown.length > 0) {
    throw new Error(`${manifestPath}: unknown fields: ${unknown.join(', ')}`)
  }

  if (candidate.schema_version !== FEATURE_SCHEMA_VERSION) {
    throw new Error(
      `${manifestPath}: schema_version must be ${FEATURE_SCHEMA_VERSION}`,
    )
  }
  if (typeof candidate.id !== 'string' || !featureIdPattern.test(candidate.id)) {
    throw new Error(`${manifestPath}: id is not a valid feature identifier`)
  }
  if (typeof candidate.kind !== 'string' || !kinds.has(candidate.kind as FeatureKind)) {
    throw new Error(`${manifestPath}: kind must be api, web, or worker`)
  }
  if (typeof candidate.version !== 'string' || !semverPattern.test(candidate.version)) {
    throw new Error(`${manifestPath}: version must be semantic versioning`)
  }
  if (typeof candidate.entrypoint !== 'string' || candidate.entrypoint.length === 0) {
    throw new Error(`${manifestPath}: entrypoint must be a non-empty path`)
  }

  return {
    schema_version: FEATURE_SCHEMA_VERSION,
    id: candidate.id,
    kind: candidate.kind as FeatureKind,
    version: candidate.version,
    entrypoint: candidate.entrypoint,
    dependencies: stringArray(candidate.dependencies, 'dependencies', manifestPath),
    migrations: stringArray(candidate.migrations, 'migrations', manifestPath),
  }
}

async function assertLocalFile(
  featureDirectory: string,
  configuredPath: string,
  field: string,
): Promise<void> {
  if (isAbsolute(configuredPath)) {
    throw new Error(`${field} must be relative to its feature directory`)
  }

  const resolved = resolve(featureDirectory, configuredPath)
  const pathWithinFeature = relative(featureDirectory, resolved)
  if (
    pathWithinFeature === '..'
    || pathWithinFeature.startsWith(`..${sep}`)
    || isAbsolute(pathWithinFeature)
  ) {
    throw new Error(`${field} escapes its feature directory`)
  }

  const file = await lstat(resolved).catch(() => undefined)
  if (!file?.isFile()) {
    throw new Error(`${field} does not point to a file: ${configuredPath}`)
  }
}

async function findManifestPaths(root: string): Promise<string[]> {
  const paths: string[] = []
  const visit = async (directory: string): Promise<void> => {
    const entries = await readdir(directory, { withFileTypes: true })
    entries.sort((left, right) => left.name.localeCompare(right.name))
    for (const entry of entries) {
      const path = join(directory, entry.name)
      if (entry.isDirectory()) {
        await visit(path)
      } else if (entry.isFile() && entry.name === 'feature.yaml') {
        paths.push(path)
      }
    }
  }
  await visit(root)
  return paths
}

export async function discoverFeatures(roots: string[]): Promise<DiscoveredFeature[]> {
  const features: DiscoveredFeature[] = []
  const ids = new Set<string>()

  for (const configuredRoot of roots) {
    const root = await realpath(configuredRoot)
    const manifestPaths = await findManifestPaths(root)
    for (const manifestPath of manifestPaths) {
      const source = await readFile(manifestPath, 'utf8')
      const manifest = parseManifest(parse(source), manifestPath)
      if (ids.has(manifest.id)) {
        throw new Error(`duplicate feature id: ${manifest.id}`)
      }

      const directory = dirname(manifestPath)
      await assertLocalFile(directory, manifest.entrypoint, `${manifest.id}.entrypoint`)
      for (const migration of manifest.migrations) {
        await assertLocalFile(directory, migration, `${manifest.id}.migrations`)
      }

      ids.add(manifest.id)
      features.push({ directory, manifest, manifestPath })
    }
  }

  return resolveFeatureOrder(features)
}

export function resolveFeatureOrder(features: DiscoveredFeature[]): DiscoveredFeature[] {
  const byId = new Map(features.map(feature => [feature.manifest.id, feature]))
  const permanent = new Set<string>()
  const visiting = new Set<string>()
  const ordered: DiscoveredFeature[] = []

  const visit = (feature: DiscoveredFeature): void => {
    const id = feature.manifest.id
    if (permanent.has(id)) {
      return
    }
    if (visiting.has(id)) {
      throw new Error(`feature dependency cycle includes ${id}`)
    }

    visiting.add(id)
    for (const dependency of [...feature.manifest.dependencies].sort()) {
      const target = byId.get(dependency)
      if (!target) {
        throw new Error(`${id} depends on missing feature ${dependency}`)
      }
      visit(target)
    }
    visiting.delete(id)
    permanent.add(id)
    ordered.push(feature)
  }

  for (const feature of [...features].sort((left, right) =>
    left.manifest.id.localeCompare(right.manifest.id))) {
    visit(feature)
  }
  return ordered
}

export function filterFeaturesByKind(
  features: DiscoveredFeature[],
  kinds: FeatureKind[],
): DiscoveredFeature[] {
  const requested = new Set(kinds)
  return features.filter(feature => requested.has(feature.manifest.kind))
}
