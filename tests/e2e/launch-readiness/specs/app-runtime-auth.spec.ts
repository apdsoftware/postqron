import AxeBuilder from '@axe-core/playwright'
import { expect, test } from '@playwright/test'
import {
  assertNoSensitiveDiagnostics,
  captureDiagnostics,
  covers,
  fixtureMailbox,
  fixtureReset,
  offBaseURL,
} from '../helpers.ts'

test.beforeEach(async () => {
  await fixtureReset()
})

test('password registration, resend, verification, login, onboarding and profile navigation are deterministic', async ({
  browser,
  page,
}, testInfo) => {
  covers(
    testInfo,
    'LR-APP-AUTH',
    'LR-APP-ONBOARDING',
    'LR-APP-PROFILE',
    'LR-SECURITY',
  )
  const diagnostics = captureDiagnostics(page)
  const email = 'new-user@example.test'
  const password = 'correct horse battery staple'

  const register = await page.request.post(`${offBaseURL}/api/v1/auth/password/register`, {
    data: {
      email,
      password,
      confirmation: password,
      contract_country: 'IT',
      consents: [{ locale: 'it-IT' }],
    },
  })
  expect(register.status()).toBe(202)
  const registerBody = await register.json()
  expect(registerBody.verification_requested).toBe(true)
  expect(JSON.stringify(registerBody)).not.toContain('token')

  let mailbox = await fixtureMailbox(email)
  expect(mailbox).toHaveLength(1)

  const resend = await page.request.post(`${offBaseURL}/api/v1/auth/password/verify/resend`, {
    data: { email },
  })
  expect(resend.status()).toBe(202)
  const resendBody = await resend.json()
  expect(resendBody.verification_requested).toBe(true)
  expect(JSON.stringify(resendBody)).not.toContain('token')

  mailbox = await fixtureMailbox(email)
  expect(mailbox.length).toBeGreaterThanOrEqual(2)
  const token = mailbox.at(-1)?.token
  expect(token).toBeTruthy()

  const verify = await page.request.post(`${offBaseURL}/api/v1/auth/password/verify`, {
    data: { token },
  })
  expect(verify.status()).toBe(200)

  const context = await browser.newContext({ locale: 'it-IT' })
  const appPage = await context.newPage()
  const login = await appPage.request.post(`${offBaseURL}/api/v1/auth/password/login`, {
    data: { email, password },
  })
  expect(login.status()).toBe(200)

  const bootstrap = await appPage.goto(`${offBaseURL}/it/app`)
  expect(bootstrap?.status()).toBe(200)
  await expect(appPage.getByRole('main')).toBeVisible()

  const onboarding = await appPage.request.post(`${offBaseURL}/api/v1/app/onboarding`, {
    data: {
      account: { display_name: 'Nuovo Utente' },
      workspace: { mode: 'create', name: 'Studio Personale' },
      consents: [],
    },
  })
  expect([200, 201]).toContain(onboarding.status())

  const currentWorkspace = await appPage.request.get(`${offBaseURL}/api/v1/app/workspaces/current`)
  expect(currentWorkspace.status()).toBe(200)
  const workspaceBody = await currentWorkspace.json()
  expect(workspaceBody.name).toBe('Studio Personale')

  const accountResponse = await appPage.request.get(`${offBaseURL}/api/v1/account`)
  expect(accountResponse.status()).toBe(200)
  const accountBody = await accountResponse.json()
  expect(accountBody.profile.locale).toBe('it-IT')
  expect(accountBody.workspaces[0].workspace.name).toBe('Studio Personale')

  const updateProfile = await appPage.request.patch(`${offBaseURL}/api/v1/account/profile`, {
    data: {
      display_name: 'Utente Aggiornato',
      locale: 'it',
      timezone: 'Europe/Rome',
    },
  })
  expect(updateProfile.status()).toBe(200)

  for (const path of ['/it/app/home', '/it/app/profile', '/it/app/providers', '/it/app/workspace']) {
    const response = await appPage.goto(`${offBaseURL}${path}`)
    expect(response?.status(), path).toBe(200)
    await expect(appPage.getByRole('main')).toBeVisible()
  }

  const logout = await appPage.request.post(`${offBaseURL}/api/v1/auth/logout`)
  expect(logout.status()).toBe(204)
  await context.close()

  assertNoSensitiveDiagnostics([
    ...diagnostics.console,
    ...diagnostics.requests,
  ])
})

test('mobile app navigation and provider optional flows fail closed without real credentials', async ({
  browser,
}, testInfo) => {
  covers(
    testInfo,
    'LR-APP-MOBILE',
    'LR-APP-PROVIDERS',
    'LR-NEGATIVE',
    'LR-WCAG',
  )
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    locale: 'it-IT',
  })
  await context.addCookies([{
    name: 'postqron_fixture_session',
    value: 'authenticated',
    domain: new URL(offBaseURL).hostname,
    path: '/',
    httpOnly: true,
    sameSite: 'Lax',
    secure: false,
  }])
  const page = await context.newPage()

  for (const path of ['/it/app/home', '/it/app/providers', '/it/app/privacy']) {
    const response = await page.goto(`${offBaseURL}${path}`)
    expect(response?.status(), path).toBe(200)
    await expect(page.getByRole('main')).toBeVisible()
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth > globalThis.innerWidth)
    expect(overflow, path).toBe(false)
  }

  const bootstrap = await page.request.get(`${offBaseURL}/api/v1/app/bootstrap`)
  const bootstrapBody = await bootstrap.json()
  expect(bootstrapBody.providers).toEqual(['google', 'apple', 'facebook', 'linkedin'])

  const authorize = await page.request.post(`${offBaseURL}/api/v1/auth/authorize`, {
    data: { provider: 'google', intent: 'login' },
  })
  expect(authorize.status()).toBe(503)
  const authorizeBody = await authorize.json()
  expect(authorizeBody.error.code).toBe('AUTH_PROVIDER_UNAVAILABLE')

  const callback = await page.request.get(
    `${offBaseURL}/api/v1/auth/callback?provider=google&error=access_denied`,
  )
  expect(callback.status()).toBe(400)
  const callbackBody = await callback.json()
  expect(callbackBody.error.code).toBe('AUTH_PROVIDER_ACCESS_DENIED')

  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
    .analyze()
  const blocking = results.violations.filter(violation =>
    violation.impact === 'serious' || violation.impact === 'critical')
  expect(blocking).toEqual([])

  await context.close()
})
