import AxeBuilder from '@axe-core/playwright'
import { createHmac } from 'node:crypto'
import { expect, test } from '@playwright/test'
import {
  covers,
  fixtureBaseURL,
  fixtureHealth,
  fixtureReset,
  locales,
  localized,
  offBaseURL,
  session,
} from '../helpers.ts'

test.beforeEach(async () => {
  await fixtureReset()
})

test('/app supports anonymous and authenticated paths without 404', async ({
  browser,
  page,
}, testInfo) => {
  covers(testInfo, 'LR-APP', 'LR-NEGATIVE')

  const anonymous = await page.goto(`${offBaseURL}/app`)
  expect(anonymous?.status()).toBe(200)
  await expect(page.locator('.auth-intro h1')).toBeVisible()

  const guarded = await page.goto(`${offBaseURL}/app/home`)
  expect(guarded?.status()).toBe(200)
  await expect(page.getByRole('main')).toBeVisible()

  const authenticated = await browser.newContext()
  await session(authenticated, 'authenticated')
  const authenticatedPage = await authenticated.newPage()
  const response = await authenticatedPage.goto(`${offBaseURL}/app/home`)
  expect(response?.status()).toBe(200)
  await expect(authenticatedPage).toHaveURL(/\/app\/home$/u)
  await expect(authenticatedPage.getByRole('main')).toBeVisible()
  await authenticated.close()
})

test('normal admin access is 403 while allowlisted mutation is audited', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN', 'LR-NEGATIVE')

  const normal = await browser.newContext()
  await session(normal, 'normal')
  const normalPage = await normal.newPage()
  const forbiddenSession = normalPage.waitForResponse(response =>
    response.url().endsWith('/api/v1/admin/session')
    && response.status() === 403)
  const forbiddenDocument = await normalPage.goto(`${offBaseURL}/admin`)
  expect(forbiddenDocument?.status()).toBe(200)
  expect((await forbiddenSession).status()).toBe(403)
  await expect(normalPage.locator('body')).toContainText('ADMIN_FORBIDDEN')
  await normal.close()

  const admin = await browser.newContext()
  await session(admin, 'admin')
  const adminPage = await admin.newPage()
  const allowedSession = adminPage.waitForResponse(response =>
    response.url().endsWith('/api/v1/admin/session')
    && response.status() === 200)
  const allowed = await adminPage.goto(`${offBaseURL}/admin`)
  expect(allowed?.status()).toBe(200)
  expect((await allowedSession).status()).toBe(200)
  await expect(adminPage.getByRole('heading', { level: 1 })).toBeVisible()

  await adminPage.getByRole('link', { name: /^plans$/iu }).click()
  await expect(adminPage).toHaveURL(/\/admin\/plans$/u)
  await adminPage.getByRole('button', { name: /assign/iu }).click()
  await adminPage.getByLabel(/reason/iu).fill('Approved launch fixture action')
  await adminPage.getByRole('checkbox').check()
  await adminPage.getByRole('button', { name: /confirm operation/iu }).click()
  await expect(adminPage.getByText(/accepted and audited/iu)).toBeVisible()
  await adminPage.getByRole('button', { name: /^cancel$/iu }).click()

  await adminPage.getByRole('link', { name: /^audit$/iu }).click()
  await expect(adminPage).toHaveURL(/\/admin\/audit$/u)
  await expect(adminPage.getByText('internal_plan.assign')).toBeVisible()
  await expect(adminPage.getByText('Approved launch fixture action')).toBeVisible()
  await admin.close()
})

