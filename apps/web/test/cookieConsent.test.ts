import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  COOKIE_CATEGORIES,
  COOKIE_CONSENT_MAX_AGE,
  COOKIE_CONSENT_NAME,
  COOKIE_POLICY_VERSION,
  blockedCookieTechnologies,
  createCookieConsent,
  isCookieCategoryAllowed,
  persistCookieConsent,
  readCookieConsent,
  registerCookieTechnology,
  registeredCookieTechnologies,
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

/**
 * Il registro è il modo in cui il blocco preventivo della policy §3 smette di
 * essere una promessa: una tecnologia dichiara la propria categoria e non parte
 * finché quella categoria non ha il consenso. Oggi non c'è niente da bloccare
 * (§2.2: «we use **no** analytics») ed è proprio per questo che l'impianto deve
 * esistere prima — #477 dovrà aggiungere una voce, non rifare il meccanismo.
 */
describe('registro delle tecnologie', () => {
  beforeEach(() => {
    resetCookieConsentGate()
    document.cookie = `${COOKIE_CONSENT_NAME}=; Path=/; Max-Age=0`
    vi.useRealTimers()
  })

  it('conosce solo le categorie nominate dalla policy', () => {
    expect([...COOKIE_CATEGORIES]).toEqual(['necessary', 'analytics', 'advertising', 'profiling', 'social'])
  })

  it('lascia partire subito ciò che è strettamente necessario', () => {
    const start = vi.fn()

    expect(registerCookieTechnology({ id: 'csrf', category: 'necessary', start })).toBe(true)
    expect(start).toHaveBeenCalledOnce()
    expect(blockedCookieTechnologies()).toEqual([])
  })

  it.each(COOKIE_CATEGORIES.filter(category => category !== 'necessary'))(
    'tiene ferma la categoria %s finché il consenso non arriva',
    (category) => {
      const start = vi.fn()
      registerCookieTechnology({ id: `tech-${category}`, category, start })

      expect(start).not.toHaveBeenCalled()
      expect(blockedCookieTechnologies().map(technology => technology.id)).toEqual([`tech-${category}`])

      persistCookieConsent(createCookieConsent(true, 'it'))
      expect(start).toHaveBeenCalledOnce()
      expect(blockedCookieTechnologies()).toEqual([])
    },
  )

  /*
   * La forma che #477 avrà: una voce con un identificatore e una categoria.
   * Nulla di questo test conosce il meccanismo del consenso, ed è il punto.
   */
  it('aggiunge una tecnologia nuova senza toccare il meccanismo', () => {
    const analytics = vi.fn()
    const session = vi.fn()

    registerCookieTechnology({ id: 'plausible', category: 'analytics', start: analytics })
    registerCookieTechnology({ id: 'session', category: 'necessary', start: session })

    expect(session).toHaveBeenCalledOnce()
    expect(analytics).not.toHaveBeenCalled()
    expect(registeredCookieTechnologies().map(technology => technology.id)).toEqual(['plausible', 'session'])
  })

  it('non fa partire due volte la stessa tecnologia', () => {
    const start = vi.fn()
    registerCookieTechnology({ id: 'plausible', category: 'analytics', start })

    persistCookieConsent(createCookieConsent(true, 'de'))
    persistCookieConsent(createCookieConsent(true, 'de'))

    expect(start).toHaveBeenCalledOnce()
  })

  it('richiede il consenso di nuovo a chi si registra dopo un rifiuto', () => {
    persistCookieConsent(createCookieConsent(false, 'fr'))

    const start = vi.fn()
    expect(registerCookieTechnology({ id: 'plausible', category: 'analytics', start })).toBe(false)
    expect(start).not.toHaveBeenCalled()
  })

  /*
   * Il legame con la versione non è una regola a parte: un consenso che
   * `readCookieConsent` scarta non sblocca niente, esattamente come un consenso
   * che non c'è. Senza questo, la §2.2 — nuova versione quando arrivano gli
   * analytics — resterebbe una formalità.
   */
  it('non sblocca nulla con un consenso legato a un\'altra versione della policy', () => {
    const stale = { ...createCookieConsent(true, 'es'), policyVersion: '0.9.0' }
    document.cookie = `${COOKIE_CONSENT_NAME}=${encodeURIComponent(JSON.stringify(stale))}; Path=/`

    const start = vi.fn()
    registerCookieTechnology({ id: 'plausible', category: 'analytics', start })

    expect(isCookieCategoryAllowed('analytics')).toBe(false)
    expect(isCookieCategoryAllowed('necessary')).toBe(true)
    expect(start).not.toHaveBeenCalled()
  })

  it('la scorciatoia senza nome dichiara comunque una categoria', () => {
    const start = vi.fn()
    runWhenCookieConsented(start, 'social')

    expect(registeredCookieTechnologies().map(technology => technology.category)).toEqual(['social'])
    expect(start).not.toHaveBeenCalled()
  })
})
