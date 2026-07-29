import { createHmac, timingSafeEqual } from 'node:crypto'
import { createServer } from 'node:http'

const host = '127.0.0.1'
const port = Number(process.env.LAUNCH_FIXTURE_PORT || 41797)
const supervisorPid = Number(process.env.LAUNCH_SUPERVISOR_PID)
const signingKey = 'postqron-launch-fixture-signing-key-v1'
const digest = 'a'.repeat(64)
const now = '2026-07-25T12:00:00.000Z'

const money = amount_cents => ({ amount_cents, currency: 'EUR' })
const tiers = (first, second, third) => [
  { from_channel: 1, to_channel: 10, monthly: money(first), annual: money(first * 10) },
  { from_channel: 11, to_channel: 25, monthly: money(second), annual: money(second * 10) },
  { from_channel: 26, to_channel: 50, monthly: money(third), annual: money(third * 10) },
]
const catalog = {
  provider: 'paddle',
  catalog_version: 'd09-v2',
  currency: 'EUR',
  plans: [
    {
      code: 'start',
      name: 'Start',
      purchasable: false,
      prices: { monthly: money(0), annual: money(0) },
      price_tiers: [],
      limits: {
        members: 1,
        channels: 3,
        scheduled_publications: 10,
        scheduled_publications_per_channel: 10,
      },
    },
    {
      code: 'pro',
      name: 'Pro',
      purchasable: true,
      prices: { monthly: money(450), annual: money(4500) },
      price_tiers: tiers(450, 300, 225),
      limits: {
        members: 3,
        channels: 6,
        scheduled_publications: 250,
        scheduled_publications_per_channel: 250,
      },
    },
    {
      code: 'team',
      name: 'Team',
      purchasable: true,
      prices: { monthly: money(900), annual: money(9000) },
      price_tiers: tiers(900, 300, 225),
      limits: {
        members: 6,
        channels: 9,
        scheduled_publications: 500,
        scheduled_publications_per_channel: 500,
      },
      trial: {
        days: 14,
        members: 6,
        channels: 9,
        scheduled_publications_per_channel: 500,
      },
    },
    {
      code: 'unlimited',
      name: 'Unlimited',
      purchasable: true,
      prices: { monthly: money(12_900), annual: money(129_000) },
      price_tiers: [],
      limits: {
        members: null,
        channels: null,
        scheduled_publications: null,
        scheduled_publications_per_channel: null,
      },
    },
  ],
}

const adminUsers = [
  {
    id: 'account-admin',
    email: 'admin@example.test',
    display_name: 'Fixture Admin',
    account_status: 'active',
    email_verified: true,
    login_methods: ['password'],
    registered_at: '2026-05-01T09:00:00.000Z',
    last_login_at: now,
    active_sessions: 1,
    workspaces: [{
      id: 'workspace-fixture',
      name: 'Fixture Workspace',
      role: 'owner',
      plan_code: 'pro',
      plan_status: 'active',
    }],
  },
  {
    id: 'account-locked',
    email: 'locked@example.test',
    display_name: 'Locked Fixture',
    account_status: 'locked',
    email_verified: false,
    login_methods: ['google', 'linkedin'],
    registered_at: '2026-06-15T11:00:00.000Z',
    last_login_at: '2026-07-01T08:00:00.000Z',
    active_sessions: 0,
    workspaces: [{
      id: 'workspace-locked',
      name: 'Locked Studio',
      role: 'member',
      plan_code: 'team',
      plan_status: 'past_due',
    }],
  },
  {
    id: 'account-empty',
    email: 'no-workspace@example.test',
    display_name: 'No Workspace',
    account_status: 'active',
    email_verified: true,
    login_methods: ['apple'],
    registered_at: '2026-07-20T10:00:00.000Z',
    last_login_at: null,
    active_sessions: 0,
    workspaces: [],
  },
]

const adminWorkspaces = [
  {
    id: 'workspace-fixture',
    name: 'Fixture Workspace',
    owner_id: 'account-admin',
    owner_email: 'admin@example.test',
    owner_display_name: 'Fixture Admin',
    status: 'active',
    plan_code: 'pro',
    plan_status: 'active',
    member_count: 3,
    channel_count: 4,
    post_count: 18,
    created_at: '2026-05-01T09:00:00.000Z',
    updated_at: now,
  },
  {
    id: 'workspace-locked',
    name: 'Locked Studio',
    owner_id: 'account-locked',
    owner_email: 'locked@example.test',
    owner_display_name: 'Locked Fixture',
    status: 'deletion_pending',
    plan_code: 'team',
    plan_status: 'past_due',
    member_count: 1,
    channel_count: 0,
    post_count: 2,
    created_at: '2026-06-15T11:00:00.000Z',
    updated_at: '2026-07-10T08:00:00.000Z',
  },
]

function directoryPage(items, url, defaultSort) {
  const page = Math.max(1, Number(url.searchParams.get('page')) || 1)
  const pageSize = [10, 25, 50, 100].includes(
    Number(url.searchParams.get('page_size')),
  )
    ? Number(url.searchParams.get('page_size'))
    : 25
  const sort = url.searchParams.get('sort') || defaultSort
  const direction = url.searchParams.get('direction') === 'asc' ? 'asc' : 'desc'
  const ordered = [...items].sort((left, right) => {
    const first = left[sort] ?? ''
    const second = right[sort] ?? ''
    const comparison = typeof first === 'number'
      ? first - Number(second)
      : String(first).localeCompare(String(second))
    return direction === 'asc' ? comparison : -comparison
  })
  const offset = (page - 1) * pageSize
  return {
    items: ordered.slice(offset, offset + pageSize),
    page,
    page_size: pageSize,
    total: ordered.length,
    sort,
    direction,
  }
}

