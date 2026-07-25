import { resolve } from 'node:path'
import {
  discoverFeatures,
  filterFeaturesByKind,
  type DiscoveredFeature,
  type FeatureVisibility,
} from '@postqron/runtime'

export interface ComposedWebRoute {
  name: string
  path: string
  file: string
  middleware: string[]
  visibility: FeatureVisibility
  featureId: string
}

export interface ComposedWebLayout {
  name: string
  file: string
  featureId: string
}

export interface ComposedWebMiddleware {
  name: string
  path: string
  global: boolean
  featureId: string
}

export interface FeatureComposition {
  components: string[]
  entrypoints: string[]
  features: Array<{ id: string, version: string }>
  layouts: ComposedWebLayout[]
  middleware: ComposedWebMiddleware[]
  plugins: string[]
  routes: ComposedWebRoute[]
}

export async function discoverFeatureComposition(
  roots: string[],
): Promise<FeatureComposition> {
  const discovered = await discoverFeatures(roots)
  return composeFeatureModules(filterFeaturesByKind(discovered, ['web']))
}

export function composeFeatureModules(
  features: DiscoveredFeature[],
): FeatureComposition {
  const composition: FeatureComposition = {
    components: [],
    entrypoints: [],
    features: [],
    layouts: [],
    middleware: [],
    plugins: [],
    routes: [],
  }
  const routeOwners = new Map<string, string>()
  const layoutOwners = new Map<string, string>()
  const middlewareOwners = new Map<string, string>()

  for (const feature of features) {
    const { manifest } = feature
    const webEntrypoint = manifest.entrypoints?.web
      || (manifest.kind === 'web' ? manifest.entrypoint : undefined)
    if (!webEntrypoint) {
      continue
    }
    composition.features.push({ id: manifest.id, version: manifest.version })
    const resolvedEntrypoint = resolve(feature.directory, webEntrypoint)
    composition.entrypoints.push(resolvedEntrypoint)
    if (manifest.entrypoints?.web) {
      composition.plugins.push(resolvedEntrypoint)
    }

    for (const route of manifest.web.routes) {
      const collisionKey = route.path
      const owner = routeOwners.get(collisionKey)
      if (owner) {
        throw new Error(
          `web route collision ${JSON.stringify(route.path)} between features `
          + `${JSON.stringify(owner)} and ${JSON.stringify(manifest.id)}`,
        )
      }
      routeOwners.set(collisionKey, manifest.id)
      composition.routes.push({
        name: route.name || `${manifest.id}-${composition.routes.length}`,
        path: route.path,
        file: resolve(feature.directory, route.file),
        middleware: [...route.middleware],
        visibility: route.visibility,
        featureId: manifest.id,
      })
    }

    for (const layout of manifest.web.layouts) {
      const owner = layoutOwners.get(layout.name)
      if (owner) {
        throw new Error(
          `web layout collision ${JSON.stringify(layout.name)} between features `
          + `${JSON.stringify(owner)} and ${JSON.stringify(manifest.id)}`,
        )
      }
      layoutOwners.set(layout.name, manifest.id)
      composition.layouts.push({
        name: layout.name,
        file: resolve(feature.directory, layout.file),
        featureId: manifest.id,
      })
    }

    for (const middleware of manifest.web.middleware) {
      const owner = middlewareOwners.get(middleware.name)
      if (owner) {
        throw new Error(
          `web middleware collision ${JSON.stringify(middleware.name)} between features `
          + `${JSON.stringify(owner)} and ${JSON.stringify(manifest.id)}`,
        )
      }
      middlewareOwners.set(middleware.name, manifest.id)
      composition.middleware.push({
        name: middleware.name,
        path: resolve(feature.directory, middleware.file),
        global: middleware.global,
        featureId: manifest.id,
      })
    }

    composition.components.push(
      ...manifest.web.components.map(directory => resolve(feature.directory, directory)),
    )
    composition.plugins.push(
      ...manifest.web.plugins.map(plugin => resolve(feature.directory, plugin)),
    )
  }

  const middlewareNames = new Set(
    composition.middleware.map(middleware => middleware.name),
  )
  for (const route of composition.routes) {
    for (const middleware of route.middleware) {
      if (!middlewareNames.has(middleware)) {
        throw new Error(
          `web route ${JSON.stringify(route.path)} in feature `
          + `${JSON.stringify(route.featureId)} references missing middleware `
          + JSON.stringify(middleware),
        )
      }
    }
  }

  return composition
}
