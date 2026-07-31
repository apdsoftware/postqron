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
  let connections = role === 'owner' ? [] as Array<ReturnType<typeof socialConnection>> : [socialConnection('reconnect_required')]
  let beginBody: unknown

  await page.context().route('**/*', async route => {
    const request = route.request()
    const url = new URL(request.url())

    if (url.pathname === '/api/v1/app/session') {
      await route.fulfill(json(session(role)))
      return
    }
    if (url.pathname === '/api/v1/app/workspaces/current') {
      await route.fulfill(json(currentWorkspace(role)))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/social-connections/bootstrap') {
      await route.fulfill(json(socialBootstrap()))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/social-connections'
      && request.method() === 'GET') {
      await route.fulfill(json({ connections }))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/social-authorizations'
      && request.method() === 'POST') {
      beginBody = request.postDataJSON()
      await route.fulfill(json({
        authorization_url: 'https://social-provider.example.test/oauth/start',
        expires_at: '2026-07-31T12:10:00.000Z',
      }))
      return
    }
    if (url.origin === 'https://social-provider.example.test'
      && url.pathname === '/oauth/start') {
      await route.fulfill({
        status: 200,
        contentType: 'text/html',
        body: `<script>location.replace(${JSON.stringify(`${offBaseURL}/api/v1/social-authorizations/callback`)})</script>`,
      })
      return
    }
    if (url.pathname === '/api/v1/social-authorizations/callback') {
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
      await route.fulfill(request.isNavigationRequest()
        ? {
            status: 200,
            contentType: 'text/plain',
            body: JSON.stringify(payload),
          }
        : json(payload))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/social-connections'
      && request.method() === 'POST') {
      connections = [socialConnection()]
      await route.fulfill(json(connections[0]))
      return
    }

    await route.continue()
  })

  return {
    beginBody: () => beginBody,
  }
}

async function routeWorkspaceSwitchPage(page: Page) {
  const roles = {
    'workspace-a': 'owner',
    'workspace-b': 'member',
  } as const
  let activeWorkspace: keyof typeof roles = 'workspace-a'
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

  await page.route('**/*', async route => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.pathname === '/api/v1/auth/csrf') {
      await route.fulfill(json({ csrf_token: 'workspace-switch-csrf' }))
      return
    }
    if (url.pathname === '/api/v1/app/workspaces/select' && request.method() === 'POST') {
      activeWorkspace = (request.postDataJSON() as { workspace_id: typeof activeWorkspace }).workspace_id
      await route.fulfill(json({ ok: true }))
      return
    }
    if (url.pathname === '/api/v1/app/session') {
      await route.fulfill(json(activeSession()))
      return
    }
    if (url.pathname === '/api/v1/app/workspaces/current') {
      if (activeWorkspace === 'workspace-b') {
        await new Promise(resolve => globalThis.setTimeout(resolve, 250))
      }
      await route.fulfill(json({
        ...currentWorkspace(roles[activeWorkspace]),
        id: activeWorkspace,
        name: names[activeWorkspace],
      }))
      return
    }
    if (url.pathname === `/api/v1/workspaces/${activeWorkspace}/social-connections/bootstrap`) {
      if (activeWorkspace === 'workspace-b') {
        await new Promise(resolve => globalThis.setTimeout(resolve, 250))
      }
      await route.fulfill(json(socialBootstrap()))
      return
    }
    if (url.pathname === `/api/v1/workspaces/${activeWorkspace}/social-connections`
      && request.method() === 'GET') {
      if (activeWorkspace === 'workspace-b') {
        await new Promise(resolve => globalThis.setTimeout(resolve, 250))
      }
      await route.fulfill(json({
        connections: [{
          ...socialConnection(),
          id: `connection-${activeWorkspace}`,
          display_name: activeWorkspace === 'workspace-a'
            ? 'A Owner Channel'
            : 'B Member Channel',
        }],
      }))
      return
    }
    await route.continue()
  })
}

async function routeComposerPage(page: Page) {
  let draftRevision = 0
  let scheduleRequests = 0
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
      && request.method() === 'PATCH') {
      draftRevision += 1
      draftContent = request.postDataJSON().content
      await route.fulfill(json(draftView()))
      return
    }
    if (url.pathname === '/api/v1/workspaces/workspace-fixture/drafts/draft-1/validate') {
      await route.fulfill(json({
        validation: {
          capability_version: 'fixture-v1',
          valid: false,
          errors: [{
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
      await route.fulfill(json({
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
      }))
      return
    }

    await route.continue()
  })

  return {
    scheduleRequests: () => scheduleRequests,
  }
}

async function routeCalendarPage(page: Page) {
  const calendarRequests: string[] = []
  const rescheduleBodies: unknown[] = []

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

    await route.continue()
  })

  return {
    calendarRequests,
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
  await popup.waitForURL(`${offBaseURL}/api/v1/social-authorizations/callback`)
  await expect(popup.locator('body')).toContainText('selection_id')

  await expect(page.getByRole('heading', { name: 'Choose what to connect' })).toBeVisible()
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

test('cross-origin F5 callbacks fail closed before OAuth starts', async ({ page }) => {
  const social = await routeSocialPage(page, 'owner')
  await page.route('**/en/app/social-channels', async route => {
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

  let popupCount = 0
  page.on('popup', () => { popupCount += 1 })
  await page.goto(`${offBaseURL}/en/app/social-channels`)
  const provider = page.locator('.app-provider-catalog li').filter({ hasText: 'Bluesky' })
  await provider.getByLabel('Discovery type').selectOption('did')
  await provider.getByLabel('Discovery value').fill('did:plc:alice')
  await provider.getByRole('button', { name: 'Connect' }).click()

  await expect(page.getByText(
    'Social OAuth requires the API callback to be exposed through the same origin as the app. No authorization was started.',
  )).toBeVisible()
  expect(popupCount).toBe(0)
  expect(social.beginBody()).toBeUndefined()
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
