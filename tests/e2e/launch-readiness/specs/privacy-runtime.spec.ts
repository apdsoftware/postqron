import { expect, test } from '@playwright/test'
import { covers, fixtureReset, offBaseURL, session } from '../helpers.ts'

test.beforeEach(async () => {
  await fixtureReset()
})

test('privacy export is private, one-time and expires', async ({ browser }, testInfo) => {
  covers(testInfo, 'LR-PRIVACY-EXPORT', 'LR-SECURITY')
  const context = await browser.newContext()
  await session(context, 'authenticated')
  const request = context.request

  const created = await request.post(`${offBaseURL}/api/v1/account/exports`, {
    data: { scope: 'account', confirmation: 'EXPORT' },
  })
  expect(created.status()).toBe(202)
  const exportRequest = await created.json()
  expect(exportRequest.status).toBe('ready')
  expect(JSON.stringify(exportRequest)).not.toMatch(/token|secret|session/iu)

  const signed = await request.get(
    `${offBaseURL}/api/v1/account/exports/${exportRequest.id}/download`,
  )
  expect(signed.status()).toBe(200)
  const download = await signed.json()
  expect(download.sha256).toMatch(/^[a-f0-9]{64}$/u)

  const first = await request.get(download.url)
  expect(first.status()).toBe(200)
  expect(first.headers()['cache-control']).toBe('private, no-store')
  const replay = await request.get(download.url)
  expect(replay.status()).toBe(404)

  const expired = await request.get(
    `${offBaseURL}/api/v1/account/exports/${exportRequest.id}/download?fixture_expired=1`,
  )
  expect(expired.status()).toBe(410)
  await context.close()
})

test('privacy deletion supports grace cancellation and finalization', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-PRIVACY-DELETION', 'LR-SECURITY')
  const context = await browser.newContext()
  await session(context, 'authenticated')
  const request = context.request
  const payload = {
    scope: 'workspace',
    workspace_id: 'workspace-fixture',
    ownership_actions: [{
      workspace_id: 'workspace-fixture',
      action: 'delete',
    }],
    confirmation: 'DELETE',
  }

  const created = await request.post(`${offBaseURL}/api/v1/account/deletions`, {
    data: payload,
  })
  expect(created.status()).toBe(202)
  const deletion = await created.json()
  expect(deletion.status).toBe('grace_period')

  const cancelled = await request.delete(
    `${offBaseURL}/api/v1/account/deletions/${deletion.id}`,
  )
  expect(cancelled.status()).toBe(204)

  const finalized = await request.post(
    `${offBaseURL}/api/v1/account/deletions?fixture_finalize=1`,
    { data: payload },
  )
  expect(finalized.status()).toBe(202)
  expect((await finalized.json()).status).toBe('completed')
  await context.close()
})

test('frozen account cancels through a pre-authorized one-time capability', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-PRIVACY-DELETION', 'LR-SECURITY')
  const context = await browser.newContext()
  await session(context, 'authenticated')
  const request = context.request

  const issued = await request.post(
    `${offBaseURL}/api/v1/account/deletion-cancel-capabilities`,
    { headers: { origin: offBaseURL } },
  )
  expect(issued.status()).toBe(201)
  expect(issued.headers()['cache-control']).toBe('no-store')
  const capability = await issued.json()
  expect(capability).toEqual({
    expires_at: '2026-08-23T12:00:00.000Z',
  })

  const created = await request.post(`${offBaseURL}/api/v1/account/deletions`, {
    data: {
      scope: 'account',
      ownership_actions: [{
        workspace_id: 'workspace-fixture',
        action: 'delete',
      }],
      confirmation: 'DELETE',
    },
  })
  expect(created.status()).toBe(202)
  const deletion = await created.json()

  const unreachableSessionCancel = await request.delete(
    `${offBaseURL}/api/v1/account/deletions/${deletion.id}`,
  )
  expect(unreachableSessionCancel.status()).toBe(401)

  const rejectedOrigin = await request.post(
    `${offBaseURL}/api/v1/account/deletions/${deletion.id}/cancel`,
    { headers: { origin: 'https://compromised.example.test' } },
  )
  expect(rejectedOrigin.status()).toBe(403)

  const attempts = await Promise.all([
    request.post(
      `${offBaseURL}/api/v1/account/deletions/${deletion.id}/cancel`,
      { headers: { origin: offBaseURL } },
    ),
    request.post(
      `${offBaseURL}/api/v1/account/deletions/${deletion.id}/cancel`,
      { headers: { origin: offBaseURL } },
    ),
  ])
  expect(attempts.map(attempt => attempt.status()).sort()).toEqual([204, 404])
  await context.close()
})
