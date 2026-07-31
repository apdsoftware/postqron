import { createRequire } from 'node:module'
import {
  fixtureReset,
  offBaseURL,
} from '../../../tests/e2e/launch-readiness/helpers.ts'
import { socialBootstrapFixture } from './fixtures.ts'
import type { Page } from '@playwright/test'

const require = createRequire(
  new URL('../../../tests/e2e/launch-readiness/package.json', import.meta.url),
)
const { expect, test } = require('@playwright/test') as typeof import('@playwright/test')
const AxeBuilder = require('@axe-core/playwright').default as typeof import('@axe-core/playwright').default

function json(body: unknown): {
  body: string
  contentType: string
  headers: Record<string, string>
  status: number
} {
  return {
    status: 200,
    contentType: 'application/json',
    headers: {
      'access-control-allow-credentials': 'true',
      'access-control-allow-origin': offBaseURL,
    },
    body: JSON.stringify(body),
  }
}

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

function socialSelection(displayName = 'A Pending Resource') {
  return {
    selection_id: 'selection-a',
    provider: 'bluesky',
    expires_at: '2026-07-31T12:15:00.000Z',
    resources: [{
      remote_id: 'did:plc:pending-a',
      resource_type: 'bluesky_account',
      account_type: 'profile',
      display_name: displayName,
      handle: 'pending-a.test',
      scopes: ['atproto'],
    }],
  }
}

function session(role: 'owner' | 'member' = 'owner') {
  return {
    account: {
      id: 'account-1',
      display_name: 'Fixture User',
      email: 'fixture@example.test',
      email_verified: true,
      locale: 'en',
    },
    authenticated_at: '2026-07-31T12:00:00.000Z',
    current_workspace: {
      id: 'workspace-fixture',
      name: 'Fixture Workspace',
      role,
    },
    onboarding_required: false,
    workspaces: [{
      id: 'workspace-fixture',
      name: 'Fixture Workspace',
      role,
    }],
  }
}

function currentWorkspace(role: 'owner' | 'member' = 'owner') {
  return {
    id: 'workspace-fixture',
    name: 'Fixture Workspace',
    role,
    status: 'active',
    created_at: '2026-07-01T10:00:00.000Z',
    updated_at: '2026-07-31T12:00:00.000Z',
  }
}

function socialBootstrap() {
  const bootstrap = socialBootstrapFixture()
  const bluesky = bootstrap.catalog.find(provider => provider.provider === 'bluesky')
  if (bluesky) {
    bluesky.status = 'available'
    bluesky.configuration_state = 'ready'
    bluesky.resources = [{
      resource_type: 'bluesky_account',
      account_types: ['profile'],
      publishing_modes: ['auto'],
    }]
    bluesky.capabilities.authorization = true
    bluesky.capabilities.authenticated_http = true
    bluesky.capabilities.access_token_hash = true
    bluesky.capabilities.dynamic_discovery = true
    bluesky.capabilities.pkce = true
    bluesky.capabilities.remote_revocation = true
    bluesky.capabilities.resource_selection = true
  }
  bootstrap.providers.push({
    provider: 'bluesky',
    status: 'available',
    configuration_state: 'ready',
    retryable: false,
  })
  return bootstrap
}

function socialConnection(status: 'connected' | 'reconnect_required' = 'connected') {
  return {
    id: 'connection-1',
    workspace_id: 'workspace-fixture',
    provider: 'bluesky',
    remote_id: 'did:plc:alice',
    resource_type: 'bluesky_account',
    account_type: 'profile',
    display_name: 'Alice',
    handle: 'alice.test',
    scopes: ['atproto'],
    status,
    reconnect_reason: status === 'reconnect_required'
      ? 'authentication_revoked'
      : undefined,
    last_verified_at: '2026-07-31T12:00:00.000Z',
    created_at: '2026-07-31T11:00:00.000Z',
    updated_at: '2026-07-31T12:00:00.000Z',
  }
}

async function routeSocialPage(page: Page, role: 'owner' | 'member') {
  let sessionRole = role
  let workspaceRole = role
  let sessionRequests = 0
  let connections = role === 'owner' ? [] as Array<ReturnType<typeof socialConnection>> : [socialConnection('reconnect_required')]
  let beginBody: unknown
  const callbackRequests: URL[] = []

  await page.route('**/api/v1/app/session', (route) => {
    sessionRequests += 1
    return route.fulfill(json(session(sessionRole)))
  })
  await page.route('**/api/v1/app/workspaces/current', route =>
    route.fulfill(json(currentWorkspace(workspaceRole))))
  await page.route(
    '**/api/v1/workspaces/workspace-fixture/social-connections/bootstrap',
    route => route.fulfill(json(socialBootstrap())),
  )
  await page.route(
    '**/api/v1/workspaces/workspace-fixture/social-connections',
    async route => {
      const request = route.request()
      if (request.method() === 'POST') {
        connections = [socialConnection()]
        await route.fulfill(json(connections[0]))
        return
      }
      await route.fulfill(json({ connections }))
    },
  )
  await page.route(
    '**/api/v1/workspaces/workspace-fixture/social-authorizations',
    async route => {
      beginBody = route.request().postDataJSON()
      await route.fulfill(json({
        authorization_url: 'https://social-provider.example.test/oauth/start',
        expires_at: '2026-07-31T12:10:00.000Z',
      }))
    },
  )
  await page.context().route('https://social-provider.example.test/oauth/start', route =>
    route.fulfill({
      status: 200,
      contentType: 'text/html',
      body: `<script>location.replace(${JSON.stringify(`${offBaseURL}/app/social-oauth/callback?state=state-1&code=code-1&iss=https%3A%2F%2Fbsky.social`)})</script>`,
    }))
  await page.context().route('**/api/v1/social-authorizations/callback*', async route => {
    callbackRequests.push(new URL(route.request().url()))
    const payload = {
      selection_id: 'selection-1',
      provider: 'bluesky',
      expires_at: '2026-07-31T12:15:00.000Z',
      resources: [{
        remote_id: 'did:plc:alice',
        resource_type: 'bluesky_account',
        account_type: 'profile',
        display_name: 'Alice',
        handle: 'alice.test',
        scopes: ['atproto'],
      }],
    }
    await route.fulfill(json(payload))
  })

  return {
    beginBody: () => beginBody,
    callbackRequests,
    sessionRequests: () => sessionRequests,
    setSessionRole(nextRole: 'owner' | 'member') {
      sessionRole = nextRole
    },
    setWorkspaceRole(nextRole: 'owner' | 'member') {
      workspaceRole = nextRole
    },
  }
}

