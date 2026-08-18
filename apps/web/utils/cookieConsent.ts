import type { LocaleCode } from '~/utils/locale'
import { LEGAL_VERSIONS } from '#legal-versions'
import { isLocaleCode } from '~/utils/locale'

/**
 * La versione arriva dal front matter di `legal/en/cookie-policy.md`, letto in
 * fase di build da `modules/legal-versions.ts`. Non è una costante ricopiata a
 * mano: le quattro versioni dei documenti sono già diverse fra loro e cambiano
 * una alla volta, e la cookie policy §2.2 promette una versione nuova quando
 * arriveranno gli analytics. Un consenso raccolto sulla 1.0.0 non è un consenso
 * alla versione che elenca gli analytics, e `readCookieConsent` lo scarta.
 */
export const COOKIE_POLICY_VERSION = LEGAL_VERSIONS['cookie-policy']

export const COOKIE_CONSENT_NAME = 'postqron_cookie_consent'

/** Sei mesi, dopo i quali la policy §4 dice che la scelta si chiede di nuovo. */
export const COOKIE_CONSENT_MAX_AGE = 60 * 60 * 24 * 183

/**
 * Le categorie sono il vocabolario della cookie policy: §2.1 «strictly
 * necessary» e l'elenco di §2.2, che nomina analytics, advertising, profiling e
 * social media come le cose che oggi *non* usiamo.
 *
 * Sono dichiarate tutte anche se oggi ne serve una sola perché il punto del
 * registro è che aggiungere una tecnologia sia una riga, non un meccanismo
 * nuovo: quando arriveranno gli analytics (#477) la voce esiste già.
 */
export const COOKIE_CATEGORIES = ['necessary', 'analytics', 'advertising', 'profiling', 'social'] as const

export type CookieCategory = (typeof COOKIE_CATEGORIES)[number]

/**
 * Solo la categoria «necessary» non richiede consenso (policy §2.1). Tutto il
 * resto è bloccato finché non arriva, e §2.2 è esplicita sul perché gli
 * analytics non staranno mai fra le necessarie: misurare come un sito viene
 * usato non serve a farlo funzionare.
 */
export function requiresCookieConsent(category: CookieCategory): boolean {
  return category !== 'necessary'
}

export interface CookieConsentRecord {
  necessary: true
  nonEssential: boolean
  decidedAt: string
  policyVersion: string
  language: LocaleCode
}

/** Una tecnologia che dichiara la propria categoria e attende il proprio turno. */
export interface CookieTechnology {
  /** Identificatore stabile: è con questo che la policy la nomina. */
  id: string
  category: CookieCategory
  /** Eseguita al più una volta, e solo se la categoria ha il consenso. */
  start: () => void
}

const registry = new Map<string, CookieTechnology>()
const started = new Set<string>()
let anonymousCount = 0

export function readCookieConsent(cookieHeader: string): CookieConsentRecord | null {
  const encoded = cookieHeader.split(';').map(part => part.trim()).find(part => part.startsWith(`${COOKIE_CONSENT_NAME}=`))?.slice(COOKIE_CONSENT_NAME.length + 1)
  if (!encoded) return null

  try {
    const value = JSON.parse(decodeURIComponent(encoded)) as Partial<CookieConsentRecord>
    const decidedAt = Date.parse(value.decidedAt ?? '')
    const expiresAt = decidedAt + COOKIE_CONSENT_MAX_AGE * 1000
    if (
      value.necessary !== true
      || typeof value.nonEssential !== 'boolean'
      || value.policyVersion !== COOKIE_POLICY_VERSION
      || !Number.isFinite(decidedAt)
      || expiresAt <= Date.now()
      || !isLocaleCode(value.language)
    ) return null

    return value as CookieConsentRecord
  }
  catch {
    return null
  }
}

export function createCookieConsent(nonEssential: boolean, language: LocaleCode): CookieConsentRecord {
  return {
    necessary: true,
    nonEssential,
    decidedAt: new Date().toISOString(),
    policyVersion: COOKIE_POLICY_VERSION,
    language,
  }
}

/** La scelta corrente, o `null` se non c'è, è scaduta o è legata a un'altra versione. */
export function currentCookieConsent(): CookieConsentRecord | null {
  return typeof document === 'undefined' ? null : readCookieConsent(document.cookie)
}

/**
 * Se una categoria può partire *adesso*. La risposta per «necessary» è sempre
 * sì; per tutte le altre è no finché il consenso non è registrato, valido e
 * legato a questa versione della policy.
 */
export function isCookieCategoryAllowed(category: CookieCategory, consent = currentCookieConsent()): boolean {
  return !requiresCookieConsent(category) || consent?.nonEssential === true
}

export function persistCookieConsent(record: CookieConsentRecord): void {
  const secure = window.location.protocol === 'https:' ? '; Secure' : ''
  document.cookie = `${COOKIE_CONSENT_NAME}=${encodeURIComponent(JSON.stringify(record))}; Path=/; Max-Age=${COOKIE_CONSENT_MAX_AGE}; SameSite=Lax${secure}`
  startAllowedTechnologies(record)
}

/**
 * Unico ingresso per qualunque tecnologia: dichiara la propria categoria e resta
 * nel registro finché quella categoria non ha il consenso. Chi vuole aggiungerne
 * una — gli analytics di #477, per dire — scrive una voce qui e non tocca nulla
 * di questo file.
 *
 * Restituisce `true` se è partita subito. Durante la generazione statica non
 * parte nulla e non si accumula nulla: non c'è un visitatore a cui chiedere, e
 * il registro è un modulo globale che sopravviverebbe alle altre pagine.
 */
export function registerCookieTechnology(technology: CookieTechnology): boolean {
  if (typeof document === 'undefined') return false

  registry.set(technology.id, technology)

  if (!isCookieCategoryAllowed(technology.category)) return false

  runOnce(technology)
  return true
}

/**
 * Scorciatoia con cui si registra una tecnologia senza nome proprio. La
 * categoria va dichiarata comunque: è il punto del registro.
 */
export function runWhenCookieConsented(start: () => void, category: CookieCategory = 'analytics'): boolean {
  return registerCookieTechnology({ id: `anonymous-${anonymousCount++}`, category, start })
}

/** Le tecnologie dichiarate, con la categoria da cui dipendono. */
export function registeredCookieTechnologies(): readonly CookieTechnology[] {
  return [...registry.values()]
}

/**
 * Le tecnologie ancora ferme in attesa del consenso. Serve a rendere il blocco
 * preventivo osservabile invece che dichiarato.
 */
export function blockedCookieTechnologies(): readonly CookieTechnology[] {
  return registeredCookieTechnologies().filter(technology => !started.has(technology.id))
}

function startAllowedTechnologies(consent: CookieConsentRecord | null): void {
  for (const technology of registry.values()) {
    if (isCookieCategoryAllowed(technology.category, consent)) runOnce(technology)
  }
}

function runOnce(technology: CookieTechnology): void {
  if (started.has(technology.id)) return

  started.add(technology.id)
  technology.start()
}

/** Solo per isolare lo stato globale fra test. */
export function resetCookieConsentGate(): void {
  registry.clear()
  started.clear()
  anonymousCount = 0
}
