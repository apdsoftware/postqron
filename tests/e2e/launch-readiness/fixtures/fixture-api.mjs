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
  catalog_version: 'd07-v1',
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
        members: 1,
        channels: 50,
        scheduled_publications: 500,
        scheduled_publications_per_channel: 500,
      },
    },
    {
      code: 'team',
      name: 'Team',
      purchasable: true,
      prices: { monthly: money(900), annual: money(9000) },
      price_tiers: tiers(900, 600, 450),
      limits: {
        members: 15,
        channels: 50,
        scheduled_publications: 500,
        scheduled_publications_per_channel: 500,
      },
      trial: {
        days: 14,
        members: 15,
        channels: 10,
        scheduled_publications_per_channel: 500,
      },
    },
  ],
}

let state

function reset() {
  state = {
    cookies: new Map(),
    entitlement: 'start',
    internal: false,
    audit: [],
    health: 'operational',
  }
}
reset()

function sessionRole(request) {
  const cookie = request.headers.cookie || ''
  return /(?:^|;\s*)postqron_fixture_session=admin(?:;|$)/u.test(cookie)
    ? 'admin'
    : /(?:^|;\s*)postqron_fixture_session=normal(?:;|$)/u.test(cookie)
      ? 'normal'
      : /(?:^|;\s*)postqron_fixture_session=authenticated(?:;|$)/u.test(cookie)
        ? 'authenticated'
        : undefined
}

function appSession(role = 'authenticated') {
  const locale = role === 'admin' ? 'de' : role === 'normal' ? 'fr' : 'it'
  return {
    account: {
      id: `account-${role}`,
      display_name: role === 'admin' ? 'Fixture Admin' : 'Fixture User',
      email: `${role}@example.test`,
      locale,
    },
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

function overview() {
  const paid = state.entitlement !== 'start'
  const plan = catalog.plans.find(item => item.code === state.entitlement)
  return {
    plan,
    interval: 'monthly',
    state: 'active',
    period: {
      start: '2026-07-01T00:00:00.000Z',
      end: '2026-08-01T00:00:00.000Z',
    },
    usage: [
      { resource: 'members', used: 1, limit: plan.limits.members, remaining: plan.limits.members - 1, over_limit: false },
      { resource: 'channels', used: paid ? 4 : 1, limit: paid ? 10 : 3, remaining: paid ? 6 : 2, over_limit: false },
      { resource: 'scheduled_publications', used: 2, limit: plan.limits.scheduled_publications, remaining: plan.limits.scheduled_publications - 2, over_limit: false },
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
    json(response, 200, {
      auth_methods: ['password'],
      providers: ['google', 'apple', 'facebook', 'linkedin'],
      legal_documents: [
        { key: 'terms', version: '1.0', href: '/legal/termini', digest_sha256: digest },
        { key: 'privacy', version: '1.0', href: '/legal/privacy', digest_sha256: digest },
      ],
      ...(role ? { session: appSession(role) } : {}),
    })
    return
  }
  if (request.method === 'POST' && url.pathname === '/api/v1/auth/password/login') {
    const input = JSON.parse((await body(request)).toString('utf8') || '{}')
    if (
      input.email !== 'admin@example.test'
      || input.password !== 'fixture-admin-password'
    ) {
      error(response, 401, 'AUTH_INVALID_CREDENTIALS')
      return
    }
    json(response, 200, { authenticated: true }, {
      'set-cookie': 'postqron_fixture_session=admin; Path=/; HttpOnly; SameSite=Lax',
    })
    return
  }
  if (request.method === 'GET' && url.pathname === '/api/v1/app/session') {
    if (!role) {
      error(response, 401, 'APP_SESSION_REQUIRED')
      return
    }
    json(response, 200, appSession(role))
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
      id: `audit-${state.audit.length + 1}`,
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
