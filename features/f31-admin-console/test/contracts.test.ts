import assert from 'node:assert/strict'
import test from 'node:test'
import {
  assertSafeIdentifier,
  parseAdminSession,
  parseAuditList,
  parseDashboard,
  parsePlanList,
  parseSearchResults,
} from '../core/contracts.ts'

test('strict response contracts reject malformed or excessive administration data', () => {
  assert.throws(() => parseAdminSession({
    account: { id: '../admin', email: 'admin@example.test' },
    authenticated_at: '2026-07-25T12:00:00Z',
    csrf_token: 'csrf',
  }))
  assert.throws(() => parseDashboard({
    services: [],
    entitlements: [{
      workspace_id: 'workspace-1',
      plan_code: 'pro',
      internal: 'yes',
    }],
    recent_audit: [],
  }))
  assert.throws(() => parseSearchResults({
    users: [],
    workspaces: [{
      id: 'workspace-1',
      name: 'Name',
      owner_email: 'owner@example.test',
      member_count: -1,
    }],
  }))
  assert.throws(() => assertSafeIdentifier('../workspace'))
})

test('plan and audit list contracts keep only safe paginated projections', () => {
  const plan = parsePlanList({
    items: [{
      workspace_id: 'workspace-1',
      workspace_name: 'Studio',
      owner_email: 'owner@example.test',
      plan_code: 'pro',
      status: 'active',
      internal: false,
      usage: {
        members: { used: 1, limit: 5, remaining: 4, unlimited: false },
        channels: { used: 2, limit: 10, remaining: 8, unlimited: false },
        scheduled_publications: {
          used: 3,
          limit: 100,
          remaining: 97,
          unlimited: false,
        },
      },
      workspace_created_at: '2026-07-25T12:00:00Z',
      plan_updated_at: '2026-07-25T12:00:00Z',
      period_start: '2026-07-01T00:00:00Z',
      period_end: '2026-08-01T00:00:00Z',
      internal_assigned_at: null,
      payment_method: 'secret',
    }],
    pagination: { page: 1, page_size: 25, total: 1 },
    raw_payload: 'secret',
  })
  assert.equal(plan.items[0]?.workspace_name, 'Studio')
  assert.equal('payment_method' in (plan.items[0] ?? {}), false)
  assert.equal('raw_payload' in plan, false)

  const audit = parseAuditList({
    items: [{
      id: 'audit-event-1',
      code: 'internal_plan.assign',
      actor_id: 'account-admin',
      subject_id: 'workspace-1',
      reason: 'Approved operation',
      outcome: 'succeeded',
      correlation_id: 'correlation-1',
      occurred_at: '2026-07-25T12:00:00Z',
      request_payload: 'secret',
    }],
    pagination: { page: 1, page_size: 25, total: 1 },
  })
  assert.equal(audit.items[0]?.correlation_id, 'correlation-1')
  assert.equal('request_payload' in (audit.items[0] ?? {}), false)

  assert.throws(() => parsePlanList({
    items: [{
      ...plan.items[0],
      usage: {
        ...plan.items[0]?.usage,
        members: { used: 1, limit: 5, remaining: 4, unlimited: true },
      },
    }],
    pagination: { page: 1, page_size: 101, total: 1 },
  }))
})

test('dashboard service status is restricted to the operational vocabulary', () => {
  assert.throws(() => parseDashboard({
    services: [{ code: 'api', status: 'healthy', checked_at: '2026-07-25T12:00:00Z' }],
    entitlements: [],
    recent_audit: [],
  }))
  for (const status of ['operational', 'degraded', 'outage', 'unknown']) {
    const result = parseDashboard({
      services: [{ code: 'database', status, checked_at: '2026-07-25T12:00:00Z' }],
      entitlements: [],
      recent_audit: [],
    })
    assert.equal(result.services[0]?.status, status)
  }
})

test('search contract returns only minimum data fields', () => {
  const result = parseSearchResults({
    users: [{
      id: 'user-1',
      email: 'user@example.test',
      display_name: 'User',
      email_verified: true,
      oauth_token: 'secret',
    }],
    workspaces: [{
      id: 'workspace-1',
      name: 'Studio',
      owner_email: 'owner@example.test',
      member_count: 2,
      payment_card: 'secret',
    }],
  })
  assert.deepEqual(result, {
    users: [{
      id: 'user-1',
      email: 'user@example.test',
      display_name: 'User',
      email_verified: true,
    }],
    workspaces: [{
      id: 'workspace-1',
      name: 'Studio',
      owner_email: 'owner@example.test',
      member_count: 2,
    }],
  })
})
