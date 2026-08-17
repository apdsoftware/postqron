import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  COOKIE_CONSENT_MAX_AGE,
  COOKIE_CONSENT_NAME,
  COOKIE_POLICY_VERSION,
  createCookieConsent,
  persistCookieConsent,
  readCookieConsent,
  resetCookieConsentGate,
  runWhenCookieConsented,
} from '~/utils/cookieConsent'

describe('consenso cookie', () => {
  beforeEach(() => {
    resetCookieConsentGate()
    document.cookie = `${COOKIE_CONSENT_NAME}=; Path=/; Max-Age=0`
    vi.useRealTimers()
  })

  it('blocca davvero una tecnologia non essenziale prima del consenso', () => {
    const loadAnalytics = vi.fn()

    expect(runWhenCookieConsented(loadAnalytics)).toBe(false)
    expect(loadAnalytics).not.toHaveBeenCalled()

    persistCookieConsent(createCookieConsent(true, 'it'))
    expect(loadAnalytics).toHaveBeenCalledOnce()
  })

  it('non sblocca una tecnologia non essenziale dopo il rifiuto', () => {
    const loadAnalytics = vi.fn()
    runWhenCookieConsented(loadAnalytics)

    persistCookieConsent(createCookieConsent(false, 'en'))

    expect(loadAnalytics).not.toHaveBeenCalled()
  })

  it('registra scelta, momento, versione della policy e lingua', () => {
    const consent = createCookieConsent(false, 'fr')
    persistCookieConsent(consent)

    expect(readCookieConsent(document.cookie)).toEqual(consent)
    expect(consent.policyVersion).toBe(COOKIE_POLICY_VERSION)
    expect(consent.language).toBe('fr')
    expect(consent.necessary).toBe(true)
  })

  it('richiede nuovamente la scelta dopo sei mesi', () => {
    const consent = createCookieConsent(true, 'de')
    vi.setSystemTime(Date.parse(consent.decidedAt) + COOKIE_CONSENT_MAX_AGE * 1000 + 1)

    const header = `${COOKIE_CONSENT_NAME}=${encodeURIComponent(JSON.stringify(consent))}`
    expect(readCookieConsent(header)).toBeNull()
  })

  it('rifiuta un consenso legato a una versione diversa della policy', () => {
    const consent = { ...createCookieConsent(true, 'es'), policyVersion: '0.9.0' }
    const header = `${COOKIE_CONSENT_NAME}=${encodeURIComponent(JSON.stringify(consent))}`

    expect(readCookieConsent(header)).toBeNull()
  })
})
