import { lstat, mkdir, symlink, unlink } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const repositoryRoot = resolve(featureRoot, '../..')
const localModules = resolve(featureRoot, 'node_modules')

await mkdir(localModules, { recursive: true })

for (const dependency of ['nuxt', 'vue']) {
  const target = resolve(repositoryRoot, 'apps/web/node_modules', dependency)
  const link = resolve(localModules, dependency)
  const existing = await lstat(link).catch(() => undefined)
  if (existing) {
    await unlink(link)
  }
  await symlink(target, link)
}
