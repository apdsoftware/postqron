export const DEFAULT_SUPPORT_EMAIL = 'help@postqron.com'
export const SUPPORT_RESPONSE_BUSINESS_DAYS = 1 as const

export class SupportContactConfigError extends Error {
  readonly code = 'SUPPORT_CONTACT_INVALID_EMAIL'

  constructor(message: string) {
    super(message)
    this.name = 'SupportContactConfigError'
  }
}

export interface SupportContactConfig {
  readonly email: string
  readonly responseBusinessDays: typeof SUPPORT_RESPONSE_BUSINESS_DAYS
}

function isValidLocalPart(value: string): boolean {
  return value.length >= 1
    && value.length <= 64
    && !value.startsWith('.')
    && !value.endsWith('.')
    && !value.includes('..')
    && /^[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+$/u.test(value)
}

function isValidDomain(value: string): boolean {
  if (value.length > 253 || !value.includes('.')) {
    return false
  }
  return value.split('.').every(label =>
    label.length >= 1
    && label.length <= 63
    && /^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$/u.test(label))
}

export function isSupportEmail(value: string): boolean {
  const hasControlCharacter = [...value].some((character) => {
    const codePoint = character.codePointAt(0) ?? 0
    return codePoint <= 31 || codePoint === 127
  })
  if (
    value.length > 254
    || value !== value.trim()
    || hasControlCharacter
  ) {
    return false
  }
  const parts = value.split('@')
  return parts.length === 2
    && isValidLocalPart(parts[0]!)
    && isValidDomain(parts[1]!)
}

export function resolveSupportContactConfig(
  configuredEmail?: unknown,
): Readonly<SupportContactConfig> {
  const email = configuredEmail === undefined
    || (typeof configuredEmail === 'string' && configuredEmail.trim() === '')
    ? DEFAULT_SUPPORT_EMAIL
    : configuredEmail

  if (typeof email !== 'string' || !isSupportEmail(email)) {
    throw new SupportContactConfigError(
      'NUXT_PUBLIC_SUPPORT_EMAIL must be a plain, valid email address',
    )
  }

  return Object.freeze({
    email,
    responseBusinessDays: SUPPORT_RESPONSE_BUSINESS_DAYS,
  })
}

export function supportMailto(email: string): `mailto:${string}` {
  if (!isSupportEmail(email)) {
    throw new SupportContactConfigError(
      'A mailto link requires a valid support email address',
    )
  }
  return `mailto:${email}`
}
