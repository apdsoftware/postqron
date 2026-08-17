import type { LocaleCode } from '~/utils/locale'
import { isLocaleCode } from '~/utils/locale'

export const COOKIE_POLICY_VERSION = '1.0.0'
export const COOKIE_CONSENT_NAME = 'postqron_cookie_consent'
export const COOKIE_CONSENT_MAX_AGE = 60 * 60 * 24 * 183

export interface CookieConsentRecord {
  necessary: true
  nonEssential: boolean
  decidedAt: string
  policyVersion: string
  language: LocaleCode
}

type NonEssentialTask = () => void
const pendingTasks = new Set<NonEssentialTask>()

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

export function persistCookieConsent(record: CookieConsentRecord): void {
  const secure = window.location.protocol === 'https:' ? '; Secure' : ''
  document.cookie = `${COOKIE_CONSENT_NAME}=${encodeURIComponent(JSON.stringify(record))}; Path=/; Max-Age=${COOKIE_CONSENT_MAX_AGE}; SameSite=Lax${secure}`
  if (record.nonEssential) flushNonEssentialTasks()
}

/**
 * Unico ingresso per tecnologie non essenziali: il callback non viene eseguito
 * finché il consenso valido non è già presente, e resta in attesa se manca.
 */
export function runWhenCookieConsented(task: NonEssentialTask): boolean {
  if (typeof document !== 'undefined' && readCookieConsent(document.cookie)?.nonEssential) {
    task()
    return true
  }

  pendingTasks.add(task)
  return false
}

function flushNonEssentialTasks(): void {
  for (const task of pendingTasks) task()
  pendingTasks.clear()
}

/** Solo per isolare lo stato globale fra test. */
export function resetCookieConsentGate(): void {
  pendingTasks.clear()
}
