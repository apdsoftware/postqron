import { lstat, readdir, readFile, realpath } from 'node:fs/promises'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'
import { parse } from 'yaml'

export const FEATURE_SCHEMA_VERSION = 1

export type FeatureKind = 'api' | 'web' | 'worker'
export type FeatureVisibility = 'public' | 'private'

export interface FeatureEntrypoints {
  server?: string
  web?: string
}

export interface ServerRoute {
  path: string
  handler: string
  methods: string[]
  visibility: FeatureVisibility
}

export interface WebRoute {
  name?: string
  path: string
  file: string
  visibility: FeatureVisibility
  middleware: string[]
}

export interface WebLayout {
  name: string
  file: string
}

export interface WebMiddleware {
  name: string
  file: string
  global: boolean
}

export interface ServerModule {
  routes: ServerRoute[]
}

export interface WebModule {
  routes: WebRoute[]
  layouts: WebLayout[]
  components: string[]
  plugins: string[]
  middleware: WebMiddleware[]
}

export interface FeatureManifest {
  schema_version: number
  id: string
  kind: FeatureKind
  version: string
  entrypoint?: string
  entrypoints?: FeatureEntrypoints
  dependencies: string[]
  migrations: string[]
  required?: boolean
  server: ServerModule
  web: WebModule
}

export interface DiscoveredFeature {
  directory: string
  manifest: FeatureManifest
  manifestPath: string
}

export interface FeatureRoot {
  path: string
  optional?: boolean
}

export type FeatureRootInput = string | FeatureRoot

const featureIdPattern = /^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$/
const semverPattern = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/
const kinds = new Set<FeatureKind>(['api', 'web', 'worker'])
const visibility = new Set<FeatureVisibility>(['public', 'private'])
const httpMethodPattern = /^(?:GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS)$/
const identifierPattern = /^[A-Za-z][A-Za-z0-9_.-]*$/
const layoutNamePattern = /^[a-z][a-z0-9-]*$/

function stringArray(value: unknown, field: string, manifestPath: string): string[] {
  if (value === undefined) {
    return []
  }
  if (!Array.isArray(value) || value.some(item => typeof item !== 'string')) {
    throw new Error(`${manifestPath}: ${field} must be an array of strings`)
  }
  return [...value]
}

