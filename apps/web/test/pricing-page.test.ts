import { describe, expect, it } from 'vitest'

import { pricingPages } from '~/content/pricing'
import { siteContent } from '~/content'
import { LOCALE_CODES } from '~/utils/locale'

describe('pagina prezzi', () => {
  it.each(LOCALE_CODES)('%s riporta tutte le righe della matrice di SPEC §8', (locale) => {
    const page = pricingPages[locale]

    expect(page.rows).toHaveLength(14)
    expect(page.rows.every(row => row.values.length === 4)).toBe(true)
  })

  it.each(LOCALE_CODES)('%s dichiara il fair use Team e i dieci workspace Agency', (locale) => {
    const serialized = JSON.stringify(pricingPages[locale])

    expect(serialized).toMatch(/1[.,\s]000/)
    expect(serialized).toContain('10')
  })

  it.each(LOCALE_CODES)('%s offre l’annuale esclusivamente su Pro a 90 euro', (locale) => {
    const plans = siteContent[locale].plans

    expect(plans.map(plan => plan.annual?.price ?? null)).toEqual([null, '90', null, null])
    expect(plans[1]!.annual!.savingNote).toBeTruthy()
  })

  it.each(LOCALE_CODES)('%s non manda i piani a pagamento a un checkout inesistente', (locale) => {
    const paidPlans = siteContent[locale].plans.slice(1)

    expect(paidPlans.every(plan => plan.ctaTo === '/contact')).toBe(true)
    expect(pricingPages[locale].checkoutNote).toBeTruthy()
  })

  // La regola del downgrade è stata decisa (R58): si sospende tutto e riattiva
  // l'utente. Il test verificava che la pagina dichiarasse il criterio «da
  // decidere»; ora verifica che dichiari *la regola*, perché una pagina prezzi
  // che tace su cosa succede ai job è peggio di una che ammette di non saperlo.
  it.each(LOCALE_CODES)('%s dice che il downgrade sospende tutto e la scelta è dell\'utente', (locale) => {
    const answer = pricingPages[locale].downgrade.answer
    expect(answer.length).toBeGreaterThan(80)
    // «non scegliamo noi»: la parte che rende la regola accettabile.
    expect(answer).toMatch(/non scegliamo|do not pick|no elegimos|wählen nicht|ne choisissons pas/i)
    // e la promessa che nulla viene cancellato.
    expect(answer).toMatch(/cancelliamo|deleted|borramos|gelöscht|supprimé/i)
  })
})