async function routeWorkspaceSwitchPage(
  page: Page,
  options: {
    aConnectionStatus?: 'connected' | 'reconnect_required'
    failFirstSessionRefresh?: boolean
    failLostResponseRecoverySession?: boolean
    failNextSwitch?: boolean
    failRollback?: boolean
    loseNextSwitchResponse?: boolean
  } = {},
) {
  const roles = {
    'workspace-a': 'owner',
    'workspace-b': 'member',
  } as const
  let activeWorkspace: keyof typeof roles = 'workspace-a'
  let failNextSwitch = options.failNextSwitch ?? false
  let failFirstSessionRefresh = options.failFirstSessionRefresh ?? false
  let loseNextSwitchResponse = options.loseNextSwitchResponse ?? false
  let failedSessionRefreshRequests = 0
  let rollbackAttempts = 0
  const listRequests = { 'workspace-a': 0, 'workspace-b': 0 }
  const names = {
    'workspace-a': 'Workspace A',
    'workspace-b': 'Workspace B',
  } as const

  function activeSession() {
    return {
      ...session(roles[activeWorkspace]),
      current_workspace: {
        id: activeWorkspace,
        name: names[activeWorkspace],
        role: roles[activeWorkspace],
      },
      workspaces: (Object.keys(roles) as Array<keyof typeof roles>).map(id => ({
        id,
        name: names[id],
        role: roles[id],
      })),
    }
  }

  await page.route('**/api/v1/auth/csrf', route =>
    route.fulfill(json({ csrf_token: 'workspace-switch-csrf' })))
  await page.route('**/api/v1/app/workspaces/select', async route => {
    const targetWorkspace = (
      route.request().postDataJSON() as { workspace_id: keyof typeof roles }
    ).workspace_id
    if (failNextSwitch) {
      failNextSwitch = false
      await route.fulfill({
        ...json({
          error: {
            code: 'workspace_switch_failed',
            message: 'The workspace switch failed.',
            retryable: true,
          },
        }),
        status: 503,
      })
      return
    }
    if (loseNextSwitchResponse && targetWorkspace === 'workspace-b') {
      loseNextSwitchResponse = false
      // The server commits before the transport fails, faithfully modelling a
      // timeout/lost response after the mutation reached the backend.
      activeWorkspace = targetWorkspace
      if (options.failLostResponseRecoverySession) {
        failedSessionRefreshRequests = 2
      }
      await route.abort('timedout')
      return
    }
    if (targetWorkspace === 'workspace-a' && activeWorkspace === 'workspace-b') {
      rollbackAttempts += 1
      if (options.failRollback) {
        await route.fulfill({
          ...json({
            error: {
              code: 'workspace_rollback_failed',
              message: 'The workspace rollback failed.',
              retryable: true,
            },
          }),
          status: 503,
        })
        return
      }
    }

    // A successful selection POST commits server state immediately. Any
    // following session failure must therefore be treated as post-commit.
    activeWorkspace = targetWorkspace
    if (failFirstSessionRefresh && targetWorkspace === 'workspace-b') {
      failFirstSessionRefresh = false
      // ofetch retries a failed GET once; fail both attempts belonging to the
      // first api.session() so the layout's explicit rollback path executes.
      failedSessionRefreshRequests = 2
    }
    await route.fulfill(json({ ok: true }))
  })
  await page.route('**/api/v1/app/session', async route => {
    if (failedSessionRefreshRequests > 0) {
      failedSessionRefreshRequests -= 1
      await route.fulfill({
        ...json({
          error: {
            code: 'session_refresh_failed',
            message: 'The session refresh failed.',
            retryable: true,
          },
        }),
        status: 503,
      })
      return
    }
    await route.fulfill(json(activeSession()))
  })
  await page.route('**/api/v1/app/workspaces/current', async route => {
    const requestedWorkspace = activeWorkspace
    if (requestedWorkspace === 'workspace-b') {
      await new Promise(resolve => globalThis.setTimeout(resolve, 250))
    }
    await route.fulfill(json({
      ...currentWorkspace(roles[requestedWorkspace]),
      id: requestedWorkspace,
      name: names[requestedWorkspace],
    }))
  })
  for (const workspaceId of Object.keys(roles) as Array<keyof typeof roles>) {
    await page.route(
      `**/api/v1/workspaces/${workspaceId}/social-connections/bootstrap`,
      async route => {
        if (workspaceId === 'workspace-b') {
          await new Promise(resolve => globalThis.setTimeout(resolve, 250))
        }
        await route.fulfill(json(socialBootstrap()))
      },
    )
    await page.route(
      `**/api/v1/workspaces/${workspaceId}/social-connections`,
      async route => {
        listRequests[workspaceId] += 1
        if (workspaceId === 'workspace-b') {
          await new Promise(resolve => globalThis.setTimeout(resolve, 250))
        }
        await route.fulfill(json({
          connections: [{
            ...socialConnection(workspaceId === 'workspace-a'
              ? options.aConnectionStatus
              : 'connected'),
            id: `connection-${workspaceId}`,
            workspace_id: workspaceId,
            display_name: workspaceId === 'workspace-a'
              ? 'A Owner Channel'
              : 'B Member Channel',
          }],
        }))
      },
    )
  }
  return {
    activeWorkspace: () => activeWorkspace,
    listRequests,
    rollbackAttempts: () => rollbackAttempts,
  }
}

