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
  await adminPage.getByRole('button', { name: /assign/iu }).first().click()
  await adminPage.getByLabel(/reason/iu).fill('Approved launch fixture action')
  await adminPage.getByRole('checkbox').check()
  await adminPage.getByRole('button', { name: /confirm operation/iu }).click()
  await expect(adminPage.getByText(/accepted and audited/iu)).toBeVisible()
  await adminPage.getByRole('button', { name: /^cancel$/iu }).click()

  await adminPage.getByRole('link', { name: /^audit$/iu }).click()
  await expect(adminPage).toHaveURL(/\/admin\/audit$/u)
  const auditTable = adminPage.locator('.admin-audit-desktop')
  await expect(auditTable.getByText('internal_plan.assign')).toBeVisible()
  await expect(auditTable.getByText('Approved launch fixture action')).toBeVisible()
  await auditTable.getByRole('button', { name: /view details/iu }).click()
  await expect(adminPage.getByRole('heading', {
    name: /audit event details/iu,
  })).toBeVisible()
  const auditDialog = adminPage.getByRole('dialog')
  await expect(auditDialog.getByText('correlation-1')).toBeVisible()
  await auditDialog.getByRole('button', { name: /close details/iu }).click()
  const auditCSV = await adminPage.getByRole('link', {
    name: /export csv/iu,
  }).getAttribute('href')
  const exportResult = await adminPage.evaluate(async (href) => {
    const result = await fetch(String(href))
    return {
      status: result.status,
      disposition: result.headers.get('content-disposition'),
    }
  }, auditCSV)
  expect(exportResult.status).toBe(200)
  expect(exportResult.disposition).toContain('postqron-admin-audit.csv')
  await admin.close()
})

test('admin plan filters and pagination persist in query string and remain usable on mobile', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN')
  const context = await browser.newContext()
  await session(context, 'admin')
  const page = await context.newPage()

  await page.goto(`${offBaseURL}/admin/plans`)
  await expect(page.getByText(/page 1 of 2/iu)).toBeVisible()
  await page.getByRole('button', { name: /next page/iu }).click()
  await expect(page).toHaveURL(/\/admin\/plans\?.*page=2/u)
  await expect(page.getByText(/page 2 of 2/iu)).toBeVisible()

  await page.getByLabel(/workspace or owner/iu).fill('Studio')
  await page.locator('form.admin-data-filters select').first().selectOption('pro')
  await page.getByRole('button', { name: /apply filters/iu }).click()
  await expect(page).toHaveURL(/\/admin\/plans\?.*q=Studio/u)
  await expect(page).toHaveURL(/\/admin\/plans\?.*plan=pro/u)
  await expect(page.getByLabel(/workspace or owner/iu)).toHaveValue('Studio')
  await expect(page.locator('form.admin-data-filters select').first())
    .toHaveValue('pro')

  const plansCSV = await page.getByRole('link', {
    name: /export csv/iu,
  }).getAttribute('href')
  expect(plansCSV).toContain('q=Studio')
  expect(plansCSV).toContain('plan=pro')
  expect(plansCSV).not.toContain('page=')

  await page.setViewportSize({ width: 320, height: 700 })
  await expect(page.locator('.admin-mobile-list')).toBeVisible()
  const horizontalOverflow = await page.evaluate(() =>
    document.documentElement.scrollWidth > globalThis.innerWidth)
  expect(horizontalOverflow).toBe(false)
  await context.close()
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

test('admin account menu orders profile before logout on desktop and mobile and revokes the server session', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN', 'LR-NEGATIVE')

  const context = await browser.newContext()
  await session(context, 'admin')
  const page = await context.newPage()
  await page.goto(`${offBaseURL}/admin`)

  const accountMenu = page.locator('.admin-profile-menu')
  const accountMenuTrigger = accountMenu.locator('summary')
  await expect(page.getByRole('button', { name: /^sign out$/iu })).toBeHidden()
  await accountMenuTrigger.click()
  const profile = accountMenu.getByRole('link', { name: /^profile$/iu })
  const logout = page.getByRole('button', { name: /^sign out$/iu })
  await expect(profile).toBeVisible()
  await expect(logout).toBeVisible()
  const accountActions = accountMenu.locator(
    '.admin-profile-menu__panel > a, .admin-profile-menu__panel > .admin-logout',
  )
  await expect(accountActions).toHaveCount(2)
  await expect(accountActions.nth(0)).toContainText(/^profile$/iu)
  await expect(accountActions.nth(1)).toContainText(/^sign out$/iu)

  await accountMenuTrigger.press('Escape')
  await expect(accountMenu).not.toHaveAttribute('open', '')
  await expect(accountMenuTrigger).toBeFocused()

  await page.setViewportSize({ width: 375, height: 812 })
  await accountMenuTrigger.click()
  await expect(profile).toBeVisible()
  await expect(logout).toBeVisible()
  const horizontalOverflow = await page.evaluate(() =>
    document.documentElement.scrollWidth > globalThis.innerWidth)
  expect(horizontalOverflow).toBe(false)

  const revoked = page.waitForResponse(response =>
    response.url().endsWith('/api/v1/auth/logout')
    && response.status() === 204)
  await logout.click()
  await revoked
  await expect(page).toHaveURL(/\/admin\?signed_out=1$/u)
  await expect(page.getByRole('status')).toContainText(/signed out securely/iu)
  await expect(page.getByRole('heading', {
    level: 2,
    name: /administrator sign-in/iu,
  })).toBeVisible()

  const rejected = await context.request.get(
    `${fixtureBaseURL}/api/v1/admin/session`,
  )
  expect(rejected.status()).toBe(401)
  await context.close()
})

