import assert from 'node:assert/strict'
import test from 'node:test'
import { AdminApi } from '../core/api.ts'
import {
  auditQueryFromRoute,
  localDateTime,
  planQueryFromRoute,
  routeQuery,
  utcInstant,
} from '../core/list-query.ts'

test('plan and audit filters round-trip through shareable route query strings', () => {
  const plans = planQueryFromRoute({
    q: 'Studio',
    plan: 'pro',
    status: 'active',
    type: 'internal',
    from: '2026-07-01T00:00:00.000Z',
    to: '2026-07-31T23:59:59.000Z',
    sort: 'owner',
    direction: 'asc',
    page: '3',
  })
  assert.deepEqual(routeQuery({ ...plans }), {
    q: 'Studio',
    plan: 'pro',
    status: 'active',
    type: 'internal',
    from: '2026-07-01T00:00:00.000Z',
    to: '2026-07-31T23:59:59.000Z',
    sort: 'owner',
    direction: 'asc',
    page: '3',
  })

  const audit = auditQueryFromRoute({
    action: 'internal_plan.assign',
    actor: 'account-admin',
    subject: 'workspace-1',
    outcome: 'succeeded',
  })
  assert.equal(audit.sort, 'occurred_at')
  assert.equal(audit.direction, 'desc')
  assert.equal(audit.page, 1)
  assert.equal(localDateTime('2026-07-01T12:30:00Z'), '2026-07-01T12:30')
  assert.equal(utcInstant('2026-07-01T12:30'), '2026-07-01T12:30:00.000Z')
})

test('admin list client sends server filters and export URLs omit pagination', async () => {
  const paths: string[] = []
  const api = new AdminApi('https://api.example.test/', async (path) => {
    paths.push(path)
    if (path.startsWith('/api/v1/admin/plans?')) {
      return { items: [], pagination: { page: 2, page_size: 25, total: 30 } }
    }
    return { items: [], pagination: { page: 1, page_size: 25, total: 0 } }
  })
  await api.plans({
    q: 'Studio',
    plan: 'pro',
    page: 2,
    page_size: 25,
    sort: 'owner',
    direction: 'asc',
  })
  await api.audit({
    action: 'internal_plan.assign',
    page: 1,
    page_size: 25,
  })
  assert.match(paths[0] ?? '', /q=Studio/u)
  assert.match(paths[0] ?? '', /page=2/u)
  assert.match(paths[1] ?? '', /action=internal_plan.assign/u)

  const exportURL = api.plansExportURL({
    q: 'Studio',
    page: 3,
    page_size: 25,
  }, 'xlsx')
  assert.equal(
    exportURL,
    'https://api.example.test/api/v1/admin/plans/export?q=Studio&format=xlsx',
  )
})
