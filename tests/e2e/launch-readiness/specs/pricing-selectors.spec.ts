import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, test } from '@playwright/test'
import {
  covers,
  fixtureReset,
  locales,
  localized,
  offBaseURL,
  onBaseURL,
} from '../helpers.ts'

// F2 (`features/f02-marketing-site/components/PlanCatalog.vue`) and F34
// (`features/f34-prelaunch/components/PrelaunchPricing.vue`) render the same
// shared model (`features/f02-marketing-site/src/pricing-model.ts` and
// `src/catalog.ts`) with the same copy keys and roles, so both surfaces are
// exercised through the same locators here.
const surfaces = [
  {
    id: 'public pricing (F2 /prezzi)',
    baseURL: offBaseURL,
    path: '/prezzi',
    ctaKind: 'per-plan' as const,
  },
  {
    id: 'prelaunch pricing (F34 /prelaunch)',
    baseURL: onBaseURL,
    path: '/prelaunch',
    ctaKind: 'shared' as const,
    accessPath: '/prelaunch/access',
  },
]

// Mirrors the deterministic `d09-v2` catalog served by
// fixtures/fixture-api.mjs: start=3/pro=6/team=9/unlimited=null channels,
// tiered per-channel prices of 450/900 cents (monthly) and 4500/9000 cents
// (annual) for Pro/Team, and a flat 12900/129000 for Unlimited.
const channelLimit = { Start: 3, Pro: 6, Team: 9 } as const

type Locale = typeof locales[number]

function surfaceURL(surface: typeof surfaces[number], locale: Locale): string {
  return `${surface.baseURL}${localized(locale, surface.path)}`
}

// Accessible names for the shared pricing controls (`PRICING_COPY` in
// `features/f02-marketing-site/src/catalog.ts`), one per supported locale.
// Needed because every other locator here only works against the English
// default (`surfaceURL(surface, 'en')`).
const PRICING_LABELS: Record<Locale, {
  monthly: string
  interval: string
  quantity: string
  planGroup: string
}> = {
  en: { monthly: 'Monthly', interval: 'Billing frequency', quantity: 'Social channels', planGroup: 'Choose a plan' },
  it: { monthly: 'Mensile', interval: 'Frequenza di fatturazione', quantity: 'Canali social', planGroup: 'Scegli un piano' },
  es: { monthly: 'Mensual', interval: 'Frecuencia de facturación', quantity: 'Canales sociales', planGroup: 'Elige un plan' },
  fr: { monthly: 'Mensuel', interval: 'Fréquence de facturation', quantity: 'Canaux sociaux', planGroup: 'Choisissez un abonnement' },
  de: { monthly: 'Monatlich', interval: 'Abrechnungszeitraum', quantity: 'Social-Media-Kanäle', planGroup: 'Wähle einen Tarif' },
}

function controls(page: Page, locale: Locale = 'en') {
  const labels = PRICING_LABELS[locale]
  return {
    intervalGroup: page.getByRole('group', { name: labels.interval }),
    monthlyButton: page.getByRole('button', { name: labels.monthly, exact: true }),
    // Only used against the English surface; other locales never resolve
    // this locator.
    annualButton: page.getByRole('button', { name: /Annual/u }),
    quantitySelect: page.getByRole('combobox', { name: labels.quantity }),
    radiogroup: page.getByRole('radiogroup', { name: labels.planGroup }),
    // Scoped to `main`: the site header's language switcher also carries
    // `aria-live="polite"` status paragraphs, which would otherwise collide.
    liveRegion: page.getByRole('main').locator('[aria-live="polite"]'),
  }
}

function radio(page: Page, name: string, locale: Locale = 'en') {
  return controls(page, locale).radiogroup.getByRole('radio', { name, exact: true })
}

function card(page: Page, name: string, locale: Locale = 'en') {
  return controls(page, locale).radiogroup.locator('article')
    .filter({ has: page.getByRole('radio', { name, exact: true }) })
}

function amountText(cents: number, locale = 'en'): string {
  return (cents / 100).toLocaleString(locale, {
    minimumFractionDigits: cents % 100 === 0 ? 0 : 2,
    maximumFractionDigits: 2,
  })
}

function interpolate(
  message: string,
  parameters: Readonly<Record<string, string | number>>,
): string {
  return message.replace(/\{([A-Za-z][A-Za-z0-9_]*)\}/gu, (_match, key: string) =>
    String(parameters[key] ?? `{${key}}`))
}

