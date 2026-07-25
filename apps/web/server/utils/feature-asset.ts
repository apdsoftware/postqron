import { readFile } from 'node:fs/promises'
import { delimiter, isAbsolute, relative, resolve, sep } from 'node:path'
import { createError } from 'h3'
import { useRuntimeConfig } from 'nitropack/runtime'

export async function readFeatureAsset(
  featureID: string,
  assetPath: string,
): Promise<Buffer> {
  if (
    !featureID
    || isAbsolute(assetPath)
    || assetPath === '..'
    || assetPath.startsWith(`..${sep}`)
  ) {
    throw createError({ statusCode: 500, statusMessage: 'Invalid feature asset' })
  }

  const config = useRuntimeConfig()
  const configuredRoots = process.env.POSTQRON_FEATURE_ROOTS || config.featureRoots
  const roots = configuredRoots.split(delimiter)
  for (const configuredRoot of roots) {
    const root = resolve(process.cwd(), configuredRoot)
    const featureDirectory = resolve(root, featureID)
    const candidate = resolve(featureDirectory, assetPath)
    const localPath = relative(featureDirectory, candidate)
    if (
      localPath === '..'
      || localPath.startsWith(`..${sep}`)
      || isAbsolute(localPath)
    ) {
      continue
    }
    const contents = await readFile(candidate).catch(() => undefined)
    if (contents) {
      return contents
    }
  }

  throw createError({ statusCode: 404, statusMessage: 'Feature asset not found' })
}
