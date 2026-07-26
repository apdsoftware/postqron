import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { ADMIN_CATALOGS } from '../core/catalogs.ts'
import { ADMIN_NAV_ITEMS } from '../components/nav.ts'

async function source(path: string) {
  return readFile(new URL(path, import.meta.url), 'utf8')
}

test('every sidebar entry has a dedicated page, catalog label, and route guard', async () => {
  assert.equal(ADMIN_NAV_ITEMS.length, 6)
  const files = {
    '/admin': 'admin.vue',
    '/admin/users': 'users.vue',
    '/admin/workspaces': 'workspaces.vue',
    '/admin/plans': 'plans.vue',
    '/admin/audit': 'audit.vue',
    '/admin/profile': 'profile.vue',
  } as const

  for (const item of ADMIN_NAV_ITEMS) {
    const file = files[item.path as keyof typeof files]
    assert.ok(file, `unexpected nav path ${item.path}`)
    for (const locale of ['en', 'it', 'es', 'fr', 'de'] as const) {
      assert.ok(
        ADMIN_CATALOGS[locale][item.labelKey],
        `${locale} is missing ${item.labelKey}`,
      )
    }
    const page = await source(`../pages/${file}`)
    assert.match(page, /middleware: 'admin-access'/u)
    assert.match(page, /layout: 'admin-console'/u)
  }
})

test('the sidebar marks the current route active and keeps native keyboard navigation', async () => {
  const layout = await source('../layouts/admin-console.vue')
  assert.match(layout, /aria-current="currentPath === item\.path \? 'page' : undefined"/u)
  assert.match(layout, /<nav :aria-label="t\('shell\.navigation'\)">/u)
  assert.doesNotMatch(layout, /tabindex="-?\d+"[^>]*>\s*<a/u)
  assert.match(layout, /@keydown\.esc="closeMenu"/u)
  assert.match(layout, /:aria-expanded="menuOpen"/u)
  assert.match(layout, /data-postqron-slot="admin-logout-action"/u)
})

test('the drawer toggle and scrim only render the mobile affordances declared once', async () => {
  const [layout, css] = await Promise.all([
    source('../layouts/admin-console.vue'),
    source('../admin-console.css'),
  ])
  assert.match(layout, /:data-open="menuOpen"/u)
  assert.match(layout, /class="admin-shell__scrim"/u)
  assert.match(css, /\.admin-sidebar\[data-open="true"\] \{/u)
  assert.match(css, /@media \(max-width: 48rem\) \{/u)
})