test('admin signs in with email and password without an OAuth provider', async ({
  page,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN', 'LR-NEGATIVE')

  const document = await page.goto(`${offBaseURL}/admin`)
  expect(document?.status()).toBe(200)
  await expect(page.getByRole('heading', {
    level: 2,
    name: /administrator sign-in/iu,
  })).toBeVisible()

  await page.getByLabel(/email address/iu).fill('admin@example.test')
  await page.getByLabel(/^password$/iu).fill('incorrect-password')
  await page.getByRole('button', { name: /^sign in$/iu }).click()
  await expect(page.getByRole('alert')).toContainText(/invalid/iu)

  await page.getByLabel(/^password$/iu).fill('fixture-admin-password')
  await page.getByRole('button', { name: /^sign in$/iu }).click()
  await expect(page.getByRole('heading', { level: 2, name: /service health/iu }))
    .toBeVisible()
})

test('dashboard is the admin landing page and surfaces a danger alert only when a service is not operational', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN')

  const healthy = await browser.newContext()
  await session(healthy, 'admin')
  const healthyPage = await healthy.newPage()
  await healthyPage.goto(`${offBaseURL}/admin`)
  await expect(healthyPage).toHaveURL(/\/admin$/u)
  await expect(healthyPage.getByRole('alert')).toHaveCount(0)
  await expect(healthyPage.getByText(/^healthy$/iu)).toBeVisible()
  await healthy.close()

  for (const status of ['degraded', 'outage', 'unknown'] as const) {
    await fixtureHealth(status)
    const context = await browser.newContext()
    await session(context, 'admin')
    const page = await context.newPage()
    await page.goto(`${offBaseURL}/admin`)
    const alert = page.getByRole('alert')
    await expect(alert).toBeVisible()
    await expect(alert).toContainText(/database|worker_queue/u)
    await context.close()
  }
  await fixtureHealth('operational')
})

test('a failed health request never renders as fully operational', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN', 'LR-NEGATIVE')

  await fixtureHealth('api_failure')
  const context = await browser.newContext()
  await session(context, 'admin')
  const page = await context.newPage()
  await page.goto(`${offBaseURL}/admin`)
  await expect(page.getByRole('alert')).toContainText(/unavailable/iu)
  await expect(page.getByText(/^healthy$/iu)).toHaveCount(0)
  await context.close()
  await fixtureHealth('operational')
})

test('admin sidebar deep-links every section, marks the active route, and collapses on mobile', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN')

  const context = await browser.newContext()
  await session(context, 'admin')
  const page = await context.newPage()

  const sections: Array<[string, RegExp]> = [
    ['Users', /\/admin\/users$/u],
    ['Workspaces', /\/admin\/workspaces$/u],
    ['Plans', /\/admin\/plans$/u],
    ['Audit', /\/admin\/audit$/u],
    ['Profile', /\/admin\/profile$/u],
    ['Dashboard', /\/admin$/u],
  ]

  const initial = await page.goto(`${offBaseURL}/admin`)
  expect(initial?.status()).toBe(200)

  for (const [name, url] of sections) {
    await page.getByRole('link', { name: new RegExp(`^${name}$`, 'iu') }).click()
    await expect(page).toHaveURL(url)
    await expect(page.getByRole('link', { name: new RegExp(`^${name}$`, 'iu') }))
      .toHaveAttribute('aria-current', 'page')
  }

  await page.setViewportSize({ width: 375, height: 812 })
  await page.goto(`${offBaseURL}/admin`)
  const sidebar = page.locator('.admin-sidebar')
  const drawerToggle = page.getByRole('button', {
    name: /^open navigation menu$/iu,
  })
  await expect(drawerToggle).toBeVisible()
  await expect(sidebar).not.toHaveAttribute('data-open', 'true')
  await drawerToggle.click()
  await expect(drawerToggle).toHaveAttribute('aria-expanded', 'true')
  await expect(sidebar).toHaveAttribute('data-open', 'true')
  await page.keyboard.press('Escape')
  await expect(sidebar).not.toHaveAttribute('data-open', 'true')
  await context.close()
})