// Locale-specific static copy fragments (`PRICING_COPY` in `catalog.ts`),
// used to confirm actual localized values render, not just that some text
// exists. Placeholder-adjacent fragments below are the static text around
// `{count}`/`{amount}`, which interpolate() replaces with catalog-derived
// numbers regardless of locale.
const LOCALE_COPY: Record<Locale, {
  unlimitedName: string
  usersIncludedManySuffix: string
  perChannelMonthlySuffix: string
  unlimitedFlat: string
}> = {
  en: {
    unlimitedName: 'Unlimited',
    usersIncludedManySuffix: 'users included, never billed individually',
    perChannelMonthlySuffix: 'per channel per month',
    unlimitedFlat: 'Flat price, independent of the number of channels',
  },
  it: {
    unlimitedName: 'Illimitato',
    usersIncludedManySuffix: 'utenti inclusi, mai addebitati singolarmente',
    perChannelMonthlySuffix: 'per canale al mese',
    unlimitedFlat: 'Prezzo fisso, indipendente dal numero di canali',
  },
  es: {
    unlimitedName: 'Ilimitado',
    usersIncludedManySuffix: 'usuarios incluidos, nunca facturados por separado',
    perChannelMonthlySuffix: 'por canal al mes',
    unlimitedFlat: 'Precio fijo, independiente del número de canales',
  },
  fr: {
    unlimitedName: 'Illimité',
    usersIncludedManySuffix: 'utilisateurs inclus, jamais facturés individuellement',
    perChannelMonthlySuffix: 'par canal et par mois',
    unlimitedFlat: 'Prix fixe, indépendant du nombre de canaux',
  },
  de: {
    unlimitedName: 'Unbegrenzt',
    usersIncludedManySuffix: 'Benutzer enthalten, nie einzeln berechnet',
    perChannelMonthlySuffix: 'pro Kanal und Monat',
    unlimitedFlat: 'Festpreis, unabhängig von der Kanalanzahl',
  },
}

const EN_ANNUAL_EXPLAINER = 'With annual billing you pay {months} monthly '
  + 'instalments upfront and use the service for {serviceMonths} months. '
  + 'You save {percent} compared to monthly billing.'
const IT_ANNUAL_EXPLAINER = 'Con la fatturazione annuale paghi anticipatamente '
  + '{months} mensilità e utilizzi il servizio per {serviceMonths} mesi. '
  + 'Risparmi il {percent} rispetto al mensile.'
const ANNUAL_TERMS = { months: '10', serviceMonths: '12' }

// Every locale's exact, catalog-interpolated annual explainer (`catalog.ts`
// `annualExplainer`), used to confirm the 10-for-12 terms render as real
// localized copy in all five languages, not just as bare digits.
const ANNUAL_EXPLAINER_TEMPLATE: Record<Locale, string> = {
  en: EN_ANNUAL_EXPLAINER,
  it: IT_ANNUAL_EXPLAINER,
  es: 'Con la facturación anual pagas por adelantado {months} mensualidades '
    + 'y usas el servicio durante {serviceMonths} meses. Ahorras un {percent} '
    + 'respecto al mensual.',
  fr: 'Avec la facturation annuelle, vous payez {months} mensualités à '
    + 'l’avance et utilisez le service pendant {serviceMonths} mois. Vous '
    + 'économisez {percent} par rapport au mensuel.',
  de: 'Bei jährlicher Abrechnung zahlst du {months} Monatsraten im Voraus '
    + 'und nutzt den Dienst {serviceMonths} Monate lang. Du sparst {percent} '
    + 'gegenüber der monatlichen Abrechnung.',
}
const ANNUAL_EXPLAINER_PERCENT: Record<Locale, string> = {
  en: '16.67%',
  it: '16,67%',
  es: '16,67 %',
  fr: '16,67 %',
  de: '16,67 %',
}

test.beforeEach(async () => {
  await fixtureReset()
})

