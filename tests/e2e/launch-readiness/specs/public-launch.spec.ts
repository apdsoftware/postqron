import AxeBuilder from '@axe-core/playwright'
import { expect, test } from '@playwright/test'
import {
  assertNoSensitiveDiagnostics,
  captureDiagnostics,
  covers,
  fixtureReset,
  locales,
  localized,
  offBaseURL,
  onBaseURL,
} from '../helpers.ts'

test.beforeEach(async () => {
  await fixtureReset()
})

test('pre-launch fails closed when enabled and exposes the normal site when disabled', async ({
  page,
}, testInfo) => {
  covers(testInfo, 'LR-PRELAUNCH', 'LR-NEGATIVE')

  const gated = await page.goto(`${onBaseURL}/prezzi`)
  expect(gated?.status()).toBe(200)
  await expect(page).toHaveURL(/\/prelaunch$/u)
  await expect(page.locator('body')).toHaveAttribute('data-prelaunch-mode', 'on')

  const publicResponse = await page.goto(`${offBaseURL}/prezzi`)
  expect(publicResponse?.status()).toBe(200)
  await expect(page).toHaveURL(/\/prezzi$/u)
  await expect(page.locator('body')).toHaveAttribute('data-prelaunch-mode', 'off')

  const stale = await page.goto(`${offBaseURL}/prelaunch`)
  expect(stale?.status()).toBe(200)
  await expect(page).toHaveURL(/\/app$/u)
  await expect(page.locator('.auth-page__main > section h1').first()).toBeVisible()
})

test('legal, support and contact launch URLs are successful and never 404', async ({
  page,
}, testInfo) => {
  covers(testInfo, 'LR-LEGAL', 'LR-SUPPORT')

  for (const path of ['/legal/termini', '/legal/privacy', '/legal/cookie']) {
    const response = await page.goto(`${offBaseURL}${path}`)
    expect(response?.status(), path).toBe(200)
  }

  const contact = await page.goto(`${offBaseURL}/contatti`)
  expect(contact?.status()).toBe(200)
  await expect(page.locator('a[href="mailto:help@postqron.com"]').first())
    .toBeVisible()
  const home = await page.goto(`${offBaseURL}/`)
  expect(home?.status()).toBe(200)
  await expect(page.locator('a[href="mailto:help@postqron.com"]').first())
    .toBeVisible()
  await expect(page.locator('a[href="/contatti"]')).toBeVisible()
})