test('Paddle sandbox checkout stays pending until signed webhook, then opens portal', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-PADDLE', 'LR-NEGATIVE')
  const context = await browser.newContext()
  await session(context, 'authenticated')
  const page = await context.newPage()
  await page.addInitScript(() => {
    let callback: ((event: { name: string }) => void) | undefined
    const fixturePaddle = {
      Environment: { set: (_environment: 'sandbox') => {} },
      Initialize: (options: {
        eventCallback(event: { name: string }): void
      }) => {
        callback = options.eventCallback
      },
      Update: (options: {
        eventCallback(event: { name: string }): void
      }) => {
        callback = options.eventCallback
      },
      Checkout: {
        open: (_options: unknown) => {
          setTimeout(() => callback?.({ name: 'checkout.completed' }), 20)
        },
      },
    }
    Object.defineProperty(globalThis, 'Paddle', {
      value: fixturePaddle,
      configurable: true,
    })
  })

  await page.goto(
    `${offBaseURL}/app/billing/checkout?plan=pro&interval=monthly&quantity=10`,
  )
  await expect(page.getByText(/processing|elaborazione|procesando|traitement|verarbeitet/iu))
    .toBeVisible()

  const timestamp = 1_753_444_800
  const event = JSON.stringify({
    event_id: 'evt_fixture',
    event_type: 'transaction.completed',
    data: { plan: 'pro' },
  })
  const signature = createHmac(
    'sha256',
    'postqron-launch-fixture-signing-key-v1',
  ).update(`${timestamp}:${event}`).digest('hex')

  const rejected = await fetch(
    `${fixtureBaseURL}/api/v1/billing/paddle/webhook`,
    {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'paddle-signature': `ts=${timestamp};h1=${'0'.repeat(64)}`,
      },
      body: event,
    },
  )
  expect(rejected.status).toBe(400)

  const accepted = await fetch(
    `${fixtureBaseURL}/api/v1/billing/paddle/webhook`,
    {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'paddle-signature': `ts=${timestamp};h1=${signature}`,
      },
      body: event,
    },
  )
  expect(accepted.status).toBe(200)
  await expect(page.getByText(/confirmed|confermato|confirmado|confirmé|bestätigt/iu))
    .toBeVisible({ timeout: 35_000 })

  await page.route('https://customer-portal.paddle.com/**', route =>
    route.fulfill({
      status: 200,
      contentType: 'text/html',
      body: '<title>Fixture customer portal</title>',
    }))
  await page.goto(`${offBaseURL}/app/billing`)
  await page.getByRole('button', { name: /portal/iu }).click()
  await expect(page).toHaveURL(/customer-portal\.paddle\.com\/fixture/u)
  await context.close()
})

test('five-locale app and admin routes render without missing keys', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-LOCALE-MATRIX', 'LR-I18N')

  for (const locale of locales) {
    const appContext = await browser.newContext()
    await session(appContext, 'authenticated')
    const appPage = await appContext.newPage()
    const appResponse = await appPage.goto(
      `${offBaseURL}${localized(locale, '/app/home')}`,
    )
    expect(appResponse?.status(), `${locale} app`).toBe(200)
    await expect(appPage.locator('html')).toHaveAttribute('lang', locale)
    await expect(appPage.locator('body')).not.toContainText(/MISSING|I18N_/u)
    await appContext.close()

    const adminContext = await browser.newContext()
    await session(adminContext, 'admin')
    const adminPage = await adminContext.newPage()
    const adminResponse = await adminPage.goto(
      `${offBaseURL}${localized(locale, '/admin')}`,
    )
    expect(adminResponse?.status(), `${locale} admin`).toBe(200)
    await expect(adminPage.locator('html')).toHaveAttribute('lang', locale)
    await expect(adminPage.locator('body')).not.toContainText(/MISSING|I18N_/u)
    await adminContext.close()
  }
})

test('authenticated app and admin pass serious and critical WCAG checks', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-WCAG')

  for (const [role, path] of [
    ['authenticated', '/app/home'],
    ['admin', '/admin'],
  ] as const) {
    const context = await browser.newContext()
    await session(context, role)
    const page = await context.newPage()
    await page.goto(`${offBaseURL}${path}`)
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze()
    const blocking = results.violations.filter(violation =>
      violation.impact === 'serious' || violation.impact === 'critical')
    expect(blocking, path).toEqual([])
    await context.close()
  }
})