test('admin changes password with safe errors, rotates this session, and revokes the others', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN', 'LR-NEGATIVE')

  const current = await browser.newContext()
  const other = await browser.newContext()
  await session(current, 'admin')
  await session(other, 'admin')
  const page = await current.newPage()
  const otherPage = await other.newPage()
  await page.goto(`${offBaseURL}/admin/profile`)
  await otherPage.goto(`${offBaseURL}/admin`)

  await page.getByLabel(/^current password$/iu).fill('wrong-current-password')
  await page.getByLabel(/^new password$/iu).fill('fixture-new-admin-password')
  await page.getByLabel(/^confirm new password$/iu).fill('fixture-new-admin-password')
  await page.getByRole('button', { name: /^change password$/iu }).click()
  await expect(page.getByRole('alert')).toContainText(/current password is invalid/iu)

  await page.getByLabel(/^current password$/iu).fill('fixture-admin-password')
  await page.getByLabel(/^new password$/iu).fill('fixture-new-admin-password')
  await page.getByLabel(/^confirm new password$/iu).fill('different-confirmation')
  await page.getByRole('button', { name: /^change password$/iu }).click()
  await expect(page.getByRole('alert')).toContainText(/do not match/iu)

  await page.getByLabel(/^current password$/iu).fill('fixture-admin-password')
  await page.getByLabel(/^new password$/iu).fill('fixture-admin-password')
  await page.getByLabel(/^confirm new password$/iu).fill('fixture-admin-password')
  await page.getByRole('button', { name: /^change password$/iu }).click()
  await expect(page.getByRole('alert')).toContainText(/different password/iu)

  await page.getByLabel(/^current password$/iu).fill('fixture-admin-password')
  await page.getByLabel(/^new password$/iu).fill('fixture-new-admin-password')
  await page.getByLabel(/^confirm new password$/iu).fill('fixture-new-admin-password')
  const changed = page.waitForResponse(response =>
    response.url().endsWith('/api/v1/auth/password/change')
    && response.status() === 200)
  await page.getByRole('button', { name: /^change password$/iu }).click()
  await changed
  await expect(page.getByRole('status')).toContainText(/other sessions were revoked/iu)
  await expect(
    page.locator('.admin-profile-details').getByText(
      'admin@example.test',
      { exact: true },
    ),
  ).toBeVisible()

  const oldSessionRejected = otherPage.waitForResponse(response =>
    response.url().endsWith('/api/v1/admin/session')
    && response.status() === 401)
  await otherPage.reload()
  await oldSessionRejected
  await expect(otherPage.getByRole('heading', {
    level: 2,
    name: /administrator sign-in/iu,
  })).toBeVisible()

  const accountMenu = page.locator('.admin-profile-menu')
  await accountMenu.locator('summary').click()
  await accountMenu.getByRole('button', { name: /^sign out$/iu }).click()
  await page.getByLabel(/email address/iu).fill('admin@example.test')
  await page.getByLabel(/^password$/iu).fill('fixture-admin-password')
  await page.getByRole('button', { name: /^sign in$/iu }).click()
  await expect(page.getByRole('alert')).toContainText(/invalid/iu)
  await page.getByLabel(/^password$/iu).fill('fixture-new-admin-password')
  await page.getByRole('button', { name: /^sign in$/iu }).click()
  await expect(page.getByRole('heading', { level: 2, name: /service health/iu }))
    .toBeVisible()

  await current.close()
  await other.close()
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

test('admin user and workspace directories keep server filters in the URL and export safely', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN', 'LR-NEGATIVE', 'LR-WCAG')

  const context = await browser.newContext({ acceptDownloads: true })
  await session(context, 'admin')
  const page = await context.newPage()

  const userResponse = page.waitForResponse((response) => {
    const url = new URL(response.url())
    return url.pathname === '/api/v1/admin/users'
      && url.searchParams.get('status') === 'locked'
      && url.searchParams.get('page_size') === '10'
  })
  await page.goto(
    `${offBaseURL}/admin/users?status=locked&email_verified=false`
    + '&plan=team&login_method=linkedin&page=1&page_size=10'
    + '&sort=email&direction=asc',
  )
  expect((await userResponse).status()).toBe(200)
  await expect(page).toHaveURL(/status=locked/u)
  await expect(page).toHaveURL(/email_verified=false/u)
  const userTable = page.locator('.admin-table tbody')
  await expect(userTable.getByText('locked@example.test')).toBeVisible()
  await expect(userTable.getByText('admin@example.test')).toHaveCount(0)
  await expect(page.getByText(/1 matching result/iu)).toBeVisible()

  await page.getByRole('button', { name: /view details/iu }).click()
  const detail = page.getByRole('dialog')
  await expect(detail).toContainText('Locked Fixture')
  await expect(detail).toContainText('google, linkedin')
  await page.getByRole('button', { name: /^close$/iu }).click()

  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: /export csv/iu }).click()
  const download = await downloadPromise
  expect(download.suggestedFilename()).toBe('postqron-admin-users.csv')

  await page.getByLabel(/quick search/iu).fill('no-result-user')
  await page.getByRole('button', { name: /apply filters/iu }).click()
  await expect(page).toHaveURL(/q=no-result-user/u)
  await expect(page.getByText(/no matching administration data/iu)).toBeVisible()

  await page.goto(
    `${offBaseURL}/admin/workspaces?status=active&plan=pro`
    + '&owner=admin%40example.test&page=1&page_size=25'
    + '&sort=channel_count&direction=desc',
  )
  await expect(page.getByText('Fixture Workspace')).toBeVisible()
  await expect(page.getByText('Locked Studio')).not.toBeVisible()
  await expect(page.getByRole('link', {
    name: 'Fixture Admin',
    exact: true,
  })).toHaveAttribute(
    'href',
    /\/admin\/users\?q=admin%40example\.test$/u,
  )
  await expect(page.getByRole('link', {
    name: 'pro',
    exact: true,
  })).toHaveAttribute(
    'href',
    /\/admin\/plans$/u,
  )

  for (const path of ['/admin/users', '/admin/workspaces']) {
    await page.goto(`${offBaseURL}${path}`)
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze()
    const blocking = results.violations.filter(violation =>
      violation.impact === 'serious' || violation.impact === 'critical')
    expect(blocking, path).toEqual([])
  }
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

  const checkoutCalls: string[] = []
  page.on('request', (request) => {
    if (request.method() === 'POST' && /\/billing\/checkout$/u.test(request.url())) {
      checkoutCalls.push(request.url())
    }
  })

  await page.goto(
    `${offBaseURL}/app/billing/checkout?plan=pro&interval=monthly&quantity=6`,
  )
  // The summary must be readable before Paddle ever opens: no auto-open.
  await expect(page.getByText(
    /6 social channels|6 canali social|6 canales sociales|6 canaux sociaux|6 social-media-kanäle/iu,
  )).toBeVisible()
  await expect(page.getByText(
    /base recurring total|totale ricorrente base|total recurrente base|total récurrent de base|wiederkehrender basispreis/iu,
  )).toBeVisible()
  const openCheckoutButton = page.getByRole('button', {
    name: /open secure checkout|apri il checkout sicuro|abrir el pago seguro|ouvrir le paiement sécurisé|sicheren checkout öffnen/iu,
  })
  await expect(openCheckoutButton).toBeVisible()
  expect(checkoutCalls, 'no checkout call before the CTA is clicked').toHaveLength(0)

  await openCheckoutButton.click()
  await expect(page.getByText(/processing|elaborazione|procesando|traitement|verarbeitet/iu))
    .toBeVisible()
  expect(checkoutCalls.length).toBeGreaterThan(0)

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

