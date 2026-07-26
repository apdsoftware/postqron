import { expect, test } from '@playwright/test'

const localeExpectations = {
  en: 'Your social content, finally in order.',
  it: 'I tuoi contenuti social, finalmente in ordine.',
  es: 'Tu contenido social, por fin en orden.',
  fr: 'Vos contenus sociaux, enfin en ordre.',
  de: 'Deine Social-Media-Inhalte, endlich geordnet.',
} as const

for (const [locale, heading] of Object.entries(localeExpectations)) {
  test(`renders the ${locale} pre-launch experience`, async ({ page }) => {
    await page.goto(`/${locale}/prelaunch`)
    const prefix = `/${locale}`
    await expect(page).toHaveTitle(/Postqron/u)
    await expect(page.locator('html')).toHaveAttribute('lang', locale)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(heading)
    await expect(page.locator('.prelaunch-hero').getByRole('link', {
      name: /access|acceso|accès|accesso|zugang/iu,
    })).toHaveAttribute('href', `${prefix}/prelaunch/access`)
    await expect(page.locator(`a[href="${prefix}/legal/privacy"]`)).toBeVisible()
    await expect(page.locator('a[href="mailto:help@postqron.com"]')).toBeVisible()
    await expect(page.locator('.prelaunch-plan')).toHaveCount(3)
    await expect(page.locator('.prelaunch-plan').first()).toContainText('Start')
  })
}

test('falls back to English for an unsupported browser language', async ({
  browser,
}) => {
  const context = await browser.newContext({ locale: 'pt-BR' })
  const page = await context.newPage()
  await page.goto('/prelaunch')
  await expect(page).toHaveURL(/\/en\/prelaunch$/u)
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await context.close()
})

test('access request is explicit and separate from marketing', async ({
  page,
}) => {
  let body: URLSearchParams | undefined
  await page.route('**/api/v1/prelaunch/access-requests', async route => {
    body = new URLSearchParams(route.request().postData() || '')
    await route.fulfill({
      status: 303,
      headers: {
        location: 'http://127.0.0.1:41734/prelaunch/access?result=success',
      },
    })
  })
  await page.goto('/en/prelaunch/access')
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute(
    'content',
    'noindex, nofollow',
  )
  await page.getByLabel('Email address').fill('person@example.test')
  await page.getByRole('checkbox').check()
  await page.getByRole('button', { name: 'Request access' }).click()
  await expect(page.locator('.prelaunch-access__notice')).toContainText(
    'Request received',
  )
  expect(body?.get('email')).toBe('person@example.test')
  expect(body?.get('locale')).toBe('en')
  expect(body?.get('access_consent')).toBe('true')
  expect(body?.get('marketing_consent')).toBe('false')
  expect(body?.get('consent_policy_version')).toBe('prelaunch-access-v1')
})

test('keyboard navigation reaches the primary CTA', async ({ page }) => {
  await page.goto('/en/prelaunch')
  await page.keyboard.press('Tab')
  await page.keyboard.press('Enter')
  await expect(page.locator('#main-content')).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(page.locator('.prelaunch-hero').getByRole('link', {
    name: 'Request early access',
  })).toBeFocused()
})

test('go-live redirects obsolete pre-launch URLs to the app', async ({
  page,
}) => {
  await page.goto('http://127.0.0.1:41735/prelaunch')
  await expect(page).toHaveURL(/\/en\/app$/u)
  await expect(page.locator('body')).not.toContainText(
    'Your social content, finally in order.',
  )
})