function mapping(
  value: unknown,
  field: string,
  manifestPath: string,
): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${manifestPath}: ${field} must be a YAML mapping`)
  }
  return value as Record<string, unknown>
}

function assertKnownFields(
  candidate: Record<string, unknown>,
  allowed: string[],
  field: string,
  manifestPath: string,
): void {
  const known = new Set(allowed)
  const unknown = Object.keys(candidate).filter(key => !known.has(key))
  if (unknown.length > 0) {
    throw new Error(`${manifestPath}: ${field} has unknown fields: ${unknown.join(', ')}`)
  }
}

function parseVisibility(
  value: unknown,
  field: string,
  manifestPath: string,
): FeatureVisibility {
  if (typeof value !== 'string' || !visibility.has(value as FeatureVisibility)) {
    throw new Error(`${manifestPath}: ${field} must be public or private`)
  }
  return value as FeatureVisibility
}

function parseServerRoute(
  value: unknown,
  index: number,
  manifestPath: string,
): ServerRoute {
  const field = `server.routes[${index}]`
  const route = mapping(value, field, manifestPath)
  assertKnownFields(route, ['path', 'handler', 'methods', 'visibility'], field, manifestPath)
  validateRoutePath(route.path, `${field}.path`, manifestPath)
  if ((route.path as string).startsWith('/api/v1')) {
    throw new Error(`${manifestPath}: ${field}.path must be relative to the /api/v1 mount`)
  }
  if (typeof route.handler !== 'string' || !identifierPattern.test(route.handler)) {
    throw new Error(`${manifestPath}: ${field}.handler is not a valid handler identifier`)
  }
  const methods = stringArray(route.methods, `${field}.methods`, manifestPath)
  if (
    methods.length === 0
    || methods.some(method => !httpMethodPattern.test(method))
    || new Set(methods).size !== methods.length
  ) {
    throw new Error(`${manifestPath}: ${field}.methods must contain unique supported HTTP methods`)
  }
  return {
    path: route.path as string,
    handler: route.handler,
    methods,
    visibility: parseVisibility(route.visibility, `${field}.visibility`, manifestPath),
  }
}

function parseWebRoute(
  value: unknown,
  index: number,
  manifestPath: string,
): WebRoute {
  const field = `web.routes[${index}]`
  const route = mapping(value, field, manifestPath)
  assertKnownFields(
    route,
    ['name', 'path', 'file', 'visibility', 'middleware'],
    field,
    manifestPath,
  )
  validateRoutePath(route.path, `${field}.path`, manifestPath)
  if (route.name !== undefined && (
    typeof route.name !== 'string' || !identifierPattern.test(route.name)
  )) {
    throw new Error(`${manifestPath}: ${field}.name is not a valid route identifier`)
  }
  if (typeof route.file !== 'string' || route.file.length === 0) {
    throw new Error(`${manifestPath}: ${field}.file must be a non-empty path`)
  }
  const routeVisibility = parseVisibility(
    route.visibility,
    `${field}.visibility`,
    manifestPath,
  )
  const middleware = stringArray(route.middleware, `${field}.middleware`, manifestPath)
  if (middleware.some(name => !layoutNamePattern.test(name))) {
    throw new Error(`${manifestPath}: ${field}.middleware contains an invalid name`)
  }
  if (routeVisibility === 'private' && middleware.length === 0) {
    throw new Error(`${manifestPath}: ${field} private routes require explicit middleware`)
  }
  return {
    name: route.name as string | undefined,
    path: route.path as string,
    file: route.file,
    visibility: routeVisibility,
    middleware,
  }
}

function validateRoutePath(value: unknown, field: string, manifestPath: string): void {
  if (
    typeof value !== 'string'
    || !value.startsWith('/')
    || value.includes('?')
    || value.includes('#')
  ) {
    throw new Error(`${manifestPath}: ${field} must start with / and omit query/fragment`)
  }
}

function parseWebLayout(
  value: unknown,
  index: number,
  manifestPath: string,
): WebLayout {
  const field = `web.layouts[${index}]`
  const layout = mapping(value, field, manifestPath)
  assertKnownFields(layout, ['name', 'file'], field, manifestPath)
  if (typeof layout.name !== 'string' || !layoutNamePattern.test(layout.name)) {
    throw new Error(`${manifestPath}: ${field}.name is not valid`)
  }
  if (typeof layout.file !== 'string' || layout.file.length === 0) {
    throw new Error(`${manifestPath}: ${field}.file must be a non-empty path`)
  }
  return { name: layout.name, file: layout.file }
}

function parseWebMiddleware(
  value: unknown,
  index: number,
  manifestPath: string,
): WebMiddleware {
  const field = `web.middleware[${index}]`
  const middleware = mapping(value, field, manifestPath)
  assertKnownFields(middleware, ['name', 'file', 'global'], field, manifestPath)
  if (
    typeof middleware.name !== 'string'
    || !layoutNamePattern.test(middleware.name)
  ) {
    throw new Error(`${manifestPath}: ${field}.name is not valid`)
  }
  if (typeof middleware.file !== 'string' || middleware.file.length === 0) {
    throw new Error(`${manifestPath}: ${field}.file must be a non-empty path`)
  }
  if (middleware.global !== undefined && typeof middleware.global !== 'boolean') {
    throw new Error(`${manifestPath}: ${field}.global must be boolean`)
  }
  return {
    name: middleware.name,
    file: middleware.file,
    global: middleware.global === true,
  }
}

function parseObjectArray<T>(
  value: unknown,
  field: string,
  manifestPath: string,
  parseItem: (item: unknown, index: number, manifestPath: string) => T,
): T[] {
  if (value === undefined) {
    return []
  }
  if (!Array.isArray(value)) {
    throw new Error(`${manifestPath}: ${field} must be an array`)
  }
  return value.map((item, index) => parseItem(item, index, manifestPath))
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
    'entrypoints',
    'dependencies',
    'migrations',
    'required',
    'server',
    'web',
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
  if (candidate.required !== undefined && typeof candidate.required !== 'boolean') {
    throw new Error(`${manifestPath}: required must be boolean`)
  }

  let entrypoints: FeatureEntrypoints | undefined
  if (candidate.entrypoints !== undefined) {
    const configured = mapping(candidate.entrypoints, 'entrypoints', manifestPath)
    assertKnownFields(configured, ['server', 'web'], 'entrypoints', manifestPath)
    for (const [host, entrypoint] of Object.entries(configured)) {
      if (typeof entrypoint !== 'string' || entrypoint.length === 0) {
        throw new Error(`${manifestPath}: entrypoints.${host} must be a non-empty path`)
      }
    }
    entrypoints = {
      server: configured.server as string | undefined,
      web: configured.web as string | undefined,
    }
    if (!entrypoints.server && !entrypoints.web) {
      throw new Error(`${manifestPath}: entrypoints must declare server or web`)
    }
  }
  if (candidate.entrypoint !== undefined && (
    typeof candidate.entrypoint !== 'string' || candidate.entrypoint.length === 0
  )) {
    throw new Error(`${manifestPath}: entrypoint must be a non-empty path`)
  }
  if (candidate.entrypoint !== undefined && entrypoints !== undefined) {
    throw new Error(`${manifestPath}: entrypoint cannot be combined with entrypoints`)
  }
  if (candidate.entrypoint === undefined && entrypoints === undefined) {
    throw new Error(`${manifestPath}: entrypoint or entrypoints.server/web is required`)
  }

  const serverCandidate = candidate.server === undefined
    ? {}
    : mapping(candidate.server, 'server', manifestPath)
  assertKnownFields(serverCandidate, ['routes'], 'server', manifestPath)
  const server: ServerModule = {
    routes: parseObjectArray(
      serverCandidate.routes,
      'server.routes',
      manifestPath,
      parseServerRoute,
    ),
  }

  const webCandidate = candidate.web === undefined
    ? {}
    : mapping(candidate.web, 'web', manifestPath)
  assertKnownFields(
    webCandidate,
    ['routes', 'layouts', 'components', 'plugins', 'middleware'],
    'web',
    manifestPath,
  )
  const web: WebModule = {
    routes: parseObjectArray(webCandidate.routes, 'web.routes', manifestPath, parseWebRoute),
    layouts: parseObjectArray(
      webCandidate.layouts,
      'web.layouts',
      manifestPath,
      parseWebLayout,
    ),
    components: stringArray(webCandidate.components, 'web.components', manifestPath),
    plugins: stringArray(webCandidate.plugins, 'web.plugins', manifestPath),
    middleware: parseObjectArray(
      webCandidate.middleware,
      'web.middleware',
      manifestPath,
      parseWebMiddleware,
    ),
  }

  const dependencies = stringArray(candidate.dependencies, 'dependencies', manifestPath)
  for (const dependency of dependencies) {
    if (!featureIdPattern.test(dependency)) {
      throw new Error(
        `${manifestPath}: dependency ${JSON.stringify(dependency)} is not a valid feature identifier`,
      )
    }
  }

  return {
    schema_version: FEATURE_SCHEMA_VERSION,
    id: candidate.id,
    kind: candidate.kind as FeatureKind,
    version: candidate.version,
    entrypoint: candidate.entrypoint as string | undefined,
    entrypoints,
    dependencies,
    migrations: stringArray(candidate.migrations, 'migrations', manifestPath),
    required: candidate.required as boolean | undefined,
    server,
    web,
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

async function assertLocalDirectory(
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

  const directory = await lstat(resolved).catch(() => undefined)
  if (!directory?.isDirectory()) {
    throw new Error(`${field} does not point to a directory: ${configuredPath}`)
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

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function errorCode(error: unknown): string | undefined {
  if (!error || typeof error !== 'object' || !('code' in error)) {
    return undefined
  }
  return typeof error.code === 'string' ? error.code : undefined
}

export async function discoverFeatures(
  roots: FeatureRootInput[],
): Promise<DiscoveredFeature[]> {
  const features: DiscoveredFeature[] = []
  const ids = new Set<string>()

  for (const configuredRoot of roots) {
    const rootConfiguration = typeof configuredRoot === 'string'
      ? { path: configuredRoot, optional: false }
      : { path: configuredRoot.path, optional: configuredRoot.optional === true }

    let root: string
    try {
      root = await realpath(rootConfiguration.path)
    } catch (error) {
      if (rootConfiguration.optional && errorCode(error) === 'ENOENT') {
        continue
      }
      throw new Error(
        `feature root ${JSON.stringify(rootConfiguration.path)} is not accessible: `
        + describeError(error),
        { cause: error },
      )
    }

    let manifestPaths: string[]
    try {
      manifestPaths = await findManifestPaths(root)
    } catch (error) {
      throw new Error(
        `feature root ${JSON.stringify(rootConfiguration.path)} cannot be scanned: `
        + describeError(error),
        { cause: error },
      )
    }
    for (const manifestPath of manifestPaths) {
      const source = await readFile(manifestPath, 'utf8')
      const manifest = parseManifest(parse(source), manifestPath)
      if (ids.has(manifest.id)) {
        throw new Error(`duplicate feature id: ${manifest.id}`)
      }

      const directory = dirname(manifestPath)
      for (const [host, entrypoint] of Object.entries(featureEntrypoints(manifest))) {
        await assertLocalFile(directory, entrypoint, `${manifest.id}.entrypoints.${host}`)
      }
      if (manifest.kind === 'worker' && manifest.entrypoint) {
        await assertLocalFile(directory, manifest.entrypoint, `${manifest.id}.entrypoint`)
      }
      for (const migration of manifest.migrations) {
        await assertLocalFile(directory, migration, `${manifest.id}.migrations`)
      }
      for (const route of manifest.web.routes) {
        await assertLocalFile(directory, route.file, `${manifest.id}.web route ${route.path}`)
      }
      for (const layout of manifest.web.layouts) {
        await assertLocalFile(directory, layout.file, `${manifest.id}.web layout ${layout.name}`)
      }
      for (const componentDirectory of manifest.web.components) {
        await assertLocalDirectory(
          directory,
          componentDirectory,
          `${manifest.id}.web.components`,
        )
      }
      for (const plugin of manifest.web.plugins) {
        await assertLocalFile(directory, plugin, `${manifest.id}.web.plugins`)
      }
      for (const middleware of manifest.web.middleware) {
        await assertLocalFile(
          directory,
          middleware.file,
          `${manifest.id}.web.middleware.${middleware.name}`,
        )
      }

      if (manifest.server.routes.length > 0 && featureEntrypoints(manifest).server === undefined) {
        throw new Error(`${manifestPath}: server routes require entrypoints.server`)
      }
      const hasWebComposition = Object.values(manifest.web)
        .some(items => items.length > 0)
      if (hasWebComposition && featureEntrypoints(manifest).web === undefined) {
        throw new Error(`${manifestPath}: web composition requires entrypoints.web`)
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
  return features.filter(feature =>
    [...requested].some(kind => featureSupportsKind(feature.manifest, kind)))
}

export function featureEntrypoints(
  manifest: FeatureManifest,
): FeatureEntrypoints {
  if (manifest.entrypoints) {
    const entrypoints: FeatureEntrypoints = {}
    if (manifest.entrypoints.server) {
      entrypoints.server = manifest.entrypoints.server
    }
    if (manifest.entrypoints.web) {
      entrypoints.web = manifest.entrypoints.web
    }
    return entrypoints
  }
  if (manifest.kind === 'api') {
    return { server: manifest.entrypoint }
  }
  if (manifest.kind === 'web') {
    return { web: manifest.entrypoint }
  }
  return {}
}

export function featureSupportsKind(
  manifest: FeatureManifest,
  kind: FeatureKind,
): boolean {
  const entrypoints = featureEntrypoints(manifest)
  if (kind === 'api') {
    return entrypoints.server !== undefined
  }
  if (kind === 'web') {
    return entrypoints.web !== undefined
  }
  return manifest.kind === 'worker'
}

export function isFeatureRequired(manifest: FeatureManifest): boolean {
  return manifest.required !== false
}
