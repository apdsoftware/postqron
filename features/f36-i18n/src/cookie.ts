export const LOCALE_COOKIE_CONTRACT = Object.freeze({
  name: 'postqron_locale',
  classification: 'necessary_functional',
  purpose: 'Remember the language explicitly selected by the user',
  consentRequired: false,
  containsPersonalData: false,
  path: '/',
  sameSite: 'lax',
  secureInProduction: true,
  httpOnly: false,
  maxAgeSeconds: 60 * 60 * 24 * 365,
} as const)

export type LocaleCookieContract = typeof LOCALE_COOKIE_CONTRACT
