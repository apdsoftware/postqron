import assert from 'node:assert/strict'
import test from 'node:test'
import { AdminApi, type AdminFetch } from '../core/api.ts'
import {
  parseUserDirectoryPage,
  parseWorkspaceDirectoryPage,
} from '../core/contracts.ts'
import {
  directoryRouteQuery,
  directorySearchParams,
  userDirectoryParamsFromQuery,
  workspaceDirectoryParamsFromQuery,
} from '../core/directory-query.ts'

const user = {
  id: 'account-1',
  email: 'user@example.test',
  display_name: 'Fixture User',
  account_status: 'active',
  email_verified: true,
  login_methods: ['password', 'google'],
  registered_at: '2026-07-01T10:00:00Z',
  last_login_at: '2026-07-26T09:30:00Z',
  active_sessions: 2,
  workspaces: [{
    id: 'workspace-1',
    name: 'Fixture Workspace',
    role: 'owner',
    plan_code: 'pro',
    plan_status: 'active',
  }],
  password_hash: 'must-not-survive',
  oauth_token: 'must-not-survive',
}

const workspace = {
  id: 'workspace-1',
  name: 'Fixture Workspace',
  owner_id: 'account-1',
  owner_email: 'user@example.test',
  owner_display_name: 'Fixture User',
  status: 'active',
  plan_code: 'pro',
  plan_status: 'active',
  member_count: 3,
  channel_count: 4,
  post_count: 18,
  created_at: '2026-07-01T10:00:00Z',
  updated_at: '2026-07-26T09:30:00Z',
  payment_method: 'must-not-survive',
}

test('directory contracts accept empty pages and strip non-authorized fields', () => {
  const users = parseUserDirectoryPage({
    items: [user],
    page: 2,
    page_size: 25,
    total: 30,
    sort: 'email',
    direction: 'asc',
  })
  assert.equal(users.total, 30)
  assert.equal('password_hash' in users.items[0]!, false)
  assert.equal('oauth_token' in users.items[0]!, false)

  const workspaces = parseWorkspaceDirectoryPage({
    items: [workspace],
    page: 1,
    page_size: 10,
    total: 1,
    sort: 'updated_at',
    direction: 'desc',
  })
  assert.equal(workspaces.items[0]?.channel_count, 4)
  assert.equal('payment_method' in workspaces.items[0]!, false)

  assert.deepEqual(parseUserDirectoryPage({
    items: [],
    page: 1,
    page_size: 25,
    total: 0,
    sort: 'registered_at',
    direction: 'desc',
  }).items, [])
})

test('combined filters, ordering, and pagination round-trip through shareable URLs', () => {
  const users = userDirectoryParamsFromQuery({
    q: '  fixture  ',
    status: 'locked',
    email_verified: 'false',
    plan: 'team',
    login_method: 'linkedin',
    registered_from: '2026-01-01',
    registered_to: '2026-07-31',
    last_login_from: '2026-06-01',
    page: '3',
    page_size: '50',
    sort: 'last_login_at',
    direction: 'asc',
  })
  assert.deepEqual(users, {
    q: 'fixture',
    status: 'locked',
    email_verified: false,
    plan: 'team',
    login_method: 'linkedin',
    registered_from: '2026-01-01',
    registered_to: '2026-07-31',
    last_login_from: '2026-06-01',
    page: 3,
    page_size: 50,
    sort: 'last_login_at',
    direction: 'asc',
  })
  assert.equal(
    directorySearchParams(users).get('email_verified'),
    'false',
  )
  const exportQuery = directorySearchParams(users, false)
  assert.equal(exportQuery.has('page'), false)
  assert.equal(exportQuery.has('page_size'), false)
  assert.equal(directoryRouteQuery(users).page, '3')

  assert.deepEqual(workspaceDirectoryParamsFromQuery({
    owner: 'owner@example.test',
    status: 'active',
    plan: 'pro',
    page: 'invalid',
    page_size: '999',
  }), {
    owner: 'owner@example.test',
    status: 'active',
    plan: 'pro',
    page: 1,
    page_size: 25,
    sort: 'updated_at',
    direction: 'desc',
  })
})

test('admin API requests only the selected server page and exports all filtered rows', async () => {
  const calls: Array<{
    path: string
    options?: Readonly<Record<string, unknown>>
  }> = []
  const fetch: AdminFetch = async (path, options) => {
    calls.push({ path, options })
    if (path.includes('/export?')) {
      return new Blob(['fixture'])
    }
    if (path.startsWith('/api/v1/admin/users/')) {
      return user
    }
    if (path.startsWith('/api/v1/admin/users?')) {
      return {
        items: [user],
        page: 4,
        page_size: 10,
        total: 91,
        sort: 'email',
        direction: 'asc',
      }
    }
    return {
      items: [workspace],
      page: 1,
      page_size: 25,
      total: 1,
      sort: 'updated_at',
      direction: 'desc',
    }
  }
  const api = new AdminApi('https://admin.postqron.test', fetch)
  const users = await api.users({
    q: 'fixture',
    page: 4,
    page_size: 10,
    sort: 'email',
    direction: 'asc',
  })
  assert.equal(users.items.length, 1)
  assert.equal(users.total, 91)
  assert.match(calls[0]!.path, /page=4/u)
  assert.match(calls[0]!.path, /page_size=10/u)

  assert.equal((await api.user('account-1')).email, 'user@example.test')
  await api.workspaces({ page: 1, page_size: 25 })
  const exported = await api.exportUsers({
    q: 'fixture',
    page: 4,
    page_size: 10,
  }, 'xlsx')
  assert.equal(exported.filename, 'postqron-admin-users.xlsx')
  const exportCall = calls.at(-1)!
  assert.match(exportCall.path, /format=xlsx/u)
  assert.doesNotMatch(exportCall.path, /page(?:_size)?=/u)
  assert.equal(exportCall.options?.responseType, 'blob')
})