function filteredAdminUsers(url) {
  const search = (url.searchParams.get('q') || '').toLowerCase()
  return adminUsers.filter((user) => {
    const planCodes = user.workspaces.map(workspace => workspace.plan_code)
    return (
      (!search || `${user.email} ${user.display_name}`.toLowerCase().includes(search))
      && (!url.searchParams.get('status')
        || user.account_status === url.searchParams.get('status'))
      && (!url.searchParams.has('email_verified')
        || user.email_verified === (url.searchParams.get('email_verified') === 'true'))
      && (!url.searchParams.get('plan')
        || planCodes.includes(url.searchParams.get('plan')))
      && (!url.searchParams.get('login_method')
        || user.login_methods.includes(url.searchParams.get('login_method')))
      && (!url.searchParams.get('registered_from')
        || user.registered_at.slice(0, 10) >= url.searchParams.get('registered_from'))
      && (!url.searchParams.get('registered_to')
        || user.registered_at.slice(0, 10) <= url.searchParams.get('registered_to'))
      && (!url.searchParams.get('last_login_from')
        || (user.last_login_at
          && user.last_login_at.slice(0, 10) >= url.searchParams.get('last_login_from')))
      && (!url.searchParams.get('last_login_to')
        || (user.last_login_at
          && user.last_login_at.slice(0, 10) <= url.searchParams.get('last_login_to')))
    )
  })
}

function filteredAdminWorkspaces(url) {
  const search = (url.searchParams.get('q') || '').toLowerCase()
  const owner = (url.searchParams.get('owner') || '').toLowerCase()
  return adminWorkspaces.filter(workspace => (
    (!search || `${workspace.id} ${workspace.name} ${workspace.owner_email}`
      .toLowerCase().includes(search))
    && (!owner || `${workspace.owner_email} ${workspace.owner_display_name}`
      .toLowerCase().includes(owner))
    && (!url.searchParams.get('status')
      || workspace.status === url.searchParams.get('status'))
    && (!url.searchParams.get('plan')
      || workspace.plan_code === url.searchParams.get('plan'))
    && (!url.searchParams.get('created_from')
      || workspace.created_at.slice(0, 10) >= url.searchParams.get('created_from'))
    && (!url.searchParams.get('created_to')
      || workspace.created_at.slice(0, 10) <= url.searchParams.get('created_to'))
    && (!url.searchParams.get('updated_from')
      || workspace.updated_at.slice(0, 10) >= url.searchParams.get('updated_from'))
    && (!url.searchParams.get('updated_to')
      || workspace.updated_at.slice(0, 10) <= url.searchParams.get('updated_to'))
  ))
}

function csvCell(value) {
  const text = String(value ?? '')
  const safe = /^[=+\-@\t\r]/u.test(text) ? `'${text}` : text
  return `"${safe.replaceAll('"', '""')}"`
}

function directoryCSV(subject, items) {
  const fields = subject === 'users'
    ? ['email', 'display_name', 'account_status', 'email_verified', 'registered_at']
    : ['name', 'owner_email', 'status', 'plan_code', 'member_count', 'channel_count', 'post_count']
  return `\uFEFF${[
    fields.map(csvCell).join(','),
    ...items.map(item => fields.map(field => csvCell(item[field])).join(',')),
  ].join('\r\n')}\r\n`
}

let state

function reset() {
  state = {
    cookies: new Map(),
    entitlement: 'start',
    internal: false,
    audit: [],
    health: 'operational',
    adminVerifier: 'fixture-admin-password',
    nextSession: 1,
    nextVerification: 1,
    mailbox: [],
    privacyExports: new Map(),
    privacyDownloads: new Map(),
    privacyDeletions: new Map(),
    sessions: new Map([
      ['admin', { role: 'admin', account_id: 'account-admin' }],
      ['authenticated', { role: 'authenticated', account_id: 'account-authenticated' }],
      ['normal', { role: 'normal', account_id: 'account-normal' }],
    ]),
    accounts: new Map([
      ['account-admin', {
        id: 'account-admin',
        email: 'admin@example.test',
        loginSecret: 'fixture-admin-password',
        display_name: 'Fixture Admin',
        locale: 'de',
        timezone: 'Europe/Berlin',
        contract_country: 'IT',
        email_verified: true,
        onboarding_required: false,
        current_workspace_id: 'workspace-fixture',
        workspaces: [{
          id: 'workspace-fixture',
          name: 'Fixture Workspace',
          role: 'owner',
        }],
        providers: [{
          id: 'password',
          kind: 'identity',
          name: 'password',
          external_label: 'password',
          connected_at: now,
          only_login_method: true,
        }],
        updated_at: now,
      }],
      ['account-authenticated', {
        id: 'account-authenticated',
        email: 'authenticated@example.test',
        loginSecret: 'correct horse battery staple',
        display_name: 'Fixture User',
        locale: 'it',
        timezone: 'Europe/Rome',
        contract_country: 'IT',
        email_verified: true,
        onboarding_required: false,
        current_workspace_id: 'workspace-fixture',
        workspaces: [{
          id: 'workspace-fixture',
          name: 'Fixture Workspace',
          role: 'owner',
        }],
        providers: [{
          id: 'password',
          kind: 'identity',
          name: 'password',
          external_label: 'password',
          connected_at: now,
          only_login_method: false,
        }, {
          id: 'google',
          kind: 'identity',
          name: 'google',
          external_label: 'authenticated@example.test',
          connected_at: now,
          only_login_method: false,
        }],
        updated_at: now,
      }],
      ['account-normal', {
        id: 'account-normal',
        email: 'normal@example.test',
        loginSecret: 'correct horse battery staple',
        display_name: 'Fixture User',
        locale: 'fr',
        timezone: 'Europe/Paris',
        contract_country: 'IT',
        email_verified: true,
        onboarding_required: false,
        current_workspace_id: 'workspace-fixture',
        workspaces: [{
          id: 'workspace-fixture',
          name: 'Fixture Workspace',
          role: 'owner',
        }],
        providers: [{
          id: 'apple',
          kind: 'identity',
          name: 'apple',
          external_label: 'normal@example.test',
          connected_at: now,
          only_login_method: true,
        }],
        updated_at: now,
      }],
    ]),
  }
}
reset()