async function routeComposerPage(
  page: Page,
  options: { ambiguousFirstSchedule?: boolean, validationValid?: boolean } = {},
) {
  let draftRevision = 0
  let scheduleRequests = 0
  const scheduleBodies: unknown[] = []
  const scheduleKeys: string[] = []
  const scheduledOperations = new Map<string, { body: string, response: unknown }>()
  let draftContent = {
    text: '',
    link: '',
    media: [] as Array<{
      id: string
      kind: 'image'
      content_type: string
      size_bytes: number
      inspection_status: 'ready'
      url: string
    }>,
    thread: [] as Array<{ text: string, media_ids: string[] }>,
    destinations: [] as Array<Record<string, unknown>>,
  }

  await page.addInitScript(() => {
    const originalFetch = globalThis.fetch.bind(globalThis)
    globalThis.fetch = async (input, init) => {
      const url = input instanceof Request
        ? input.url
        : typeof input === 'string'
          ? input
          : String(input)
      const method = input instanceof Request
        ? input.method
        : init?.method ?? 'GET'
      if (url === 'https://uploads.example.test/media-1' && method === 'PUT') {
        return new Response('', { status: 200 })
      }
      return originalFetch(input as Parameters<typeof fetch>[0], init)
    }
  })

  const draftView = () => ({
    draft: {
      id: 'draft-1',
      workspace_id: 'workspace-fixture',
      created_by: 'account-1',
      content: draftContent,
      revision: Math.max(1, draftRevision),
      created_at: '2026-07-31T11:00:00.000Z',
      updated_at: '2026-07-31T12:00:00.000Z',
    },
    validation: {
      capability_version: 'fixture-v1',
      valid: true,
      errors: [],
      destinations: [],
    },
  })

  await page.route('**/*', async route => {
    const request = route.request()
    const url = new URL(request.url())

    if (url.pathname === '/api/v1/app/session') {
      await route.fulfill(json(session('owner')))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/social-connections') {
      await route.fulfill(json({
        connections: [{
          id: 'threads-1',
          workspace_id: 'workspace-fixture',
          provider: 'threads',
          remote_id: 'threads-profile-1',
          resource_type: 'threads_profile',
          account_type: 'profile',
          display_name: 'Launch Thread',
          handle: 'launch.thread',
          scopes: ['threads_basic'],
          status: 'connected',
          last_verified_at: '2026-07-31T12:00:00.000Z',
          created_at: '2026-07-31T11:00:00.000Z',
          updated_at: '2026-07-31T12:00:00.000Z',
        }],
      }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/composer/capabilities') {
      await route.fulfill(json({
        version: 'fixture-v1',
        status: 'ready',
        capabilities: [{
          id: 'threads:thread',
          provider: 'threads',
          channel_type: 'threads_profile',
          format: 'thread',
          available: true,
          text: { allowed: true, required: false, max_characters: 500 },
          link: { allowed: false, required: false },
          media: {
            allowed: true,
            allowed_kinds: ['image'],
            minimum_items: 0,
            maximum_items: 4,
          },
          thread: {
            allowed: true,
            required: true,
            minimum_items: 1,
            maximum_items: 4,
            max_item_characters: 280,
            max_media_per_item: 1,
          },
        }],
      }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/drafts'
      && request.method() === 'POST') {
      draftRevision = 1
      draftContent = request.postDataJSON().content
      await route.fulfill(json(draftView()))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/drafts/draft-1'
      && (request.method() === 'PATCH' || request.method() === 'PUT')) {
      draftRevision += 1
      draftContent = request.postDataJSON().content
      await route.fulfill(json(draftView()))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/drafts/draft-1/validate') {
      const valid = options.validationValid ?? false
      await route.fulfill(json({
        validation: {
          capability_version: 'fixture-v1',
          valid,
          errors: valid ? [] : [{
            field: 'thread[0].text',
            rule: 'required',
            code: 'text_required',
            message: 'RAW_BACKEND_THREAD_MESSAGE',
          }],
          destinations: [],
        },
      }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/composer/media'
      && request.method() === 'POST') {
      await route.fulfill(json({
        id: 'media-1',
        status: 'pending',
        upload_url: 'https://uploads.example.test/media-1',
        upload_headers: { 'content-type': 'image/png' },
        complete_url: '/api/v1/workspaces/workspace-fixture/composer/media/media-1/complete',
        expires_at: '2026-07-31T12:10:00.000Z',
        max_bytes: 10485760,
      }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/composer/media/media-1/complete') {
      draftContent.media = [{
        id: 'media-1',
        kind: 'image',
        content_type: 'image/png',
        size_bytes: 2048,
        inspection_status: 'ready',
        url: '/api/v1/workspaces/workspace-fixture/composer/media/media-1/file',
      }]
      await route.fulfill(json({
        id: 'media-1',
        kind: 'image',
        content_type: 'image/png',
        size_bytes: 2048,
        inspection_status: 'ready',
        url: '/api/v1/workspaces/workspace-fixture/composer/media/media-1/file',
      }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/scheduled-posts'
      && request.method() === 'POST') {
      scheduleRequests += 1
      const key = request.headers()['idempotency-key'] ?? ''
      const body = request.postData() ?? ''
      scheduleKeys.push(key)
      scheduleBodies.push(request.postDataJSON())
      if (!key) {
        await route.fulfill({
          ...json({
            error: {
              code: 'idempotency_key_invalid',
              retryable: false,
            },
          }),
          status: 400,
        })
        return
      }
      const existing = scheduledOperations.get(key)
      if (existing && existing.body !== body) {
        await route.fulfill({
          ...json({
            error: {
              code: 'idempotency_payload_mismatch',
              retryable: false,
            },
          }),
          status: 409,
        })
        return
      }
      const response = existing?.response ?? {
        id: 'post-1',
        workspace_id: 'workspace-fixture',
        draft_id: 'draft-1',
        channel_ids: ['threads-1'],
        status: 'scheduled',
        scheduled_for_utc: '2026-07-31T16:00:00.000Z',
        scheduled_local: '2026-07-31T12:00:00',
        time_zone: 'America/Santo_Domingo',
        utc_offset_minutes: -240,
        revision: 1,
        created_at: '2026-07-31T12:00:00.000Z',
        updated_at: '2026-07-31T12:00:00.000Z',
      }
      scheduledOperations.set(key, { body, response })
      if (options.ambiguousFirstSchedule && scheduleRequests === 1) {
        await route.abort('failed')
        return
      }
      const responseFixture = json(response)
      await route.fulfill({
        ...responseFixture,
        status: 201,
        headers: {
          ...responseFixture.headers,
          ...(existing ? { 'idempotency-replayed': 'true' } : {}),
        },
      })
      return
    }

    await route.continue()
  })

  return {
    scheduleBodies,
    scheduleKeys,
    scheduleRequests: () => scheduleRequests,
    scheduledOperations,
  }
}

async function routeCalendarPage(
  page: Page,
  options: { ambiguousFirstDuplicate?: boolean } = {},
) {
  const calendarRequests: string[] = []
  const rescheduleBodies: unknown[] = []
  const duplicateBodies: unknown[] = []
  const duplicateKeys: string[] = []
  const duplicateOperations = new Map<string, string>()

  await page.route('**/*', async route => {
    const request = route.request()
    const url = new URL(request.url())

    if (url.pathname === '/api/v1/app/session') {
      await route.fulfill(json(session('owner')))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/social-connections') {
      await route.fulfill(json({
        connections: [{
          id: 'channel-1',
          workspace_id: 'workspace-fixture',
          provider: 'youtube',
          remote_id: 'youtube-1',
          resource_type: 'youtube_channel',
          account_type: 'channel',
          display_name: 'Launch Channel',
          handle: 'launch-channel',
          scopes: ['youtube.upload'],
          status: 'connected',
          last_verified_at: '2026-07-31T12:00:00.000Z',
          created_at: '2026-07-31T11:00:00.000Z',
          updated_at: '2026-07-31T12:00:00.000Z',
        }],
      }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/drafts') {
      await route.fulfill(json({
        drafts: [{
          draft: {
            id: 'draft-1',
            workspace_id: 'workspace-fixture',
            created_by: 'account-1',
            content: {
              text: 'March launch',
              link: '',
              media: [],
              thread: [],
              destinations: [],
            },
            revision: 1,
            created_at: '2026-03-01T08:00:00.000Z',
            updated_at: '2026-03-01T08:00:00.000Z',
          },
          validation: {
            capability_version: 'fixture-v1',
            valid: true,
            errors: [],
            destinations: [],
          },
        }, {
          draft: {
            id: 'draft-3',
            workspace_id: 'workspace-fixture',
            created_by: 'account-1',
            content: {
              text: 'April boundary',
              link: '',
              media: [],
              thread: [],
              destinations: [],
            },
            revision: 1,
            created_at: '2026-03-03T08:00:00.000Z',
            updated_at: '2026-03-03T08:00:00.000Z',
          },
          validation: {
            capability_version: 'fixture-v1',
            valid: true,
            errors: [],
            destinations: [],
          },
        }, {
          draft: {
            id: 'draft-4',
            workspace_id: 'workspace-fixture',
            created_by: 'account-1',
            content: {
              text: 'March boundary',
              link: '',
              media: [],
              thread: [],
              destinations: [],
            },
            revision: 1,
            created_at: '2026-02-28T08:00:00.000Z',
            updated_at: '2026-02-28T08:00:00.000Z',
          },
          validation: {
            capability_version: 'fixture-v1',
            valid: true,
            errors: [],
            destinations: [],
          },
        }, {
          draft: {
            id: 'draft-2',
            workspace_id: 'workspace-fixture',
            created_by: 'account-1',
            content: {
              text: 'Published launch',
              link: '',
              media: [],
              thread: [],
              destinations: [],
            },
            revision: 1,
            created_at: '2026-03-02T08:00:00.000Z',
            updated_at: '2026-03-02T08:00:00.000Z',
          },
          validation: {
            capability_version: 'fixture-v1',
            valid: true,
            errors: [],
            destinations: [],
          },
        }],
      }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/calendar') {
      calendarRequests.push(url.search)
      await route.fulfill(json({
        entries: [{
          post_id: 'boundary-1',
          draft_id: 'draft-4',
          channel_ids: ['channel-1'],
          status: 'scheduled',
          scheduled_for_utc: '2026-02-28T23:30:00.000Z',
          scheduled_local: '2026-03-01T00:30:00',
          time_zone: 'Europe/Rome',
          utc_offset_minutes: 60,
          revision: 1,
        }, {
          post_id: 'scheduled-1',
          draft_id: 'draft-1',
          channel_ids: ['channel-1'],
          status: 'scheduled',
          scheduled_for_utc: '2026-03-10T09:00:00.000Z',
          scheduled_local: '2026-03-10T10:00:00',
          time_zone: 'Europe/Rome',
          utc_offset_minutes: 60,
          revision: 2,
        }, {
          post_id: 'published-1',
          draft_id: 'draft-2',
          channel_ids: ['channel-1'],
          status: 'published',
          scheduled_for_utc: '2026-03-12T18:00:00.000Z',
          scheduled_local: '2026-03-12T14:00:00',
          time_zone: 'America/Santo_Domingo',
          utc_offset_minutes: -240,
          revision: 1,
        }, {
          post_id: 'boundary-2',
          draft_id: 'draft-3',
          channel_ids: ['channel-1'],
          status: 'scheduled',
          scheduled_for_utc: '2026-04-01T00:30:00.000Z',
          scheduled_local: '2026-04-01T02:30:00',
          time_zone: 'Europe/Rome',
          utc_offset_minutes: 120,
          revision: 1,
        }],
      }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/scheduled-posts/scheduled-1/reschedule'
      && request.method() === 'POST') {
      const body = request.postDataJSON() as {
        scheduled_at: {
          local_date_time: string
          time_zone: string
          utc_offset_minutes: number
        }
      }
      rescheduleBodies.push(body)
      await route.fulfill(json({
        id: 'scheduled-1',
        workspace_id: 'workspace-fixture',
        draft_id: 'draft-1',
        channel_ids: ['channel-1'],
        status: 'scheduled',
        scheduled_for_utc: '2026-07-10T08:00:00.000Z',
        scheduled_local: body.scheduled_at.local_date_time,
        time_zone: body.scheduled_at.time_zone,
        utc_offset_minutes: body.scheduled_at.utc_offset_minutes,
        revision: 3,
        created_at: '2026-03-01T08:00:00.000Z',
        updated_at: '2026-03-01T08:00:00.000Z',
      }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/scheduled-posts/scheduled-1/duplicate'
      && request.method() === 'POST') {
      const key = request.headers()['idempotency-key'] ?? ''
      const body = request.postData() ?? ''
      duplicateKeys.push(key)
      duplicateBodies.push(request.postDataJSON())
      if (!key) {
        await route.fulfill({
          ...json({ error: { code: 'idempotency_key_invalid', retryable: false } }),
          status: 400,
        })
        return
      }
      const existing = duplicateOperations.get(key)
      if (existing !== undefined && existing !== body) {
        await route.fulfill({
          ...json({ error: { code: 'idempotency_payload_mismatch', retryable: false } }),
          status: 409,
        })
        return
      }
      duplicateOperations.set(key, body)
      if (options.ambiguousFirstDuplicate && duplicateKeys.length === 1) {
        await route.abort('failed')
        return
      }
      const responseFixture = json({
        id: 'duplicate-1',
        workspace_id: 'workspace-fixture',
        draft_id: 'draft-duplicate-1',
        channel_ids: ['channel-1'],
        status: 'scheduled',
        scheduled_for_utc: '2026-03-10T09:00:00.000Z',
        scheduled_local: '2026-03-10T10:00:00',
        time_zone: 'Europe/Rome',
        utc_offset_minutes: 60,
        revision: 1,
        duplicated_from_post_id: 'scheduled-1',
        created_at: '2026-03-01T08:00:00.000Z',
        updated_at: '2026-03-01T08:00:00.000Z',
      })
      await route.fulfill({
        ...responseFixture,
        status: 201,
        headers: {
          ...responseFixture.headers,
          ...(existing !== undefined ? { 'idempotency-replayed': 'true' } : {}),
        },
      })
      return
    }

    await route.continue()
  })

  return {
    calendarRequests,
    duplicateBodies,
    duplicateKeys,
    duplicateOperations,
    rescheduleBodies,
  }
}

test.beforeEach(async () => {
  await fixtureReset()
})

test('owner social flow captures typed discovery, completes callback selection, cleans URL, mobile layout, and axe', async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 880 })
  const social = await routeSocialPage(page, 'owner')

  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await expect(page.getByRole('heading', { name: 'Social channels' })).toBeVisible()

  const provider = page.locator('.app-provider-catalog li').filter({ hasText: 'Bluesky' })
  await provider.getByLabel('Discovery type').selectOption('did')
  await provider.getByLabel('Discovery value').fill('did:plc:alice')
  const popupPromise = page.waitForEvent('popup')
  await provider.getByRole('button', { name: 'Connect' }).click()
  const popup = await popupPromise
  expect(social.beginBody()).toEqual({
    provider: 'bluesky',
    discovery: {
      kind: 'did',
      value: 'did:plc:alice',
    },
  })
  await popup.waitForURL(`${offBaseURL}/en/app/social-oauth/callback`)
  await expect(popup.locator('[data-postqron-social-callback-handoff]')).toBeAttached()

  await expect(page.getByRole('heading', { name: 'Choose what to connect' })).toBeVisible()
  expect(social.callbackRequests).toHaveLength(1)
  expect(social.callbackRequests[0]?.searchParams.get('state')).toBe('state-1')
  expect(social.callbackRequests[0]?.searchParams.get('iss')).toBe('https://bsky.social')
  await page.getByRole('button', { name: 'Connect this resource' }).click()

  await expect(page.locator('.app-provider-list')).toContainText('Alice')
  await expect(page).toHaveURL(`${offBaseURL}/en/app/social-channels`)
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth <= globalThis.innerWidth)).toBe(true)

  const results = await new AxeBuilder({ page }).include('main').analyze()
  expect(results.violations).toEqual([])
})

test('member workspace stays fail-closed for connect, reconnect, and revoke', async ({
  page,
}) => {
  await routeSocialPage(page, 'member')

  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await expect(page.getByText(
    'Only an Owner on an active workspace can connect, reconnect, select, or revoke channels.',
  )).toBeVisible()

  const provider = page.locator('.app-provider-catalog li').filter({ hasText: 'Bluesky' })
  await expect(provider.getByRole('button', { name: 'Connect' })).toBeDisabled()

  const connection = page.locator('.app-provider-list li').filter({ hasText: 'Alice' })
  await expect(connection.getByRole('button', { name: 'Reconnect' })).toBeDisabled()
  await expect(connection.getByRole('button', { name: 'Disconnect' })).toBeDisabled()
})

test('workspace changes clear social state atomically across Owner and Member roles', async ({
  page,
}) => {
  await routeWorkspaceSwitchPage(page)
  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await expect(page.getByText('A Owner Channel')).toBeVisible()

  const workspace = page.getByLabel('Current workspace')
  await workspace.selectOption('workspace-b')
  expect(await page.getByText('A Owner Channel').isVisible()).toBe(false)
  await expect(page.getByText('B Member Channel')).toBeVisible()
  await expect(page.getByText(
    'Only an Owner on an active workspace can connect, reconnect, select, or revoke channels.',
  )).toBeVisible()
  await expect(page.locator('.app-provider-catalog').getByRole('button', { name: 'Connect' }).first()).toBeDisabled()

  await workspace.selectOption('workspace-a')
  expect(await page.getByText('B Member Channel').isVisible()).toBe(false)
  await expect(page.getByText('A Owner Channel')).toBeVisible()
  await expect(page.locator('.app-provider-catalog').getByRole('button', { name: 'Connect' }).first()).toBeEnabled()
})

test('an in-flight connect popup closes on Owner to Member switch and its delayed error is ignored', async ({
  page,
}) => {
  await routeWorkspaceSwitchPage(page)
  const beginRequested = deferred()
  const releaseBegin = deferred()
  await page.route(
    '**/api/v1/workspaces/workspace-a/social-authorizations',
    async route => {
      beginRequested.resolve()
      await releaseBegin.promise
      await route.fulfill({
        ...json({
          code: 'provider_temporary',
          message: 'STALE_A_CONNECT_ERROR',
          retryable: true,
        }),
        status: 503,
      })
    },
  )

  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await expect(page.getByText('A Owner Channel')).toBeVisible()
  const provider = page.locator('.app-provider-catalog li').filter({ hasText: 'Facebook Pages' })
  const popupPromise = page.waitForEvent('popup')
  await provider.getByRole('button', { name: 'Connect' }).click()
  const popup = await popupPromise
  await beginRequested.promise

  const popupClosed = popup.waitForEvent('close')
  await page.getByLabel('Current workspace').selectOption('workspace-b')
  await popupClosed
  releaseBegin.resolve()

  await expect(page.getByText('B Member Channel')).toBeVisible()
  await expect(page.getByText('STALE_A_CONNECT_ERROR')).toHaveCount(0)
  await expect(page.locator('.app-inline-alert[role="alert"]')).toHaveCount(0)
})

test('an in-flight reconnect popup closes on transition and stale callback metadata is discarded', async ({
  page,
}) => {
  await routeWorkspaceSwitchPage(page, { aConnectionStatus: 'reconnect_required' })
  const reconnectRequested = deferred()
  const releaseReconnect = deferred()
  await page.route(
    '**/api/v1/workspaces/workspace-a/social-connections/connection-workspace-a/reconnect',
    async route => {
      reconnectRequested.resolve()
      await releaseReconnect.promise
      await route.fulfill(json({
        authorization_url: 'https://social-provider.example.test/oauth/pending-a',
        expires_at: '2026-07-31T12:10:00.000Z',
      }))
    },
  )
  await page.context().route('https://social-provider.example.test/oauth/pending-a', route =>
    route.fulfill({
      status: 200,
      contentType: 'text/plain',
      body: JSON.stringify(socialSelection('STALE_A_CALLBACK_RESOURCE')),
    }))

  await page.goto(`${offBaseURL}/en/app/social-channels`)
  const popupPromise = page.waitForEvent('popup')
  await page.getByRole('button', { name: 'Reconnect' }).click()
  const popup = await popupPromise
  await reconnectRequested.promise

  const popupClosed = popup.waitForEvent('close')
  await page.getByLabel('Current workspace').selectOption('workspace-b')
  await popupClosed
  releaseReconnect.resolve()

  await expect(page.getByText('B Member Channel')).toBeVisible()
  await expect(page.getByText('STALE_A_CALLBACK_RESOURCE')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Choose what to connect' })).toHaveCount(0)
})

test('a delayed processCallback response cleans stale callback history and cannot expose workspace A selection under workspace B', async ({
  page,
}) => {
  await routeWorkspaceSwitchPage(page)
  const callbackRequested = deferred()
  const releaseCallback = deferred()
  await page.route('**/api/v1/social-authorizations/callback*', async route => {
    callbackRequested.resolve()
    await releaseCallback.promise
    await route.fulfill(json(socialSelection('STALE_A_PROCESS_CALLBACK')))
  })

  await page.goto(`${offBaseURL}/en/app/social-channels?campaign=kept&state=state-a&code=code-a&iss=https%3A%2F%2Fissuer.example.test&error=access_denied`)
  await callbackRequested.promise
  await page.getByLabel('Current workspace').selectOption('workspace-b')
  releaseCallback.resolve()

  await expect(page).toHaveURL(`${offBaseURL}/en/app/social-channels?campaign=kept`)
  await expect(page.getByText('B Member Channel')).toBeVisible()
  await expect(page.getByText('STALE_A_PROCESS_CALLBACK')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Choose what to connect' })).toHaveCount(0)
})

test('a delayed selectResource list from A cannot repopulate B', async ({ page }) => {
  await routeWorkspaceSwitchPage(page)
  const staleListRequested = deferred()
  const releaseStaleList = deferred()
  let resourceSelected = false
  await page.route(
    '**/api/v1/workspaces/workspace-a/social-connections',
    async route => {
      if (route.request().method() === 'POST') {
        resourceSelected = true
        await route.fulfill(json({
          ...socialConnection(),
          id: 'selected-a',
          workspace_id: 'workspace-a',
          display_name: 'Selected A Resource',
        }))
        return
      }
      if (resourceSelected) {
        staleListRequested.resolve()
        await releaseStaleList.promise
        await route.fulfill(json({
          connections: [{
            ...socialConnection(),
            id: 'stale-selected-a',
            workspace_id: 'workspace-a',
            display_name: 'STALE_A_SELECTED_LIST',
          }],
        }))
        return
      }
      await route.fulfill(json({ connections: [
        { ...socialConnection(), workspace_id: 'workspace-a', display_name: 'A Owner Channel' },
      ] }))
    },
  )
  await page.route('**/api/v1/social-authorizations/callback*', route =>
    route.fulfill(json(socialSelection())))

  await page.goto(`${offBaseURL}/en/app/social-channels?state=state-a&code=code-a`)
  await expect(page.getByRole('heading', { name: 'Choose what to connect' })).toBeVisible()
  await page.getByRole('button', { name: 'Connect this resource' }).click()
  await staleListRequested.promise
  await page.getByLabel('Current workspace').selectOption('workspace-b')
  releaseStaleList.resolve()

  await expect(page.getByText('B Member Channel')).toBeVisible()
  await expect(page.getByText('STALE_A_SELECTED_LIST')).toHaveCount(0)
  await expect(page.getByText('Selected A Resource')).toHaveCount(0)
})

test('a delayed disconnect list from A cannot repopulate B or publish stale notice', async ({
  page,
}) => {
  await routeWorkspaceSwitchPage(page)
  const staleListRequested = deferred()
  const releaseStaleList = deferred()
  let revoked = false
  await page.route(
    '**/api/v1/workspaces/workspace-a/social-connections/connection-workspace-a',
    async route => {
      revoked = true
      await route.fulfill(json({
        connection: {
          ...socialConnection(),
          id: 'connection-workspace-a',
          workspace_id: 'workspace-a',
          display_name: 'A Owner Channel',
          status: 'revoked',
          revoked_at: '2026-07-31T12:30:00.000Z',
        },
        provider_revoked: true,
      }))
    },
  )
  await page.route(
    '**/api/v1/workspaces/workspace-a/social-connections',
    async route => {
      if (revoked) {
        staleListRequested.resolve()
        await releaseStaleList.promise
        await route.fulfill(json({
          connections: [{
            ...socialConnection(),
            id: 'stale-revoked-a',
            workspace_id: 'workspace-a',
            display_name: 'STALE_A_REVOKE_LIST',
          }],
        }))
        return
      }
      await route.fulfill(json({ connections: [{
        ...socialConnection(),
        id: 'connection-workspace-a',
        workspace_id: 'workspace-a',
        display_name: 'A Owner Channel',
      }] }))
    },
  )

  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await page.getByRole('button', { name: 'Disconnect' }).click()
  await staleListRequested.promise
  await page.getByLabel('Current workspace').selectOption('workspace-b')
  releaseStaleList.resolve()

  await expect(page.getByText('B Member Channel')).toBeVisible()
  await expect(page.getByText('STALE_A_REVOKE_LIST')).toHaveCount(0)
  await expect(page.getByText(
    'Channel disconnected. Its stored credentials were deleted.',
  )).toHaveCount(0)
})

test('B to A transition discards delayed B reads', async ({ page }) => {
  await routeWorkspaceSwitchPage(page)
  await page.goto(`${offBaseURL}/en/app/social-channels`)
  const workspace = page.getByLabel('Current workspace')

  await workspace.selectOption('workspace-b')
  await expect(workspace).toBeEnabled()
  await workspace.selectOption('workspace-a')

  await expect(page.getByText('A Owner Channel')).toBeVisible()
  await page.waitForTimeout(400)
  await expect(page.getByText('B Member Channel')).toHaveCount(0)
})

test('a rejected workspace selection keeps and refetches the effective old workspace', async ({
  page,
}) => {
  const fixture = await routeWorkspaceSwitchPage(page, { failNextSwitch: true })
  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await expect(page.getByText('A Owner Channel')).toBeVisible()

  const workspace = page.getByLabel('Current workspace')
  await workspace.selectOption('workspace-b')

  await expect(workspace).toHaveValue('workspace-a')
  await expect(page.getByText(
    'We could not switch workspaces. Your current workspace was not changed.',
  )).toBeVisible()
  expect(fixture.activeWorkspace()).toBe('workspace-a')
  expect(fixture.rollbackAttempts()).toBe(0)
  await expect(page.getByText('A Owner Channel')).toBeVisible()
  await expect.poll(() => fixture.listRequests['workspace-a']).toBeGreaterThanOrEqual(2)
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
})

test('a post-commit session failure explicitly rolls back and verifies workspace A', async ({
  page,
}) => {
  const fixture = await routeWorkspaceSwitchPage(page, {
    failFirstSessionRefresh: true,
  })
  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await expect(page.getByText('A Owner Channel')).toBeVisible()

  const workspace = page.getByLabel('Current workspace')
  await workspace.selectOption('workspace-b')

  await expect(workspace).toHaveValue('workspace-a')
  await expect(page.getByText(
    'The new workspace could not be verified. Your previous workspace was restored and verified.',
  )).toBeVisible()
  expect(fixture.rollbackAttempts()).toBe(1)
  expect(fixture.activeWorkspace()).toBe('workspace-a')
  await expect(page.getByText('A Owner Channel')).toBeVisible()
  await expect.poll(() => fixture.listRequests['workspace-a']).toBeGreaterThanOrEqual(2)
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
})

test('a lost switch response reconciles server workspace B and verifies rollback A', async ({
  page,
}) => {
  const fixture = await routeWorkspaceSwitchPage(page, {
    loseNextSwitchResponse: true,
  })
  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await expect(page.getByText('A Owner Channel')).toBeVisible()

  await page.getByLabel('Current workspace').selectOption('workspace-b')

  await expect(page.getByLabel('Current workspace')).toHaveValue('workspace-a')
  await expect(page.getByText(
    'The new workspace could not be verified. Your previous workspace was restored and verified.',
  )).toBeVisible()
  expect(fixture.rollbackAttempts()).toBe(1)
  expect(fixture.activeWorkspace()).toBe('workspace-a')
  await expect(page.getByText('A Owner Channel')).toBeVisible()
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
})

test('a lost switch response with unavailable reconciliation fails closed until authoritative retry', async ({
  page,
}) => {
  const fixture = await routeWorkspaceSwitchPage(page, {
    failLostResponseRecoverySession: true,
    loseNextSwitchResponse: true,
  })
  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await expect(page.getByText('A Owner Channel')).toBeVisible()

  await page.getByLabel('Current workspace').selectOption('workspace-b')

  await expect(page.getByRole('heading', {
    name: 'This service is temporarily unavailable',
  })).toBeVisible()
  expect(fixture.rollbackAttempts()).toBe(0)
  expect(fixture.activeWorkspace()).toBe('workspace-b')
  await expect(page.getByLabel('Current workspace')).toHaveValue('')
  await expect(page.getByText('A Owner Channel')).toHaveCount(0)
  await expect(page.getByText('B Member Channel')).toHaveCount(0)
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)

  await page.getByRole('button', { name: 'Retry' }).click()

  await expect(page.getByLabel('Current workspace')).toHaveValue('workspace-b')
  await expect(page.getByText('B Member Channel')).toBeVisible()
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
})

test('a failed rollback removes stale authority and later retry adopts server workspace B', async ({
  page,
}) => {
  const fixture = await routeWorkspaceSwitchPage(page, {
    failFirstSessionRefresh: true,
    failRollback: true,
  })
  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await expect(page.getByText('A Owner Channel')).toBeVisible()

  await page.getByLabel('Current workspace').selectOption('workspace-b')

  await expect(page.getByRole('heading', {
    name: 'This service is temporarily unavailable',
  })).toBeVisible()
  expect(fixture.rollbackAttempts()).toBe(1)
  expect(fixture.activeWorkspace()).toBe('workspace-b')
  await expect(page.getByText('A Owner Channel')).toHaveCount(0)
  await expect(page.getByText('B Member Channel')).toHaveCount(0)
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
  await expect(page.getByLabel('Current workspace')).toHaveValue('')

  await page.getByRole('button', { name: 'Retry' }).click()

  await expect(page.getByLabel('Current workspace')).toHaveValue('workspace-b')
  await expect(page.getByText('B Member Channel')).toBeVisible()
  await expect(page.getByText(
    'Only an Owner on an active workspace can connect, reconnect, select, or revoke channels.',
  )).toBeVisible()
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
})

test('an authoritative Social workspace mismatch terminates in retryable unavailable state', async ({
  page,
}) => {
  await routeWorkspaceSwitchPage(page)
  await page.route('**/api/v1/app/workspaces/current', route =>
    route.fulfill(json({
      ...currentWorkspace('member'),
      id: 'workspace-b',
      name: 'Workspace B',
    })))

  await page.goto(`${offBaseURL}/en/app/social-channels`)

  await expect(page.getByRole('heading', {
    name: 'This service is temporarily unavailable',
  })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
  await expect(page.getByText('A Owner Channel')).toHaveCount(0)
})

test('an Owner to Member permission change during current reads fails closed without infinite loading', async ({
  page,
}) => {
  await routeSocialPage(page, 'owner')
  const workspaceRequested = deferred()
  const releaseWorkspace = deferred()
  await page.route('**/api/v1/app/workspaces/current', async (route) => {
    workspaceRequested.resolve()
    await releaseWorkspace.promise
    await route.fulfill(json(currentWorkspace('owner')))
  })

  await page.goto(`${offBaseURL}/en/app/social-channels`)
  await workspaceRequested.promise
  await page.evaluate(() => {
    const root = document.querySelector('#__nuxt') as HTMLElement & {
      __vue_app__?: {
        $nuxt?: { payload: { state: Record<string, unknown> } }
      }
    }
    const payloadState = root.__vue_app__?.$nuxt?.payload.state
    const key = '$spostqron.app-shell.session'
    const current = payloadState?.[key] as ReturnType<typeof session> | undefined
    if (!payloadState || !current) {
      throw new Error('Nuxt session state is unavailable')
    }
    payloadState[key] = {
      ...current,
      current_workspace: current.current_workspace
        ? { ...current.current_workspace, role: 'member' }
        : undefined,
      workspaces: current.workspaces.map(workspace => ({
        ...workspace,
        role: workspace.id === current.current_workspace?.id ? 'member' : workspace.role,
      })),
    }
  })
  releaseWorkspace.resolve()

  await expect(page.getByRole('heading', {
    name: 'This service is temporarily unavailable',
  })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
  await expect(page.locator('.app-provider-catalog')).toHaveCount(0)
})

test('Social Retry refreshes authoritative session before recovering Owner to Member', async ({
  page,
}) => {
  const fixture = await routeSocialPage(page, 'owner')
  fixture.setWorkspaceRole('member')
  await page.goto(`${offBaseURL}/en/app/social-channels`)

  await expect(page.getByRole('heading', {
    name: 'This service is temporarily unavailable',
  })).toBeVisible()
  const requestsBeforeRetry = fixture.sessionRequests()
  fixture.setSessionRole('member')
  await page.getByRole('button', { name: 'Retry' }).click()

  await expect.poll(fixture.sessionRequests).toBeGreaterThan(requestsBeforeRetry)
  await expect(page.getByText(
    'Only an Owner on an active workspace can connect, reconnect, select, or revoke channels.',
  )).toBeVisible()
  await expect(page.locator('.app-provider-catalog').getByRole('button', { name: 'Connect' }).first()).toBeDisabled()
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
  await expect(page.getByRole('heading', {
    name: 'This service is temporarily unavailable',
  })).toHaveCount(0)
})

test('an account configuration error without status is unavailable rather than offline', async ({
  page,
}) => {
  await routeSocialPage(page, 'owner')
  await page.route('**/api/v1/app/workspaces/current', route =>
    route.fulfill(json({ invalid: 'current workspace' })))

  await page.goto(`${offBaseURL}/en/app/social-channels`)

  await expect(page.getByRole('heading', {
    name: 'This service is temporarily unavailable',
  })).toBeVisible()
  await expect(page.getByRole('heading', {
    name: 'You appear to be offline',
  })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()
  await expect(page.getByText('Loading your workspace')).toHaveCount(0)
})

test('separate APP and API domains complete OAuth through the fixed same-origin relay', async ({ page }) => {
  const social = await routeSocialPage(page, 'owner')
  await page.context().route(/\/en\/app\/(?:social-channels|social-oauth\/callback)(?:\?|$)/u, async route => {
    const response = await route.fetch()
    const original = await response.text()
    const body = original.replace(
      /(\bapiBase\s*:\s*")http:\/\/127\.0\.0\.1:41795/u,
      '$1http://127.0.0.1:41796',
    )
    expect(body).not.toBe(original)
    const headers = response.headers()
    // The fixture rewrites Nuxt's hashed inline runtime-config script. Remove
    // only this document's CSP so Chromium executes that test-only rewrite;
    // production CSP coverage remains in apps/web/test.
    delete headers['content-security-policy']
    await route.fulfill({ response, body, headers })
  })

  await page.goto(`${offBaseURL}/en/app/social-channels`)
  const provider = page.locator('.app-provider-catalog li').filter({ hasText: 'Bluesky' })
  await provider.getByLabel('Discovery type').selectOption('did')
  await provider.getByLabel('Discovery value').fill('did:plc:alice')
  const popupPromise = page.waitForEvent('popup')
  await provider.getByRole('button', { name: 'Connect' }).click()
  const popup = await popupPromise

  await popup.waitForURL(`${offBaseURL}/en/app/social-oauth/callback`)
  await expect(page.getByRole('heading', { name: 'Choose what to connect' })).toBeVisible()
  expect(social.beginBody()).toBeDefined()
  expect(social.callbackRequests).toHaveLength(1)
  expect(social.callbackRequests[0]?.origin).toBe('http://127.0.0.1:41796')
})

test('composer uploads media, enables thread controls from capabilities, and localizes validation errors', async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 880 })
  const composer = await routeComposerPage(page)

  await page.goto(`${offBaseURL}/en/app/publish`)
  await expect(page.getByRole('heading', { name: 'Create a post' })).toBeVisible()

  await page.getByRole('checkbox', { name: /Launch Thread/u }).check()
  await expect(page.getByRole('heading', { name: 'Thread items' })).toBeVisible()

  await page.locator('input[type="file"]').setInputFiles({
    name: 'asset.png',
    mimeType: 'image/png',
    buffer: Buffer.from('fixture-image'),
  })
  await expect(page.getByText('Media uploaded and checked.')).toBeVisible()
  await expect(page.getByText('image/png')).toBeVisible()

  await page.getByRole('button', { name: 'Add thread item' }).click()
  await page.getByRole('checkbox', { name: /image\/png/u }).check()
  await page.getByRole('button', { name: 'Publish now' }).click()

  const validation = page.locator('[role="alert"] .composer-validation').first()
  await expect(validation).toContainText('Thread')
  await expect(validation).toContainText('Text is required for this destination.')
  await expect(validation).not.toContainText('RAW_BACKEND_THREAD_MESSAGE')
  expect(composer.scheduleRequests()).toBe(0)
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth <= globalThis.innerWidth)).toBe(true)
  const results = await new AxeBuilder({ page }).include('main').analyze()
  expect(results.violations).toEqual([])
})

test('publish-now real clicks reuse one Idempotency-Key after an ambiguous response', async ({
  page,
}) => {
  const composer = await routeComposerPage(page, {
    ambiguousFirstSchedule: true,
    validationValid: true,
  })

  await page.goto(`${offBaseURL}/en/app/publish`)
  await page.getByRole('checkbox', { name: /Launch Thread/u }).check()
  await page.getByRole('button', { name: 'Add thread item' }).click()
  await page.getByLabel('Item text').fill('Idempotent launch')
  await page.getByRole('button', { name: 'Publish now' }).click()

  await expect(page.getByText(
    'Postqron is unreachable. Check your connection and retry; your local edits remain on screen.',
  )).toBeVisible()
  expect(composer.scheduleRequests()).toBe(1)
  expect(composer.scheduleKeys[0]).toMatch(/^[!-~]{1,200}$/u)

  await page.getByRole('button', { name: 'Publish now' }).click()
  await expect(page).toHaveURL(`${offBaseURL}/en/app/calendar`)
  expect(composer.scheduleRequests()).toBe(2)
  expect(composer.scheduleKeys[1]).toBe(composer.scheduleKeys[0])
  expect(composer.scheduleBodies[1]).toEqual(composer.scheduleBodies[0])
  expect(composer.scheduledOperations.size).toBe(1)

  const mismatchStatus = await page.evaluate(async ({ key, original }) => {
    const body = original as {
      scheduled_at: { local_date_time: string }
    }
    const response = await fetch(
      '/api/v1/workspaces/workspace-fixture/scheduled-posts',
      {
        method: 'POST',
        credentials: 'include',
        headers: {
          'content-type': 'application/json',
          'Idempotency-Key': key,
        },
        body: JSON.stringify({
          ...body,
          scheduled_at: {
            ...body.scheduled_at,
            local_date_time: '2030-01-01T10:00',
          },
        }),
      },
    )
    return response.status
  }, {
    key: composer.scheduleKeys[0] ?? '',
    original: composer.scheduleBodies[0],
  })
  expect(mismatchStatus).toBe(409)
  expect(composer.scheduledOperations.size).toBe(1)
})

test('calendar refetches on timezone changes, keeps local DST boundaries visible, and locks non-scheduled posts', async ({
  page,
}) => {
  const calendar = await routeCalendarPage(page)

  await page.goto(`${offBaseURL}/en/app/calendar`)
  for (let index = 0; index < 4; index += 1) {
    await page.getByRole('button', { name: 'Previous month' }).click()
  }

  await expect(page.getByRole('heading', { name: 'March 2026' })).toBeVisible()
  expect(calendar.calendarRequests.some(query =>
    query.includes('from=2026-02-27T00%3A00%3A00.000Z')
    && query.includes('until=2026-04-03T00%3A00%3A00.000Z'))).toBe(true)

  await page.getByRole('button', { name: 'List view' }).click()
  await expect(page.getByText('March launch')).toBeVisible()
  await expect(page.getByText('Published launch')).toBeVisible()
  const publishedCard = page.locator('li.app-card').filter({ hasText: 'Published launch' })
  await expect(publishedCard).toContainText(
    'This post is now read-only in F30. Only scheduled posts can be edited, duplicated, rescheduled, or cancelled.',
  )

  const beforeRefetch = calendar.calendarRequests.length
  await page.getByLabel('Display timezone').selectOption('Europe/Rome')
  await expect.poll(() => calendar.calendarRequests.length).toBeGreaterThan(beforeRefetch)

  const scheduledCard = page.locator('li.app-card').filter({ hasText: 'March launch' })
  await scheduledCard.getByRole('button', { name: 'Reschedule' }).click()
  await expect(page.getByRole('heading', { name: 'Reschedule post' })).toBeVisible()
  await expect(page.locator('article.calendar-reschedule select').first()).toHaveValue('Europe/Rome')
  await page.getByLabel('Local date and time').fill('2026-07-10T10:00')
  await page.getByRole('button', { name: 'Confirm new time' }).click()
  expect(calendar.rescheduleBodies[0]).toEqual({
    expected_revision: 2,
    scheduled_at: {
      local_date_time: '2026-07-10T10:00',
      time_zone: 'Europe/Rome',
      utc_offset_minutes: 120,
    },
  })

  await scheduledCard.getByRole('button', { name: 'Reschedule' }).click()
  await page.getByLabel('Local date and time').fill('2026-07-10T10:00')
  await page.locator('article.calendar-reschedule select').first().selectOption('America/New_York')
  await page.getByRole('button', { name: 'Confirm new time' }).click()
  expect(calendar.rescheduleBodies[1]).toEqual({
    expected_revision: 2,
    scheduled_at: {
      local_date_time: '2026-07-10T10:00',
      time_zone: 'America/New_York',
      utc_offset_minutes: -240,
    },
  })

  const results = await new AxeBuilder({ page }).include('main').analyze()
  expect(results.violations).toEqual([])
})

test('duplicate real clicks replay one F7 operation after an ambiguous response', async ({
  page,
}) => {
  const calendar = await routeCalendarPage(page, { ambiguousFirstDuplicate: true })
  await page.goto(`${offBaseURL}/en/app/calendar`)
  for (let index = 0; index < 4; index += 1) {
    await page.getByRole('button', { name: 'Previous month' }).click()
  }
  await page.getByRole('button', { name: 'List view' }).click()
  const scheduledCard = page.locator('li.app-card').filter({ hasText: 'March launch' })

  await scheduledCard.getByRole('button', { name: 'Duplicate' }).click()
  await expect(page.getByText(
    'Postqron is unreachable. Check your connection and retry; your local edits remain on screen.',
  )).toBeVisible()
  await scheduledCard.getByRole('button', { name: 'Duplicate' }).click()

  await expect(page.getByText('Post duplicated.')).toBeVisible()
  expect(calendar.duplicateKeys).toHaveLength(2)
  expect(calendar.duplicateKeys[0]).toMatch(/^[!-~]{1,200}$/u)
  expect(calendar.duplicateKeys[1]).toBe(calendar.duplicateKeys[0])
  expect(calendar.duplicateBodies[1]).toEqual(calendar.duplicateBodies[0])
  expect(calendar.duplicateOperations.size).toBe(1)
})
