import assert from 'node:assert/strict'
import test from 'node:test'
import {
  assertSafeIdentifier,
  parseAdminSession,
  parseDashboard,
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
