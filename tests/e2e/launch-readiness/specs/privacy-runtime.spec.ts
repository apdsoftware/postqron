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
