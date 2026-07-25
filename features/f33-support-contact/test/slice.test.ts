import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const repositoryRoot = resolve(featureRoot, '../..')
const source = (path: string) => readFile(resolve(featureRoot, path), 'utf8')

test('the modular feature owns a public and localizable contact route', async () => {
  const manifest = await source('feature.yaml')
  assert.match(manifest, /^id: support-contact$/m)
  assert.match(
    manifest,
    /dependencies:\n[ ]{2}- i18n\n[ ]{2}- marketing-site/,
  )
  assert.match(manifest, /entrypoint: \.\/pages\/contatti\.vue/)
  assert.match(manifest, /path: \/contatti/)
  assert.match(manifest, /visibility: public/)
  assert.match(manifest, /file: \.\/pages\/contatti\.vue/)
})

test('the page is indexable, accessible, responsive, and has no submission form', async () => {
  const page = await source('pages/contatti.vue')
  assert.match(page, /useSeoMeta/)
  assert.match(page, /rel: 'canonical'/)
  assert.match(page, /hreflang/)
  assert.match(page, /robots: 'index, follow'/)
  assert.match(page, /'@type': 'ContactPage'/)
  assert.match(page, /<h1>/)
  assert.match(page, /<h2>/)
  assert.match(page, /:aria-label=/)
  assert.match(page, /@media \(max-width: 48rem\)/)
  assert.doesNotMatch(page, /<form\b/u)
  assert.doesNotMatch(page, /help@postqron\.com/)
})

test('the shared footer exposes localized contact and support links without duplicating email', async () => {
  const footer = await readFile(
    resolve(
      repositoryRoot,
      'features/f02-marketing-site/components/SiteFooter.vue',
    ),
    'utf8',
  )
  assert.match(footer, /useSupportContact/)
  assert.match(footer, /support\.localize\('\/contatti'\)/)
  assert.match(footer, /:href="support\.mailto\(\)"/)
  assert.match(footer, /footer\.supportLinkLabel/)
  assert.doesNotMatch(footer, /help@postqron\.com/)
})