function fixtureSessionToken(request) {
  const cookie = request.headers.cookie || ''
  return /(?:^|;\s*)postqron_fixture_session=([^;]+)(?:;|$)/u.exec(cookie)?.[1]
}

function sessionRole(request) {
  const token = fixtureSessionToken(request)
  return token ? state.sessions.get(token)?.role : undefined
}

function currentAccount(request) {
  const token = fixtureSessionToken(request)
  const session = token ? state.sessions.get(token) : undefined
  return session ? state.accounts.get(session.account_id) : undefined
}

function appSession(role = 'authenticated', account = undefined) {
  if (account) {
    return {
      account: {
        id: account.id,
        display_name: account.display_name,
        email: account.email,
        email_verified: account.email_verified,
        locale: account.locale,
      },
      authenticated_at: now,
      current_workspace: account.workspaces.find(
        workspace => workspace.id === account.current_workspace_id,
      ) || null,
      onboarding_required: account.onboarding_required,
      workspaces: account.workspaces,
    }
  }
  const locale = role === 'admin' ? 'de' : role === 'normal' ? 'fr' : 'it'
  return {
    account: {
      id: `account-${role}`,
      display_name: role === 'admin' ? 'Fixture Admin' : 'Fixture User',
      email: `${role}@example.test`,
      email_verified: true,
      locale,
    },
    authenticated_at: now,
    current_workspace: {
      id: 'workspace-fixture',
      name: 'Fixture Workspace',
      role: 'owner',
    },
    onboarding_required: false,
    workspaces: [{
      id: 'workspace-fixture',
      name: 'Fixture Workspace',
      role: 'owner',
    }],
  }
}

function mailboxDelivery(account, locale = 'it-IT') {
  const token = `verify-${state.nextVerification++}`
  const item = {
    email: account.email,
    locale,
    token,
    action_url: `/it/app/verify-email?token=${encodeURIComponent(token)}`,
  }
  state.mailbox.push(item)
  account.pending_verification_token = token
  return item
}

function issueSession(account, role = 'authenticated') {
  const token = `${role}-session-${state.nextSession++}`
  state.sessions.set(token, { role, account_id: account.id })
  return token
}

function accountArea(account) {
  return {
    profile: {
      account_id: account.id,
      display_name: account.display_name,
      locale: account.locale,
      timezone: account.timezone,
      updated_at: account.updated_at,
    },
    providers: account.providers,
    workspaces: account.workspaces.map(workspace => ({
      workspace,
      plan: {
        code: state.entitlement,
        name: catalog.plans.find(plan => plan.code === state.entitlement)?.name || 'Start',
        state: 'active',
        usage: {
          members: 1,
          channels: 2,
          scheduled_publications: 2,
        },
        limits: {
          members: 3,
          channels: 6,
          scheduled_publications: 250,
        },
        manageable: state.entitlement !== 'start',
        renews_at: '2026-08-01T00:00:00.000Z',
      },
    })),
  }
}

function currentWorkspaceMembers(account) {
  const workspace = account.workspaces.find(item => item.id === account.current_workspace_id)
  if (!workspace) {
    return []
  }
  return [
    {
      id: `${workspace.id}:${account.id}`,
      workspace_id: workspace.id,
      account_id: account.id,
      email: account.email,
      role: workspace.role,
      status: 'active',
      created_at: now,
      updated_at: now,
    },
  ]
}

function usageEntry(resource, limit, used) {
  return {
    resource,
    used,
    limit,
    remaining: limit === null ? null : Math.max(0, limit - used),
    over_limit: limit !== null && used > limit,
  }
}

function overview() {
  const paid = state.entitlement !== 'start'
  const plan = catalog.plans.find(item => item.code === state.entitlement)
  const channelsUsed = paid ? Math.min(4, plan.limits.channels ?? 4) : 1
  return {
    plan,
    interval: 'monthly',
    state: 'active',
    period: {
      start: '2026-07-01T00:00:00.000Z',
      end: '2026-08-01T00:00:00.000Z',
    },
    usage: [
      usageEntry('members', plan.limits.members, 1),
      usageEntry('channels', plan.limits.channels, channelsUsed),
      usageEntry('scheduled_publications', plan.limits.scheduled_publications, 2),
    ],
  }
}

function cookieState(subject = 'anonymous') {
  return state.cookies.get(subject) || {
    necessary: true,
    preferences: false,
    analytics: false,
    marketing: false,
    has_recorded_choice: false,
    policy_version: '1.0',
    policy_digest_sha256: digest,
    selected_at: null,
    expires_at: null,
    revision: 0,
  }
}

function json(response, status, body, headers = {}) {
  response.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    ...headers,
  })
  response.end(`${JSON.stringify(body)}\n`)
}

function error(response, status, code) {
  json(response, status, {
    error: { code, message: code, retryable: status >= 500 },
  })
}

async function body(request) {
  const chunks = []
  for await (const chunk of request) {
    chunks.push(chunk)
  }
  return Buffer.concat(chunks)
}

