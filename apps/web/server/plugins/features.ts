import { delimiter, resolve } from 'node:path'
import { discoverFeatures } from '@postqron/runtime'
import { defineNitroPlugin, useRuntimeConfig } from 'nitropack/runtime'

export default defineNitroPlugin(async (nitroApp) => {
  const config = useRuntimeConfig()
  const configuredRoots = process.env.POSTQRON_FEATURE_ROOTS || config.featureRoots
  const roots = configuredRoots
    .split(delimiter)
    .map(root => resolve(process.cwd(), root))
  const features = await discoverFeatures(roots)

  nitroApp.hooks.hook('request', (event) => {
    event.context.features = features.map(feature => ({
      id: feature.manifest.id,
      kind: feature.manifest.kind,
      version: feature.manifest.version,
    }))
  })
})