test('an incompatible plan and quantity combination is rejected before any checkout call', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-PADDLE', 'LR-NEGATIVE')
  const context = await browser.newContext()
  await session(context, 'authenticated')
  const page = await context.newPage()
  const checkoutCalls: string[] = []
  page.on('request', (request) => {
    if (request.method() === 'POST' && /\/billing\/checkout$/u.test(request.url())) {
      checkoutCalls.push(request.url())
    }
  })

  // Pro's catalog limit is 6 channels: 7 is a well-formed but incompatible
  // quantity that must never reach the checkout endpoint.
  await page.goto(
    `${offBaseURL}/app/billing/checkout?plan=pro&interval=monthly&quantity=7`,
  )
  await expect(page.getByRole('alert')).toContainText(
    /no longer compatible|non sono più compatibili|ya no son compatibles|ne sont plus compatibles|nicht mehr kompatibel/iu,
  )
  await expect(page.getByRole('button', {
    name: /open secure checkout|apri il checkout sicuro|abrir el pago seguro|ouvrir le paiement sécurisé|sicheren checkout öffnen/iu,
  })).toHaveCount(0)
  expect(checkoutCalls, 'no checkout call for an incompatible intent').toHaveLength(0)
  await context.close()
})

test('Unlimited checkout is flat-rate, sends no channel quantity, and passes WCAG checks', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-PADDLE', 'LR-WCAG')
  const context = await browser.newContext()
  await session(context, 'authenticated')
  const page = await context.newPage()
  const openedTransactions: unknown[] = []
  await page.addInitScript(() => {
    const fixturePaddle = {
      Environment: { set: (_environment: 'sandbox') => {} },
      Initialize: (_options: unknown) => {},
      Checkout: {
        open: (options: unknown) => {
          (globalThis as unknown as { __paddleOpen?: unknown[] }).__paddleOpen
            ??= []
          ;(globalThis as unknown as { __paddleOpen: unknown[] }).__paddleOpen
            .push(options)
        },
      },
    }
    Object.defineProperty(globalThis, 'Paddle', {
      value: fixturePaddle,
      configurable: true,
    })
  })

  const paddleOpenCount = () => page.evaluate(() =>
    (globalThis as unknown as { __paddleOpen?: unknown[] }).__paddleOpen?.length ?? 0)

  for (const interval of ['monthly', 'annual'] as const) {
    // Each iteration is a full navigation to a fresh document, so
    // window.__paddleOpen starts empty again every time: the expectation
    // below is always "exactly one open on this page", not a running total.
    await page.goto(
      `${offBaseURL}/app/billing/checkout?plan=unlimited&interval=${interval}`,
    )
    await expect(page.getByText(
      /flat-rate|prezzo fisso|precio fijo|prix fixe|festpreis/iu,
    )).toBeVisible()

    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze()
    const blocking = results.violations.filter(violation =>
      violation.impact === 'serious' || violation.impact === 'critical')
    expect(blocking, interval).toEqual([])

    // Paddle only opens after the explicit CTA, never automatically.
    await page.getByRole('button', {
      name: /open secure checkout|apri il checkout sicuro|abrir el pago seguro|ouvrir le paiement sécurisé|sicheren checkout öffnen/iu,
    }).click()
    // "Preparing…" renders synchronously on click, before the async
    // checkout POST resolves and Paddle.Checkout.open actually runs, so it
    // cannot be used to confirm the transaction opened. Poll the fixture's
    // own record of Paddle.Checkout.open calls instead, before navigating
    // away to the next interval.
    await expect.poll(paddleOpenCount, {
      message: `Paddle.Checkout.open was not recorded for ${interval}`,
    }).toBe(1)
  }

  openedTransactions.push(
    ...await page.evaluate(() =>
      (globalThis as unknown as { __paddleOpen?: unknown[] }).__paddleOpen ?? []),
  )
  expect(openedTransactions.length).toBeGreaterThan(0)
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
    ['authenticated', '/app/billing'],
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
