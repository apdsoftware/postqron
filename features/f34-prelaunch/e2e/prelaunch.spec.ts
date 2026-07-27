import { expect, test } from '@playwright/test'

const localeExpectations = {
  en: 'Your social content, finally in order.',
  it: 'I tuoi contenuti social, finalmente in ordine.',
  es: 'Tu contenido social, por fin en orden.',
  fr: 'Vos contenus sociaux, enfin en ordre.',
  de: 'Deine Social-Media-Inhalte, endlich geordnet.',
} as const

const localizedChannelValues = {
  en: ['1 social channel', '10+ social channels'],
  it: ['1 canale social', '10+ canali social'],
  es: ['1 canal social', '10+ canales sociales'],
  fr: ['1 canal social', '10+ canaux sociaux'],
  de: ['1 Social-Media-Kanal', '10+ Social-Media-Kanäle'],
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
    await expect(page.locator('.prelaunch-plan')).toHaveCount(4)
    await expect(page.locator('.prelaunch-plan').first()).toContainText('Start')
    const quantity = page.getByRole('slider')
    await expect(quantity).toHaveValue('1')
    await expect(quantity).toHaveAttribute(
      'aria-valuetext',
      localizedChannelValues[locale as keyof typeof localizedChannelValues][0],
    )
    await quantity.fill('10')
    await expect(quantity).toHaveAttribute(
      'aria-valuetext',
      localizedChannelValues[locale as keyof typeof localizedChannelValues][1],
    )
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

test('pricing controls preserve only compatible pre-launch selections', async ({
  page,
}) => {
  await page.goto('/en/prelaunch')

  const quantity = page.getByLabel('Social channels')
  const monthly = page.getByRole('button', { name: 'Monthly', exact: true })
  const annual = page.getByRole('button', {
    name: 'Annual — pay 10 months out of 12',
    exact: true,
  })
  const start = page.locator('[data-plan="start"]')
  const pro = page.locator('[data-plan="pro"]')
  const team = page.locator('[data-plan="team"]')
  const unlimited = page.locator('[data-plan="unlimited"]')

  await expect(quantity).toHaveValue('1')
  await expect(monthly).toHaveAttribute('aria-pressed', 'true')
  await expect(start.getByRole('radio')).toBeChecked()
  await expect(page.getByText(
    'With annual billing you pay 10 monthly instalments upfront and use the service for 12 months.',
    { exact: false },
  )).toBeVisible()

  await quantity.fill('4')
  await expect(start.getByRole('radio')).toBeDisabled()
  await expect(start.getByRole('link')).toHaveCount(0)
  await expect(pro.getByRole('radio')).toBeChecked()

  await team.getByRole('radio').check()
  await quantity.fill('5')
  await expect(team.getByRole('radio')).toBeChecked()
  await annual.click()
  await expect(annual).toHaveAttribute('aria-pressed', 'true')
  await expect(quantity).toHaveValue('5')
  await expect(team.getByRole('radio')).toBeChecked()
  await expect(team).toContainText('You pay 10 months, you use the service for 12')

  await quantity.fill('10')
  await expect(start.getByRole('radio')).toBeDisabled()
  await expect(pro.getByRole('radio')).toBeDisabled()
  await expect(team.getByRole('radio')).toBeDisabled()
  await expect(unlimited.getByRole('radio')).toBeChecked()
  await expect(start.getByRole('link')).toHaveCount(0)
  await expect(pro.getByRole('link')).toHaveCount(0)
  await expect(team.getByRole('link')).toHaveCount(0)
  await expect(unlimited.getByRole('link')).toHaveAttribute(
    'href',
    '/en/prelaunch/access',
  )
  await expect(unlimited.getByRole('link')).not.toHaveAttribute('href', /\?/u)
})

test('channel slider supports discrete keyboard changes and visible thresholds', async ({
  page,
}) => {
  await page.goto('/en/prelaunch')
  const quantity = page.getByRole('slider', { name: 'Social channels' })
  await quantity.focus()
  await quantity.press('ArrowRight')
  await expect(quantity).toHaveValue('2')
  await quantity.press('End')
  await expect(quantity).toHaveValue('10')
  await expect(quantity).toHaveAttribute('aria-valuetext', '10+ social channels')
  await expect(page.locator('.prelaunch-pricing__markers')).toContainText('10+')
  await expect(page.locator('.prelaunch-pricing__guide')).toHaveText(
    '1–3 Start · 4–6 Pro · 7–9 Team · 10+ Unlimited',
  )
})

test('pricing controls do not overflow supported viewport widths', async ({
  page,
}) => {
  for (const width of [320, 375, 768, 1024]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/en/prelaunch')
    const dimensions = await page.evaluate(() => ({
      clientWidth: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth,
    }))
    expect(
      dimensions.scrollWidth,
      `horizontal overflow at ${width}px`,
    ).toBeLessThanOrEqual(dimensions.clientWidth)
  }
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