function cors(request, response) {
  const origin = request.headers.origin
  if (origin && /^https?:\/\/(?:127\.0\.0\.1|localhost):\d+$/u.test(origin)) {
    response.setHeader('access-control-allow-origin', origin)
    response.setHeader('access-control-allow-credentials', 'true')
    response.setHeader(
      'access-control-allow-headers',
      'content-type,idempotency-key,paddle-signature,x-csrf-token',
    )
    response.setHeader(
      'access-control-allow-methods',
      'GET,POST,PUT,DELETE,OPTIONS',
    )
  }
}

const server = createServer(async (request, response) => {
  cors(request, response)
  if (request.method === 'OPTIONS') {
    response.writeHead(204)
    response.end()
    return
  }
  const url = new URL(request.url, `http://${host}:${port}`)
  const role = sessionRole(request)

  if (request.method === 'GET' && url.pathname === '/healthz') {
    json(response, 200, { status: 'ok' })
    return
  }
  if (request.method === 'POST' && url.pathname === '/__fixture/reset') {
    reset()
    json(response, 200, { reset: true })
    return
  }
  if (request.method === 'POST' && url.pathname === '/__fixture/health') {
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    const allowed = ['operational', 'degraded', 'outage', 'unknown', 'api_failure']
    state.health = allowed.includes(input.status) ? input.status : 'operational'
    json(response, 200, { health: state.health })
    return
  }
  if (request.method === 'GET' && url.pathname === '/__fixture/mailbox') {
    const email = (url.searchParams.get('email') || '').toLowerCase()
    json(response, 200, {
      items: state.mailbox.filter(item => item.email.toLowerCase() === email),
    })
    return
  }
  if (request.method === 'POST' && url.pathname === '/__fixture/shutdown') {
    json(response, 200, { stopped: true })
    setImmediate(stop)
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/billing/plans') {
    json(response, 200, catalog)
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/app/bootstrap') {
    const account = currentAccount(request)
    json(response, 200, {
      auth_methods: ['password'],
      providers: ['google', 'apple', 'facebook', 'linkedin'],
      legal_documents: [
        { key: 'terms', version: '1.0', href: '/legal/termini', digest_sha256: digest },
        { key: 'privacy', version: '1.0', href: '/legal/privacy', digest_sha256: digest },
      ],
      ...(role ? { session: appSession(role, account) } : {}),
    })
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/auth/csrf') {
    json(response, 200, { csrf_token: 'fixture-csrf' })
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/auth/password/register') {
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    if (input.password !== input.confirmation) {
      error(response, 400, 'AUTH_PASSWORD_CONFIRMATION_MISMATCH')
      return
    }
    if (typeof input.password !== 'string' || input.password.length < 12) {
      error(response, 400, 'AUTH_PASSWORD_WEAK')
      return
    }
    const email = String(input.email || '').toLowerCase().trim()
    const existing = Array.from(state.accounts.values())
      .find(account => account.email.toLowerCase() === email)
    const account = existing || {
      id: `account-${state.accounts.size + 1}`,
      email,
      loginSecret: input.password,
      display_name: '',
      locale: 'it-IT',
      timezone: 'Europe/Rome',
      contract_country: 'IT',
      email_verified: false,
      onboarding_required: true,
      current_workspace_id: '',
      workspaces: [],
      providers: [{
        id: 'password',
        kind: 'identity',
        name: 'password',
        external_label: 'password',
        connected_at: now,
        only_login_method: true,
      }],
      updated_at: now,
    }
    account.loginSecret = input.password
    state.accounts.set(account.id, account)
    mailboxDelivery(account, input.consents?.[0]?.locale || 'it-IT')
    json(response, 202, { verification_requested: true })
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/auth/password/verify') {
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    const token = String(input.token || url.searchParams.get('token') || '')
    const account = Array.from(state.accounts.values())
      .find(item => item.pending_verification_token === token)
    if (!account) {
      error(response, 400, 'AUTH_EMAIL_VERIFICATION_INVALID')
      return
    }
    account.email_verified = true
    delete account.pending_verification_token
    json(response, 200, { verified: true })
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/auth/password/verify/resend') {
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    const email = String(input.email || '').toLowerCase().trim()
    const account = Array.from(state.accounts.values())
      .find(item => item.email.toLowerCase() === email)
    if (account && !account.email_verified) {
      mailboxDelivery(account, account.locale)
    }
    json(response, 202, { verification_requested: true })
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/auth/password/login') {
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    const email = String(input.email || '').toLowerCase().trim()
    const account = Array.from(state.accounts.values())
      .find(item => item.email.toLowerCase() === email)
    if (!account) {
      error(response, 401, 'AUTH_INVALID_CREDENTIALS')
      return
    }
    if (account.id === 'account-admin') {
      account.loginSecret = state.adminVerifier
    }
    if (input.password !== account.loginSecret) {
      error(response, 401, 'AUTH_INVALID_CREDENTIALS')
      return
    }
    if (!account.email_verified) {
      error(response, 401, 'AUTH_EMAIL_UNVERIFIED')
      return
    }
    const token = issueSession(
      account,
      account.id === 'account-admin' ? 'admin' : 'authenticated',
    )
    json(response, 200, { authenticated: true }, {
      'set-cookie': `postqron_fixture_session=${token}; Path=/; HttpOnly; SameSite=Lax`,
    })
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/auth/authorize') {
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    if (!['google', 'apple', 'facebook', 'linkedin'].includes(input.provider)) {
      error(response, 400, 'AUTH_PROVIDER_UNSUPPORTED')
      return
    }
    error(response, 503, 'AUTH_PROVIDER_UNAVAILABLE')
    return
  }
  if (
    (request.method === 'GET' || request.method === 'POST')
    && url.pathname === '/api/v1/auth/callback'
  ) {
    if (url.searchParams.get('error') === 'access_denied') {
      error(response, 400, 'AUTH_PROVIDER_ACCESS_DENIED')
      return
    }
    error(response, 400, 'AUTH_PROVIDER_CALLBACK_INVALID')
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/account/exports') {
    const account = currentAccount(request)
    if (!account) {
      error(response, 401, 'unauthenticated')
      return
    }
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    const id = `export-${account.id}-${state.privacyExports.size + 1}`
    const exportRequest = {
      id,
      account_id: account.id,
      scope: input.scope || 'account',
      status: 'ready',
      requested_at: now,
      ready_at: now,
      expires_at: '2026-08-01T12:00:00.000Z',
      sha256: digest,
      size_bytes: 128,
    }
    state.privacyExports.set(id, exportRequest)
    json(response, 202, exportRequest)
    return
  }
  if (
    request.method === 'GET'
    && /^\/api\/v1\/account\/exports\/[^/]+\/download$/u.test(url.pathname)
  ) {
    const account = currentAccount(request)
    const exportID = decodeURIComponent(url.pathname.split('/').at(-2))
    const exportRequest = state.privacyExports.get(exportID)
    if (!account || !exportRequest || exportRequest.account_id !== account.id) {
      error(response, 404, 'not_found')
      return
    }
    if (url.searchParams.get('fixture_expired') === '1') {
      error(response, 410, 'export_expired')
      return
    }
    const token = `download-${state.privacyDownloads.size + 1}`
    state.privacyDownloads.set(token, { exportID, consumed: false })
    json(response, 200, {
      url: `http://${request.headers.host}/api/v1/account/privacy-artifacts/${token}`,
      expires_at: '2026-07-25T12:05:00.000Z',
      sha256: digest,
      size_bytes: 128,
    })
    return
  }
  if (
    request.method === 'GET'
    && /^\/api\/v1\/account\/privacy-artifacts\/[^/]+$/u.test(url.pathname)
  ) {
    const token = decodeURIComponent(url.pathname.split('/').at(-1))
    const download = state.privacyDownloads.get(token)
    if (!download || download.consumed) {
      error(response, 404, 'not_found')
      return
    }
    download.consumed = true
    response.writeHead(200, {
      'content-type': 'application/zip',
      'cache-control': 'private, no-store',
    })
    response.end('fixture-private-export')
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/account/deletions') {
    const account = currentAccount(request)
    if (!account) {
      error(response, 401, 'unauthenticated')
      return
    }
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    const id = `deletion-${account.id}-${state.privacyDeletions.size + 1}`
    const deletion = {
      id,
      account_id: account.id,
      scope: input.scope || 'workspace',
      workspace_id: input.workspace_id,
      status: url.searchParams.get('fixture_finalize') === '1' ? 'completed' : 'grace_period',
      requested_at: now,
      grace_ends_at: '2026-08-22T12:00:00.000Z',
      ownership: { actions: input.ownership_actions || [] },
    }
    state.privacyDeletions.set(id, deletion)
    json(response, 202, deletion)
    return
  }
  if (
    request.method === 'DELETE'
    && /^\/api\/v1\/account\/deletions\/[^/]+$/u.test(url.pathname)
  ) {
    const account = currentAccount(request)
    const deletionID = decodeURIComponent(url.pathname.split('/').at(-1))
    const deletion = state.privacyDeletions.get(deletionID)
    if (!account || !deletion || deletion.account_id !== account.id) {
      error(response, 404, 'not_found')
      return
    }
    deletion.status = 'cancelled'
    response.writeHead(204)
    response.end()
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/auth/logout') {
    if (!role) {
      response.writeHead(204, {
        'set-cookie': 'postqron_fixture_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax',
      })
      response.end()
      return
    }
    if (role === 'admin' && request.headers['x-csrf-token'] !== 'fixture-csrf') {
      error(response, 403, 'AUTH_CSRF_INVALID')
      return
    }
    state.sessions.delete(fixtureSessionToken(request))
    response.writeHead(204, {
      'set-cookie': 'postqron_fixture_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax',
    })
    response.end()
    return
  }
  if (
    request.method === 'POST'
    && url.pathname === '/api/v1/auth/password/change'
  ) {
    if (role !== 'admin') {
      error(response, 401, 'AUTH_UNAUTHENTICATED')
      return
    }
    if (request.headers['x-csrf-token'] !== 'fixture-csrf') {
      error(response, 403, 'AUTH_CSRF_INVALID')
      return
    }
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    if (input.new_password !== input.confirmation) {
      error(response, 400, 'AUTH_PASSWORD_CONFIRMATION_MISMATCH')
      return
    }
    if (
      typeof input.new_password !== 'string'
      || input.new_password.length < 12
      || input.new_password === state.adminVerifier
    ) {
      error(response, 400, 'AUTH_PASSWORD_WEAK')
      return
    }
    if (input.current_password !== state.adminVerifier) {
      error(response, 400, 'AUTH_CURRENT_PASSWORD_INVALID')
      return
    }
    state.adminVerifier = input.new_password
    for (const [token, session] of state.sessions.entries()) {
      if (session.account_id === 'account-admin') {
        state.sessions.delete(token)
      }
    }
    const token = issueSession(state.accounts.get('account-admin'), 'admin')
    json(response, 200, { changed: true }, {
      'set-cookie': `postqron_fixture_session=${token}; Path=/; HttpOnly; SameSite=Lax`,
    })
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/app/session') {
    const account = currentAccount(request)
    if (!role || !account) {
      error(response, 401, 'APP_SESSION_REQUIRED')
      return
    }
    json(response, 200, appSession(role, account))
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/app/onboarding') {
    const account = currentAccount(request)
    if (!account) {
      error(response, 401, 'APP_SESSION_REQUIRED')
      return
    }
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    const selected = input.workspace?.id
    const existing = selected
      ? account.workspaces.find(workspace => workspace.id === selected)
      : undefined
    const workspace = existing || {
      id: `workspace-${account.id}`,
      name: input.workspace?.name || `${account.display_name || 'Personal'} Workspace`,
      role: 'owner',
    }
    if (!existing) {
      account.workspaces = [workspace]
    }
    account.current_workspace_id = workspace.id
    account.display_name = input.account?.display_name || account.display_name || 'Fixture User'
    account.onboarding_required = false
    account.updated_at = now
    json(response, existing ? 200 : 201, appSession('authenticated', account))
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/app/workspaces/select') {
    const account = currentAccount(request)
    if (!account) {
      error(response, 401, 'APP_SESSION_REQUIRED')
      return
    }
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    const workspace = account.workspaces.find(item => item.id === input.workspace_id)
    if (!workspace) {
      error(response, 404, 'APP_WORKSPACE_NOT_FOUND')
      return
    }
    account.current_workspace_id = workspace.id
    response.writeHead(204)
    response.end()
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/app/workspaces/current') {
    const account = currentAccount(request)
    if (!account) {
      error(response, 401, 'APP_SESSION_REQUIRED')
      return
    }
    const workspace = account.workspaces.find(item => item.id === account.current_workspace_id)
    if (!workspace) {
      error(response, 404, 'APP_WORKSPACE_NOT_FOUND')
      return
    }
    json(response, 200, workspace)
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/app/workspaces/current/members') {
    const account = currentAccount(request)
    if (!account) {
      error(response, 401, 'APP_SESSION_REQUIRED')
      return
    }
    json(response, 200, currentWorkspaceMembers(account))
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/account') {
    const account = currentAccount(request)
    if (!account) {
      error(response, 401, 'unauthenticated')
      return
    }
    json(response, 200, accountArea(account))
    return
  }
  if (request.method === 'PATCH' && url.pathname === '/api/v1/account/profile') {
    const account = currentAccount(request)
    if (!account) {
      error(response, 401, 'unauthenticated')
      return
    }
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    account.display_name = input.display_name || account.display_name
    account.locale = input.locale || account.locale
    account.timezone = input.timezone || account.timezone
    account.updated_at = now
    json(response, 200, {
      account_id: account.id,
      display_name: account.display_name,
      locale: account.locale,
      timezone: account.timezone,
      updated_at: account.updated_at,
    })
    return
  }
  if (
    request.method === 'DELETE'
    && /^\/api\/v1\/account\/providers\/[^/]+$/u.test(url.pathname)
  ) {
    const account = currentAccount(request)
    if (!account) {
      error(response, 401, 'unauthenticated')
      return
    }
    const providerID = decodeURIComponent(url.pathname.split('/').at(-1))
    if (account.providers.length <= 1) {
      error(response, 409, 'last_login_provider')
      return
    }
    account.providers = account.providers.filter(provider => provider.id !== providerID)
    for (const provider of account.providers) {
      provider.only_login_method = account.providers.length === 1
    }
    response.writeHead(204)
    response.end()
    return
  }
  if (url.pathname === '/api/v1/cookie-preferences') {
    const subject = 'anonymous'
    if (request.method === 'GET') {
      json(response, 200, cookieState(subject), {
        'set-cookie': 'postqron_fixture_subject=anonymous; Path=/; HttpOnly; SameSite=Lax',
      })
      return
    }
    if (request.method === 'PUT') {
      const input = JSON.parse((await body(request)).toString('utf8') || '{}')
      const previous = cookieState(subject)
      const next = {
        necessary: true,
        preferences: input.preferences === true,
        analytics: input.analytics === true,
        marketing: input.marketing === true,
        has_recorded_choice: true,
        policy_version: '1.0',
        policy_digest_sha256: digest,
        selected_at: now,
        expires_at: '2027-07-25T12:00:00.000Z',
        source: input.source,
        revision: previous.revision + 1,
      }
      state.cookies.set(subject, next)
      json(response, 200, next)
      return
    }
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/admin/session') {
    if (!role) {
      error(response, 401, 'ADMIN_UNAUTHENTICATED')
      return
    }
    if (role !== 'admin') {
      error(response, 403, 'ADMIN_FORBIDDEN')
      return
    }
    json(response, 200, {
      account: { id: 'account-admin', email: 'admin@example.test' },
      authenticated_at: now,
      csrf_token: 'fixture-csrf',
    })
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/admin/dashboard') {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, 'ADMIN_FORBIDDEN')
      return
    }
    if (state.health === 'api_failure') {
      error(response, 503, 'ADMIN_UNAVAILABLE')
      return
    }
    const staleCheckedAt = '2026-07-25T10:00:00.000Z'
    const services = state.health === 'unknown'
      ? [
          { code: 'api', status: 'operational', checked_at: now },
          { code: 'database', status: 'operational', checked_at: now },
          { code: 'worker_queue', status: 'unknown', checked_at: staleCheckedAt },
        ]
      : state.health === 'degraded' || state.health === 'outage'
        ? [
            { code: 'api', status: 'operational', checked_at: now },
            { code: 'database', status: state.health, checked_at: now },
          ]
        : [{ code: 'api', status: 'operational', checked_at: now }]
    json(response, 200, {
      services,
      entitlements: [{
        workspace_id: 'workspace-fixture',
        plan_code: state.internal ? 'internal' : state.entitlement,
        internal: state.internal,
      }],
      recent_audit: [...state.audit].reverse(),
    })
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/admin/plans') {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, 'ADMIN_FORBIDDEN')
      return
    }
    const all = Array.from({ length: 30 }, (_, index) => {
      const fixture = index === 0
      const plan = fixture
        ? state.entitlement
        : ['start', 'pro', 'team'][index % 3]
      const internal = fixture && state.internal
      return {
        workspace_id: fixture ? 'workspace-fixture' : `workspace-${index + 1}`,
        workspace_name: fixture ? 'Fixture Workspace' : `Studio ${index + 1}`,
        owner_email: fixture ? 'owner@example.test' : `owner-${index + 1}@example.test`,
        plan_code: plan,
        status: 'active',
        internal,
        usage: {
          members: {
            used: 1,
            limit: internal ? null : 5,
            remaining: internal ? null : 4,
            unlimited: internal,
          },
          channels: {
            used: 2,
            limit: internal ? null : 10,
            remaining: internal ? null : 8,
            unlimited: internal,
          },
          scheduled_publications: {
            used: index,
            limit: internal ? null : 100,
            remaining: internal ? null : 100 - index,
            unlimited: internal,
          },
        },
        workspace_created_at: now,
        plan_updated_at: now,
        period_start: now,
        period_end: '2026-08-25T12:00:00.000Z',
        internal_assigned_at: internal ? now : null,
      }
    })
    const search = url.searchParams.get('q')?.toLowerCase()
    const filtered = all.filter(item =>
      (!search
        || item.workspace_name.toLowerCase().includes(search)
        || item.workspace_id.toLowerCase().includes(search)
        || item.owner_email.toLowerCase().includes(search))
      && (!url.searchParams.get('plan')
        || item.plan_code === url.searchParams.get('plan'))
      && (!url.searchParams.get('status')
        || item.status === url.searchParams.get('status'))
      && (!url.searchParams.get('type')
        || (url.searchParams.get('type') === 'internal'
          ? item.internal
          : !item.internal)))
    const page = Number(url.searchParams.get('page') || 1)
    const pageSize = Number(url.searchParams.get('page_size') || 25)
    json(response, 200, {
      items: filtered.slice((page - 1) * pageSize, page * pageSize),
      pagination: { page, page_size: pageSize, total: filtered.length },
    })
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/admin/users') {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, role ? 'ADMIN_FORBIDDEN' : 'ADMIN_UNAUTHENTICATED')
      return
    }
    json(response, 200, directoryPage(
      filteredAdminUsers(url),
      url,
      'registered_at',
    ))
    return
  }
  if (
    request.method === 'GET'
    && url.pathname === '/api/v1/admin/plans/export'
  ) {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, 'ADMIN_FORBIDDEN')
      return
    }
    const format = url.searchParams.get('format')
    if (format !== 'csv' && format !== 'xlsx') {
      error(response, 400, 'ADMIN_INVALID_EXPORT_FORMAT')
      return
    }
    response.writeHead(200, {
      'content-type': format === 'csv'
        ? 'text/csv; charset=utf-8'
        : 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      'content-disposition': `attachment; filename="postqron-admin-plans.${format}"`,
    })
    response.end(format === 'csv'
      ? 'workspace_id,workspace_name\nworkspace-fixture,Fixture Workspace\n'
      : 'PKfixture')
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/admin/audit') {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, 'ADMIN_FORBIDDEN')
      return
    }
    const filtered = [...state.audit].reverse().filter(item =>
      (!url.searchParams.get('action')
        || item.code === url.searchParams.get('action'))
      && (!url.searchParams.get('actor')
        || item.actor_id.includes(url.searchParams.get('actor')))
      && (!url.searchParams.get('subject')
        || item.subject_id.includes(url.searchParams.get('subject')))
      && (!url.searchParams.get('outcome')
        || item.outcome === url.searchParams.get('outcome')))
    const page = Number(url.searchParams.get('page') || 1)
    const pageSize = Number(url.searchParams.get('page_size') || 25)
    json(response, 200, {
      items: filtered.slice((page - 1) * pageSize, page * pageSize),
      pagination: { page, page_size: pageSize, total: filtered.length },
    })
    return
  }
  if (
    request.method === 'GET'
    && url.pathname === '/api/v1/admin/users/export'
  ) {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, role ? 'ADMIN_FORBIDDEN' : 'ADMIN_UNAUTHENTICATED')
      return
    }
    const format = url.searchParams.get('format')
    const contentType = format === 'xlsx'
      ? 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'
      : 'text/csv; charset=utf-8'
    const payload = format === 'xlsx'
      ? Buffer.from('PK\u0003\u0004fixture-xlsx')
      : Buffer.from(directoryCSV('users', filteredAdminUsers(url)), 'utf8')
    response.writeHead(200, {
      'content-type': contentType,
      'content-disposition': `attachment; filename="postqron-admin-users-20260725.${format}"`,
      'cache-control': 'no-store',
      'x-content-type-options': 'nosniff',
    })
    response.end(payload)
    return
  }
  if (
    request.method === 'GET'
    && url.pathname === '/api/v1/admin/audit/export'
  ) {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, 'ADMIN_FORBIDDEN')
      return
    }
    const format = url.searchParams.get('format')
    if (format !== 'csv' && format !== 'xlsx') {
      error(response, 400, 'ADMIN_INVALID_EXPORT_FORMAT')
      return
    }
    response.writeHead(200, {
      'content-type': format === 'csv'
        ? 'text/csv; charset=utf-8'
        : 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      'content-disposition': `attachment; filename="postqron-admin-audit.${format}"`,
    })
    response.end(format === 'csv'
      ? 'code,actor_id,subject_id\n'
      : 'PKfixture')
    return
  }
  if (
    request.method === 'GET'
    && /^\/api\/v1\/admin\/audit\/[^/]+$/u.test(url.pathname)
  ) {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, 'ADMIN_FORBIDDEN')
      return
    }
    const eventID = decodeURIComponent(url.pathname.split('/').at(-1))
    const event = state.audit.find(item => item.id === eventID)
    if (!event) {
      error(response, 404, 'ADMIN_AUDIT_NOT_FOUND')
      return
    }
    json(response, 200, event)
    return
  }
  if (
    request.method === 'GET'
    && /^\/api\/v1\/admin\/users\/[^/]+$/u.test(url.pathname)
  ) {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, role ? 'ADMIN_FORBIDDEN' : 'ADMIN_UNAUTHENTICATED')
      return
    }
    const accountId = decodeURIComponent(url.pathname.split('/').at(-1))
    const user = adminUsers.find(item => item.id === accountId)
    if (!user) {
      error(response, 404, 'ADMIN_NOT_FOUND')
      return
    }
    json(response, 200, user)
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/admin/workspaces') {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, role ? 'ADMIN_FORBIDDEN' : 'ADMIN_UNAUTHENTICATED')
      return
    }
    json(response, 200, directoryPage(
      filteredAdminWorkspaces(url),
      url,
      'updated_at',
    ))
    return
  }
  if (
    request.method === 'GET'
    && url.pathname === '/api/v1/admin/workspaces/export'
  ) {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, role ? 'ADMIN_FORBIDDEN' : 'ADMIN_UNAUTHENTICATED')
      return
    }
    const format = url.searchParams.get('format')
    const contentType = format === 'xlsx'
      ? 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'
      : 'text/csv; charset=utf-8'
    const payload = format === 'xlsx'
      ? Buffer.from('PK\u0003\u0004fixture-xlsx')
      : Buffer.from(directoryCSV('workspaces', filteredAdminWorkspaces(url)), 'utf8')
    response.writeHead(200, {
      'content-type': contentType,
      'content-disposition': `attachment; filename="postqron-admin-workspaces-20260725.${format}"`,
      'cache-control': 'no-store',
      'x-content-type-options': 'nosniff',
    })
    response.end(payload)
    return
  }
  if (
    /^\/api\/v1\/admin\/workspaces\/[^/]+\/internal-plan$/u.test(url.pathname)
    && (request.method === 'PUT' || request.method === 'DELETE')
  ) {
    if (role !== 'admin') {
      error(response, role ? 403 : 401, 'ADMIN_FORBIDDEN')
      return
    }
    if (
      request.headers['x-csrf-token'] !== 'fixture-csrf'
      || !request.headers['idempotency-key']
    ) {
      error(response, 403, 'ADMIN_CSRF_INVALID')
      return
    }
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    if (input.confirmed !== true || typeof input.reason !== 'string') {
      error(response, 400, 'ADMIN_INVALID_REQUEST')
      return
    }
    state.internal = request.method === 'PUT'
    const event = {
      id: `audit-event-${state.audit.length + 1}`,
      code: state.internal ? 'internal_plan.assign' : 'internal_plan.revoke',
      actor_id: 'account-admin',
      subject_id: 'workspace-fixture',
      reason: input.reason,
      outcome: 'success',
      correlation_id: `correlation-${state.audit.length + 1}`,
      occurred_at: now,
    }
    state.audit.push(event)
    json(response, 200, {
      code: event.code,
      correlation_id: event.correlation_id,
    })
    return
  }
  if (
    request.method === 'GET'
    && /^\/api\/v1\/workspaces\/[^/]+\/billing$/u.test(url.pathname)
  ) {
    if (!role) {
      error(response, 401, 'unauthenticated')
      return
    }
    json(response, 200, overview())
    return
  }
  if (
    request.method === 'POST'
    && /^\/api\/v1\/workspaces\/[^/]+\/billing\/checkout$/u.test(url.pathname)
  ) {
    if (!role) {
      error(response, 401, 'unauthenticated')
      return
    }
    json(response, 201, {
      id: 'txn_fixture01',
      url: 'https://pay.paddle.io/checkout/fixture',
      expires_at: '2026-07-26T12:00:00.000Z',
    })
    return
  }
  if (
    request.method === 'POST'
    && /^\/api\/v1\/workspaces\/[^/]+\/billing\/portal$/u.test(url.pathname)
  ) {
    if (!role) {
      error(response, 401, 'unauthenticated')
      return
    }
    json(response, 201, {
      url: 'https://customer-portal.paddle.com/fixture',
    })
    return
  }
  if (
    request.method === 'POST'
    && url.pathname === '/api/v1/billing/paddle/webhook'
  ) {
    const raw = await body(request)
    const header = String(request.headers['paddle-signature'] || '')
    const match = /^ts=(\d+);h1=([a-f0-9]{64})$/u.exec(header)
    if (!match) {
      error(response, 400, 'invalid_signature')
      return
    }
    const expected = createHmac('sha256', signingKey)
      .update(`${match[1]}:${raw}`)
      .digest('hex')
    const supplied = Buffer.from(match[2], 'hex')
    const valid = supplied.length === 32
      && timingSafeEqual(supplied, Buffer.from(expected, 'hex'))
    if (!valid) {
      error(response, 400, 'invalid_signature')
      return
    }
    const event = JSON.parse(raw.toString('utf8'))
    state.entitlement = event.data?.plan === 'team' ? 'team' : 'pro'
    json(response, 200, { accepted: true })
    return
  }
  error(response, 404, 'FIXTURE_NOT_FOUND')
})

server.listen(port, host, () => {
  process.stdout.write(`launch fixture listening on http://${host}:${port}\n`)
})

let stopping = false
function stop() {
  if (stopping) {
    return
  }
  stopping = true
  clearInterval(supervisorWatchdog)
  server.close(() => process.exit(0))
  setTimeout(() => process.exit(0), 2_000).unref()
}
const supervisorWatchdog = setInterval(() => {
  if (!supervisorPid) {
    return
  }
  try {
    process.kill(supervisorPid, 0)
  }
  catch {
    stop()
  }
}, 500)
supervisorWatchdog.unref()

process.once('SIGHUP', stop)
process.once('SIGINT', stop)
process.once('SIGTERM', stop)
