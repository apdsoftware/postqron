import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

async function source(path: string) {
  return readFile(new URL(path, import.meta.url), 'utf8')
}

test('admin authorization runs only in the browser without forwarding SSR cookies', async () => {
  const middleware = await source('../middleware/admin-access.ts')

  assert.match(
    middleware,
    /if \(import\.meta\.server\) \{\s+return\s+\}/u,
  )
  assert.match(middleware, /await useAdminApi\(\)\.session\(\)/u)
  assert.doesNotMatch(middleware, /useRequestHeaders|\.session\(headers\)/u)
  assert.ok(
    middleware.indexOf('if (import.meta.server)')
      < middleware.indexOf('await useAdminApi().session()'),
  )
})

test('admin dashboard is requested client-side only after a valid session', async () => {
  const page = await source('../pages/admin.vue')

  assert.match(
    page,
    /if \(import\.meta\.client && session\.value\) \{\s+await loadDashboard\(\)\s+\}/u,
  )
  assert.match(page, /dashboard\.value = await api\.dashboard\(\)/u)
  assert.doesNotMatch(page, /useRequestHeaders|api\.dashboard\(headers\)/u)
})