for (const surface of surfaces) {
  test(`${surface.id}: defaults to monthly, one channel and the minimum compatible plan`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING')

    await page.goto(surfaceURL(surface, 'en'))
    const { monthlyButton, annualButton, quantitySelect } = controls(page)

    await expect(monthlyButton).toHaveAttribute('aria-pressed', 'true')
    await expect(annualButton).toHaveAttribute('aria-pressed', 'false')
    await expect(quantitySelect).toHaveValue('1')
    await expect(radio(page, 'Start')).toBeChecked()
    for (const name of ['Start', 'Pro', 'Team', 'Unlimited']) {
      await expect(radio(page, name)).toBeEnabled()
    }
  })

  test(`${surface.id}: channel quantity transitions preselect the minimum compatible plan and disable lower ones`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING')

    await page.goto(surfaceURL(surface, 'en'))
    const { quantitySelect } = controls(page)

    const cases: Array<{
      quantity: string
      selected: string
      disabled: string[]
      enabled: string[]
    }> = [
      { quantity: '1', selected: 'Start', disabled: [], enabled: ['Start', 'Pro', 'Team', 'Unlimited'] },
      { quantity: '3', selected: 'Start', disabled: [], enabled: ['Start', 'Pro', 'Team', 'Unlimited'] },
      { quantity: '4', selected: 'Pro', disabled: ['Start'], enabled: ['Pro', 'Team', 'Unlimited'] },
      { quantity: '6', selected: 'Pro', disabled: ['Start'], enabled: ['Pro', 'Team', 'Unlimited'] },
      { quantity: '7', selected: 'Team', disabled: ['Start', 'Pro'], enabled: ['Team', 'Unlimited'] },
      { quantity: '9', selected: 'Team', disabled: ['Start', 'Pro'], enabled: ['Team', 'Unlimited'] },
      { quantity: 'over_max', selected: 'Unlimited', disabled: ['Start', 'Pro', 'Team'], enabled: ['Unlimited'] },
    ]

    for (const scenario of cases) {
      await quantitySelect.selectOption(scenario.quantity)
      await expect(radio(page, scenario.selected), scenario.quantity).toBeChecked()

      for (const name of scenario.enabled) {
        await expect(radio(page, name), `${scenario.quantity}/${name}`).toBeEnabled()
      }
      for (const name of scenario.disabled) {
        const disabledRadio = radio(page, name)
        await expect(disabledRadio, `${scenario.quantity}/${name}`).toBeDisabled()
        await expect(card(page, name)).toBeVisible()

        const reasonId = await disabledRadio.getAttribute('aria-describedby')
        expect(reasonId, `${scenario.quantity}/${name}`).toBeTruthy()
        const reason = page.locator(`#${reasonId}`)
        await expect(reason).toBeVisible()
        await expect(reason).toContainText(name)
        await expect(reason).toContainText(
          String(channelLimit[name as keyof typeof channelLimit]),
        )
      }
    }
  })

  test(`${surface.id}: an explicit higher compatible plan is preserved and falls back exactly once it stops being compatible`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING')

    await page.goto(surfaceURL(surface, 'en'))
    const { quantitySelect } = controls(page)

    await quantitySelect.selectOption('2')
    await radio(page, 'Team').check()
    await expect(radio(page, 'Team')).toBeChecked()

    await quantitySelect.selectOption('5')
    await expect(radio(page, 'Team'), 'explicit choice survives a compatible resize')
      .toBeChecked()

    await quantitySelect.selectOption('over_max')
    await expect(radio(page, 'Unlimited'), 'falls back when the explicit plan becomes incompatible')
      .toBeChecked()

    await quantitySelect.selectOption('5')
    await expect(radio(page, 'Pro'), 'the cleared explicit choice is not silently restored')
      .toBeChecked()
  })

  test(`${surface.id}: plan, quantity and interval changes are announced through an accessible live region and the plan group is keyboard operable`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING', 'LR-WCAG')

    await page.goto(surfaceURL(surface, 'en'))
    const { quantitySelect, liveRegion, annualButton } = controls(page)
    await quantitySelect.selectOption('2')

    await radio(page, 'Start').focus()
    await page.keyboard.press('ArrowRight')
    await expect(radio(page, 'Pro')).toBeChecked()
    await expect(radio(page, 'Pro')).toBeFocused()
    await expect(liveRegion).toContainText('Pro')

    await page.keyboard.press('ArrowRight')
    await expect(radio(page, 'Team')).toBeChecked()
    await expect(liveRegion).toContainText('Team')

    await annualButton.click()
    await expect(liveRegion).toContainText(/Annual|Billing: Annual/u)
  })

  test(`${surface.id}: annual billing keeps the 10-for-12 explainer visible and totals/equivalents consistent with the catalog`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING')

    await page.goto(surfaceURL(surface, 'en'))
    const { quantitySelect, annualButton, monthlyButton } = controls(page)
    const explainer = interpolate(EN_ANNUAL_EXPLAINER, {
      ...ANNUAL_TERMS,
      percent: '16.67%',
    })
    await expect(page.getByText(explainer)).toBeVisible()

    await quantitySelect.selectOption('4')
    const proCard = card(page, 'Pro')
    await expect(proCard).toContainText(amountText(1_800, 'en'))
    await expect(proCard).toContainText('Total for 4 social channels')
    await expect(proCard).toContainText(`${amountText(450, 'en')} per channel per month`)

    await annualButton.click()
    await expect(monthlyButton).toHaveAttribute('aria-pressed', 'false')
    await expect(page.getByText(explainer), 'stays visible after switching to annual')
      .toBeVisible()
    await expect(proCard).toContainText(amountText(18_000, 'en'))
    await expect(proCard).toContainText(`${amountText(18_000, 'en')} billed once a year`)
    await expect(proCard).toContainText(`Equivalent to €${amountText(1_500, 'en')}/month`)
    await expect(proCard).toContainText('You pay 10 months, you use the service for 12')
    await expect(proCard).toContainText(
      `You save €${amountText(3_600, 'en')} per year compared to monthly billing`,
    )
    await expect(proCard).toContainText(`${amountText(4_500, 'en')} per channel per year`)

    await quantitySelect.selectOption('7')
    const teamCard = card(page, 'Team')
    await expect(teamCard).toContainText(amountText(63_000, 'en'))
    await expect(teamCard).toContainText(`Equivalent to €${amountText(5_250, 'en')}/month`)
    await expect(teamCard).toContainText(
      `You save €${amountText(12_600, 'en')} per year compared to monthly billing`,
    )

    const unlimitedCard = card(page, 'Unlimited')
    await expect(unlimitedCard).toContainText('Flat price, independent of the number of channels')
    await expect(unlimitedCard).toContainText(amountText(129_000, 'en'))

    await quantitySelect.selectOption('1')
    const startCard = card(page, 'Start')
    await expect(startCard).toContainText('Free forever. No payment method.')
  })

  test(`${surface.id}: the Italian annual explainer is visible verbatim while monthly billing is selected`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING', 'LR-I18N')

    await page.goto(surfaceURL(surface, 'it'))
    await expect(controls(page, 'it').monthlyButton).toHaveAttribute('aria-pressed', 'true')
    const explainer = interpolate(IT_ANNUAL_EXPLAINER, {
      ...ANNUAL_TERMS,
      percent: '16,67%',
    })
    await expect(page.getByText(explainer)).toBeVisible()
  })

  test(`${surface.id}: every compatible plan CTA is a valid link, and an incompatible plan renders no CTA`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING')

    await page.goto(surfaceURL(surface, 'en'))
    // Default quantity is 1: every plan, including Start, is compatible.
    for (const name of ['Start', 'Pro', 'Team', 'Unlimited']) {
      const link = card(page, name).getByRole('link')
      const href = await link.getAttribute('href')
      expect(href, name).toBeTruthy()
      const target = new URL(href!, surface.baseURL)

      if (surface.ctaKind === 'shared') {
        expect(target.pathname, name).toBe(localized('en', surface.accessPath!))
      } else {
        expect(target.searchParams.get('plan'), name).toBe(name.toLowerCase())
        expect(target.searchParams.get('interval'), name).toBe('monthly')
        if (name === 'Unlimited') {
          expect(target.searchParams.has('quantity'), name).toBe(false)
        } else if (name === 'Start') {
          expect(
            target.searchParams.get('quantity'),
            'Start ignores the selector and stays at its free capacity',
          ).toBe('3')
        } else {
          expect(target.searchParams.get('quantity'), name).toBe('1')
        }
      }
    }

    // Raising the quantity to 4 makes Start incompatible: its card is
    // disabled and must not render a checkout/access CTA at all.
    await controls(page).quantitySelect.selectOption('4')
    await expect(radio(page, 'Start')).toBeDisabled()
    await expect(card(page, 'Start').getByRole('link')).toHaveCount(0)

    const monthlyHrefs = new Map<string, string>()
    for (const name of ['Pro', 'Team', 'Unlimited']) {
      const link = card(page, name).getByRole('link')
      const href = await link.getAttribute('href')
      expect(href, name).toBeTruthy()
      monthlyHrefs.set(name, href!)
      const target = new URL(href!, surface.baseURL)

      if (surface.ctaKind === 'shared') {
        expect(target.pathname, name).toBe(localized('en', surface.accessPath!))
      } else if (name === 'Unlimited') {
        expect(target.searchParams.has('quantity'), name).toBe(false)
      } else {
        expect(target.searchParams.get('quantity'), name).toBe('4')
      }
    }

    // Switching to annual must be reflected in the CTA itself: F2 carries
    // `interval=annual` (with the same quantity) into the app URL. F34's
    // CTA never encodes billing choices, so its access href must be the
    // exact same URL as before the toggle — not merely the same
    // pathname, which would miss a stray query string.
    await controls(page).annualButton.click()
    for (const name of ['Pro', 'Team', 'Unlimited']) {
      const link = card(page, name).getByRole('link')
      const href = await link.getAttribute('href')
      expect(href, name).toBeTruthy()

      if (surface.ctaKind === 'shared') {
        expect(href, name).toBe(monthlyHrefs.get(name))
        const target = new URL(href!, surface.baseURL)
        expect(target.search, name).toBe('')
      } else {
        const target = new URL(href!, surface.baseURL)
        expect(target.searchParams.get('interval'), name).toBe('annual')
        if (name === 'Unlimited') {
          expect(target.searchParams.has('quantity'), name).toBe(false)
        } else {
          expect(target.searchParams.get('quantity'), name).toBe('4')
        }
      }
    }

    if (surface.ctaKind === 'shared') {
      // F34's CTA is a `NuxtLink`: it navigates client-side, so there is no
      // document response to await, only a route change.
      await card(page, 'Pro').getByRole('link').click()
      await expect(page).toHaveURL(new RegExp(`${localized('en', surface.accessPath!)}$`, 'u'))
    } else {
      const response = await Promise.all([
        page.waitForNavigation(),
        card(page, 'Pro').getByRole('link').click(),
      ]).then(([navigation]) => navigation)
      expect(response?.status()).toBe(200)
    }
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  })

  test(`${surface.id}: five-locale pricing controls render complete copy with no missing keys`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING', 'LR-LOCALE-MATRIX', 'LR-I18N')

    for (const locale of locales) {
      const response = await page.goto(surfaceURL(surface, locale))
      expect(response?.status(), locale).toBe(200)
      await expect(page.locator('html')).toHaveAttribute('lang', locale)
      await expect(page.locator('body')).not.toContainText(
        /(?:MISSING_TRANSLATION|I18N_MISSING|translation missing)/iu,
      )

      const { intervalGroup, quantitySelect, radiogroup, liveRegion } = controls(page, locale)
      await expect(intervalGroup).toBeVisible()
      await expect(quantitySelect.locator('option')).toHaveCount(10)
      await expect(radiogroup.getByRole('radio')).toHaveCount(4)
      await expect(liveRegion).not.toBeEmpty()

      // The catalog-derived 10-for-12 terms must render as the exact,
      // catalog-interpolated localized explainer in every locale — not
      // just in English/Italian (also checked verbatim elsewhere).
      const explainer = interpolate(ANNUAL_EXPLAINER_TEMPLATE[locale], {
        ...ANNUAL_TERMS,
        percent: ANNUAL_EXPLAINER_PERCENT[locale],
      })
      await expect(page.getByText(explainer), locale).toBeVisible()

      // Default quantity is 1, so Pro is compatible and rendered with its
      // full pricing copy; assert the actual localized wording, not just
      // that some non-empty text exists.
      const snippets = LOCALE_COPY[locale]
      const proCard = card(page, 'Pro', locale)
      await expect(proCard, locale).toContainText(snippets.usersIncludedManySuffix)
      await expect(proCard, locale).toContainText(snippets.perChannelMonthlySuffix)

      const unlimitedCard = card(page, snippets.unlimitedName, locale)
      await expect(unlimitedCard, locale).toContainText(snippets.unlimitedFlat)
    }
  })

  test(`${surface.id}: fits 320/375/768/1024 viewports without horizontal overflow after selecting quantities and annual billing`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING', 'LR-WCAG')

    for (const width of [320, 375, 768, 1024]) {
      await page.setViewportSize({ width, height: 900 })
      await page.goto(surfaceURL(surface, 'en'))
      const { quantitySelect, annualButton, radiogroup } = controls(page)
      await quantitySelect.selectOption('over_max')
      await annualButton.click()

      const dimensions = await page.evaluate(() => ({
        clientWidth: document.documentElement.clientWidth,
        scrollWidth: document.documentElement.scrollWidth,
      }))
      expect(dimensions.scrollWidth, `${width}`).toBeLessThanOrEqual(dimensions.clientWidth)
      await expect(quantitySelect, `${width}`).toBeVisible()
      await expect(radiogroup, `${width}`).toBeVisible()
    }
  })

  test(`${surface.id}: interactive states with disabled cards pass serious and critical WCAG checks`, async ({
    page,
  }, testInfo) => {
    covers(testInfo, 'LR-PRICING', 'LR-WCAG')

    await page.goto(surfaceURL(surface, 'en'))
    await controls(page).quantitySelect.selectOption('7')
    await controls(page).annualButton.click()

    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze()
    const blocking = results.violations.filter(violation =>
      violation.impact === 'serious' || violation.impact === 'critical')
    expect(blocking).toEqual([])
  })
}
