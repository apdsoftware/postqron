import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  AppApiError,
  AppShellApi,
  normalizeAppApiError,
  type AppFetch,
} from '../components/core/api.ts'
import {
  parseCurrentWorkspace,
  parseWorkspaceInvitation,
} from '../components/core/contracts.ts'

const runtimeWorkspace = {
  id: 'workspace-1',
  name: 'Studio',
  role: 'owner',
  status: 'active',
  created_at: '2026-07-01T09:00:00.000Z',
  updated_at: '2026-07-28T12:00:00.000Z',
}

async function readWorkspacePage(): Promise<string> {
  return readFile(new URL('../pages/workspace.vue', import.meta.url), 'utf8')
}

test('parseCurrentWorkspace accepts authoritative F4 base data', () => {
  assert.deepEqual(parseCurrentWorkspace(runtimeWorkspace), {
    id: 'workspace-1',
    name: 'Studio',
    role: 'owner',
    status: 'active',
    created_at: '2026-07-01T09:00:00.000Z',
    updated_at: '2026-07-28T12:00:00.000Z',
  })
})

test('parseCurrentWorkspace rejects unknown status or role', () => {
  assert.throws(
    () => parseCurrentWorkspace({ ...runtimeWorkspace, status: 'archived' }),
    /APP_INVALID_CURRENT_WORKSPACE/u,
  )
  assert.throws(
    () => parseCurrentWorkspace({ ...runtimeWorkspace, role: 'admin' }),
    /APP_INVALID_CURRENT_WORKSPACE/u,
  )
})

test('parseWorkspaceInvitation keeps the write-only token out of the parsed value', () => {
  const invitation = parseWorkspaceInvitation({
    id: 'invitation-1',
    status: 'pending',
    expires_at: '2026-08-05T12:00:00.000Z',
    token: 'x'.repeat(32),
    reissued: true,
  })
  assert.deepEqual(invitation, {
    id: 'invitation-1',
    status: 'pending',
    expires_at: '2026-08-05T12:00:00.000Z',
    reissued: true,
  })
  assert.equal((invitation as Record<string, unknown>).token, undefined)
})

test('normalizeAppApiError preserves distinct HTTP statuses for workspace messaging', () => {
  for (const status of [400, 401, 403, 404, 409, 503]) {
    assert.equal(normalizeAppApiError({ status }).status, status)
  }
  // A genuine network failure — not an application error — stays offline.
  assert.equal(normalizeAppApiError({ status: 0 }).kind, 'offline')
  assert.equal(normalizeAppApiError({ status: undefined }).kind, 'offline')
  // A malformed 404 application error must never be reported as offline.
  assert.notEqual(normalizeAppApiError({ status: 404 }).kind, 'offline')
})

test('current workspace read parses base data and fails closed on malformed payloads', async () => {
  const okApi = new AppShellApi(
    'https://api.postqron.test',
    async () => runtimeWorkspace,
  )
  assert.equal((await okApi.currentWorkspace()).name, 'Studio')

  const badApi = new AppShellApi(
    'https://api.postqron.test',
    async () => ({ id: 'workspace-1' }),
  )
  await assert.rejects(
    () => badApi.currentWorkspace(),
    (error: unknown) =>
      error instanceof AppApiError
      && error.kind === 'configuration'
      && error.code === 'APP_INVALID_CURRENT_WORKSPACE',
  )
})

