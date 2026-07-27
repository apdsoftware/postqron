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

test('the login gate lives in the shared layout so every admin route is protected', async () => {
  const layout = await source('../layouts/admin-console.vue')

  assert.match(layout, /await api\.session\(\)/u)
  assert.match(layout, /<template v-if="session">/u)
  assert.match(layout, /<main\s+v-else/u)
  assert.doesNotMatch(layout, /useRequestHeaders/u)
})

test('logout is server-side, always visible in the top bar, and clears client state only after success', async () => {
  const [layout, logout, api] = await Promise.all([
    source('../layouts/admin-console.vue'),
    source('../components/AdminLogoutButton.vue'),
    source('../core/api.ts'),
  ])

  assert.match(layout, /<AdminLogoutButton/u)
  assert.match(logout, /await api\.logout\(session\.value\.csrf_token\)/u)
  assert.ok(
    logout.indexOf('await api.logout(session.value.csrf_token)')
      < logout.indexOf('session.value = undefined'),
  )
  assert.match(api, /#request\('\/api\/v1\/auth\/logout'/u)
  assert.match(api, /'X-CSRF-Token': csrfToken/u)
})

test('profile password form uses the authenticated CSRF token and never retains submitted secrets', async () => {
  const [profile, api] = await Promise.all([
    source('../pages/admin-profile.vue'),
    source('../core/api.ts'),
  ])

  assert.match(profile, /autocomplete="current-password"/u)
  assert.equal(profile.match(/autocomplete="new-password"/gu)?.length, 2)
  assert.match(profile, /await api\.changePassword\(\{/u)
  assert.match(profile, /csrfToken: session\.value\.csrf_token/u)
  assert.match(profile, /session\.value = await api\.session\(\)/u)
  assert.match(profile, /clearPasswords\(\)/u)
  assert.match(api, /current_password: input\.currentPassword/u)
  assert.match(api, /confirmation: input\.confirmation/u)
  assert.doesNotMatch(profile, /console\.(?:log|error)|localStorage|sessionStorage/u)
})

test('section data is only requested client-side after a valid session is present', async () => {
  const loader = await source('../components/use-admin-section.ts')

  assert.match(
    loader,
    /if \(!import\.meta\.client \|\| !session\.value\) \{/u,
  )
  assert.match(loader, /state\.value = await load\(\)/u)
  assert.doesNotMatch(loader, /useRequestHeaders/u)

  const dashboardPage = await source('../pages/admin.vue')
  assert.match(dashboardPage, /useAdminSectionLoad\(\s*dashboard,\s*\(\) => api\.dashboard\(\),?\s*\)/u)
})