test('cookie consent is default-deny and supports accept, reject and revocation', async ({
  page,
}, testInfo) => {
  covers(testInfo, 'LR-CONSENT', 'LR-ANALYTICS', 'LR-NEGATIVE')
  const analyticsRequests: string[] = []
  page.on('request', request => {
    if (/(?:analytics|collect|segment|plausible|matomo)/iu.test(request.url())) {
      analyticsRequests.push(request.url())
    }
  })

  await page.goto(`${offBaseURL}/`)
  await expect(page.locator('html')).toHaveAttribute('data-cookie-analytics', 'denied')
  await expect(page.locator('html')).toHaveAttribute('data-cookie-marketing', 'denied')
  expect(analyticsRequests).toEqual([])

  await page.getByRole('button', { name: 'Accept all' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-cookie-analytics', 'granted')
  await expect(page.locator('html')).toHaveAttribute('data-cookie-marketing', 'granted')

  await page.getByRole('button', { name: 'Manage cookies' }).click()
  await page.getByRole('button', { name: 'Reject all' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-cookie-analytics', 'denied')
  await expect(page.locator('html')).toHaveAttribute('data-cookie-marketing', 'denied')

  await page.getByRole('button', { name: 'Manage cookies' }).click()
  await page.getByLabel('Analytics').check()
  await page.getByRole('button', { name: 'Save preferences' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-cookie-analytics', 'granted')
  await page.getByRole('button', { name: 'Manage cookies' }).click()
  await page.getByLabel('Analytics').uncheck()
  await page.getByRole('button', { name: 'Save preferences' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-cookie-analytics', 'denied')
})

test('locale detection, precedence, fallback and language changes are deterministic', async ({
  browser,
  page,
}, testInfo) => {
  covers(testInfo, 'LR-I18N', 'LR-NEGATIVE')

  const french = await browser.newContext({ locale: 'fr-FR' })
  const frenchPage = await french.newPage()
  await frenchPage.goto(`${offBaseURL}/faq`)
  await expect(frenchPage.locator('html')).toHaveAttribute('lang', 'fr')
  await expect(frenchPage).toHaveURL(/\/fr\/faq$/u)
  await french.close()

  await page.context().addCookies([{
    name: 'postqron_locale',
    value: 'it',
    domain: new URL(offBaseURL).hostname,
    path: '/',
    sameSite: 'Lax',
  }])
  await page.goto(`${offBaseURL}/de/faq`)
  await expect(page.locator('html')).toHaveAttribute('lang', 'de')
  await page.getByRole('link', { name: 'Español' }).click()
  await expect(page).toHaveURL(/\/es\/faq$/u)
  await expect(page.locator('html')).toHaveAttribute('lang', 'es')

  const unsupported = await browser.newContext({ locale: 'pt-BR' })
  const unsupportedPage = await unsupported.newPage()
  await unsupportedPage.goto(`${offBaseURL}/faq`)
  await expect(unsupportedPage.locator('html')).toHaveAttribute('lang', 'en')
  await unsupported.close()
})

test('five-locale public, pricing, cookie and pre-launch matrix has no missing keys', async ({
  page,
}, testInfo) => {
  covers(testInfo, 'LR-LOCALE-MATRIX', 'LR-I18N')

  for (const locale of locales) {
    for (const path of ['/faq', '/prezzi', '/contatti']) {
      const response = await page.goto(
        `${offBaseURL}${localized(locale, path)}`,
      )
      expect(response?.status(), `${locale}${path}`).toBe(200)
      await expect(page.locator('html')).toHaveAttribute('lang', locale)
      await expect(page.locator('body')).not.toContainText(
        /(?:MISSING_TRANSLATION|I18N_MISSING|translation missing)/iu,
      )
      if (path === '/prezzi') {
        await expect(page.locator('.pricing-grid .plan-card')).toHaveCount(3)
      }
    }
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    const cookiePanel = page.locator('.cookie-panel')
    if (!await cookiePanel.isVisible()) {
      await page.getByRole('button', { name: /Manage cookies/iu }).click()
    }
    await expect(cookiePanel).toBeVisible()
    await expect(cookiePanel).not.toContainText(
      /(?:MISSING_TRANSLATION|I18N_MISSING|translation missing)/iu,
    )

    const prelaunch = await page.goto(
      `${onBaseURL}${localized(locale, '/prelaunch')}`,
    )
    expect(prelaunch?.status(), `${locale} prelaunch`).toBe(200)
    await expect(page.locator('html')).toHaveAttribute('lang', locale)
  }
})

test('public launch surfaces pass serious and critical automated WCAG checks', async ({
  page,
}, testInfo) => {
  covers(testInfo, 'LR-WCAG')

  for (const url of [
    `${offBaseURL}/`,
    `${offBaseURL}/prezzi`,
    `${offBaseURL}/contatti`,
    `${onBaseURL}/prelaunch`,
  ]) {
    await page.goto(url)
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze()
    const blocking = results.violations.filter(violation =>
      violation.impact === 'serious' || violation.impact === 'critical')
    expect(blocking, url).toEqual([])
  }
})

test('browser console and request diagnostics contain no credentials or PII', async ({
  page,
}, testInfo) => {
  covers(testInfo, 'LR-SECURITY')
  const diagnostics = captureDiagnostics(page)

  await page.goto(`${offBaseURL}/`)
  await page.goto(`${offBaseURL}/prezzi`)
  await page.goto(`${onBaseURL}/prelaunch`)
  assertNoSensitiveDiagnostics([
    ...diagnostics.console,
    ...diagnostics.requests,
  ])
})