test('owner workspace mutations post CSRF-protected requests to the current-workspace routes', async () => {
  const calls: Array<{ path: string, options?: Readonly<Record<string, unknown>> }> = []
  let csrfSequence = 0
  const fetch: AppFetch = async (path, options) => {
    calls.push({ path, options })
    if (path === '/api/v1/auth/csrf') {
      csrfSequence += 1
      return { csrf_token: `csrf-token-${csrfSequence}` }
    }
    if (path === '/api/v1/app/workspaces/current') {
      return { ...runtimeWorkspace, name: 'Renamed' }
    }
    if (path === '/api/v1/app/workspaces/current/invitations') {
      return {
        id: 'invitation-1',
        status: 'pending',
        expires_at: '2026-08-05T12:00:00.000Z',
        reissued: false,
      }
    }
    return undefined
  }
  const api = new AppShellApi('https://api.postqron.test', fetch)

  const renamed = await api.renameCurrentWorkspace('  Renamed  ')
  assert.equal(renamed.name, 'Renamed')
  const invitation = await api.inviteCurrentWorkspaceMember('  new@example.test ')
  assert.equal(invitation.reissued, false)
  await api.changeCurrentWorkspaceMemberRole({ memberId: 'member/2', role: 'owner' })
  await api.removeCurrentWorkspaceMember('member/2')

  const mutations = calls.filter(call => call.path !== '/api/v1/auth/csrf')
  assert.deepEqual(
    mutations.map(call => [call.path, call.options?.method]),
    [
      ['/api/v1/app/workspaces/current', 'PATCH'],
      ['/api/v1/app/workspaces/current/invitations', 'POST'],
      ['/api/v1/app/workspaces/current/members/member%2F2/role', 'PUT'],
      ['/api/v1/app/workspaces/current/members/member%2F2', 'DELETE'],
    ],
  )
  // Every mutation is immediately preceded by a fresh CSRF token fetch.
  assert.equal(calls.length, mutations.length * 2)
  for (const [index, mutation] of mutations.entries()) {
    assert.equal(calls[index * 2]?.path, '/api/v1/auth/csrf')
    assert.equal(
      (mutation.options?.headers as Record<string, string> | undefined)?.['X-CSRF-Token'],
      `csrf-token-${index + 1}`,
    )
  }
  assert.deepEqual(
    mutations[0]?.options?.body,
    { name: 'Renamed' },
  )
  assert.deepEqual(
    mutations[1]?.options?.body,
    { email: 'new@example.test' },
  )
  assert.deepEqual(mutations[2]?.options?.body, { role: 'owner' })
})

test('workspace page gates owner-only actions and keeps member view read-only', async () => {
  const page = await readWorkspacePage()
  assert.match(page, /const isOwner = computed\(\(\) => workspace\.value\?\.role === 'owner'\)/u)
  // Rename and invite forms only exist for Owners.
  assert.match(page, /<article\s+v-if="isOwner"[\s\S]*workspace\.rename\.title/u)
  assert.match(page, /<article\s+v-if="isOwner"[\s\S]*workspace\.invite\.title/u)
  // Member actions are wrapped in an owner-only block.
  assert.match(page, /v-if="isOwner"\s+class="app-member-list__actions"/u)
  assert.match(page, /workspace\.members\.memberHint/u)
})

test('workspace page protects the last Owner and confirms destructive actions', async () => {
  const page = await readWorkspacePage()
  assert.match(page, /ownerCount = computed\(\(\) =>\s*members\.value\.filter\(member => member\.role === 'owner'\)\.length\)/u)
  assert.match(page, /function isProtectedOwner\(member: WorkspaceMember\): boolean \{\s*return member\.role === 'owner' && ownerCount\.value <= 1/u)
  // Demote and remove buttons disable when the target is the final Owner.
  assert.match(page, /:disabled="Boolean\(busyMemberId\) \|\| isProtectedOwner\(member\)"/u)
  assert.match(page, /workspace\.members\.lastOwnerNote/u)
  // Destructive actions require explicit confirmation.
  assert.match(page, /confirmAction\(\s*t\('workspace\.members\.confirmRemove'/u)
  assert.match(page, /confirmAction\(\s*t\('workspace\.members\.confirmDemote'/u)
})

test('workspace page distinguishes application errors from real offline', async () => {
  const page = await readWorkspacePage()
  // Distinct messages for every documented status.
  for (const key of [
    'workspace.error.invalid',
    'workspace.error.session',
    'workspace.error.forbidden',
    'workspace.error.notFound',
    'workspace.error.conflict',
    'workspace.error.lastOwner',
    'workspace.error.memberLimit',
    'workspace.error.unavailable',
    'workspace.error.offline',
  ]) {
    assert.ok(page.includes(key), `missing ${key}`)
  }
  // A 404 is a not-found application state, never offline.
  assert.match(page, /normalized\.status === 404[\s\S]*loadNotice\.value = 'not-found'/u)
  // The 409 conflict is disambiguated by action (last Owner vs member limit).
  assert.match(page, /action === 'role' \|\| action === 'remove'\s*\?\s*'workspace\.error\.lastOwner'/u)
  assert.match(page, /action === 'invite'\s*\?\s*'workspace\.error\.memberLimit'/u)
})

test('workspace page keeps retryable states and accessible feedback regions', async () => {
  const page = await readWorkspacePage()
  assert.match(page, /kind="loading"/u)
  assert.match(page, /:kind="pageState"/u)
  assert.match(page, /@retry="retry"/u)
  assert.match(page, /useAsyncData\([\s\S]*\}, \{ server: false \}\)/u)
  // Feedback regions announce success politely and errors assertively.
  assert.match(page, /:role="renameFeedback\.tone === 'success' \? 'status' : 'alert'"/u)
  assert.match(page, /role="alert"/u)
  assert.match(page, /aria-live="polite"/u)
})
