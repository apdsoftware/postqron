import {
  APP_SHELL_LOCALES,
  type AppShellLocale,
} from './catalogs.ts'

export type PlanCode = 'start' | 'pro' | 'team' | 'unlimited'
export type BillingInterval = 'monthly' | 'annual'
export type AppSection =
  | 'entry'
  | 'home'
  | 'onboarding'
  | 'oauth-callback'
  | 'profile'
  | 'security'
  | 'providers'
  | 'plan'
  | 'workspace'
  | 'privacy'
  | 'verify-email'

export interface PurchaseIntent {
  interval?: BillingInterval
  plan?: PlanCode
  quantity?: number
}

export class AppNavigationError extends Error {
  readonly code: 'APP_INVALID_DESTINATION' | 'APP_INVALID_PURCHASE_INTENT'

  constructor(
    code: 'APP_INVALID_DESTINATION' | 'APP_INVALID_PURCHASE_INTENT',
    message: string,
  ) {
    super(message)
    this.name = 'AppNavigationError'
    this.code = code
  }
}

const localOrigin = 'https://postqron.local'
const plans = new Set<PlanCode>(['start', 'pro', 'team', 'unlimited'])
const intervals = new Set<BillingInterval>(['monthly', 'annual'])

function hasUnsafeCharacter(value: string): boolean {
  return [...value].some((character) => {
    const point = character.codePointAt(0) ?? 0
    return character === '\\' || point <= 31 || point === 127
  })
}

function parseLocal(value: string): URL {
  if (!value.startsWith('/') || value.startsWith('//') || hasUnsafeCharacter(value)) {
    throw new AppNavigationError(
      'APP_INVALID_DESTINATION',
      'Only a safe origin-relative destination is accepted',
    )
  }
  let parsed: URL
  try {
    parsed = new URL(value, localOrigin)
  } catch {
    throw new AppNavigationError(
      'APP_INVALID_DESTINATION',
      'The destination is malformed',
    )
  }
  if (parsed.origin !== localOrigin) {
    throw new AppNavigationError(
      'APP_INVALID_DESTINATION',
      'The destination resolves outside Postqron',
    )
  }
  return parsed
}

export function localeFromAppPath(value: string): AppShellLocale {
  const pathname = parseLocal(value).pathname
  const candidate = pathname.split('/')[1]
  return APP_SHELL_LOCALES.includes(candidate as AppShellLocale)
    ? candidate as AppShellLocale
    : 'en'
}

export function appRoot(locale: AppShellLocale): string {
  return `/${locale}/app`
}

export function appRoute(
  locale: AppShellLocale,
  section: AppSection,
): string {
  switch (section) {
    case 'entry':
      return appRoot(locale)
    case 'home':
      return `${appRoot(locale)}/home`
    case 'onboarding':
      return `${appRoot(locale)}/onboarding`
    case 'oauth-callback':
      return `${appRoot(locale)}/oauth/callback`
    case 'profile':
      return `${appRoot(locale)}/profile`
    case 'security':
      return `${appRoot(locale)}/security`
    case 'providers':
      return `${appRoot(locale)}/providers`
    case 'plan':
      return `${appRoot(locale)}/plan`
    case 'workspace':
      return `${appRoot(locale)}/workspace`
    case 'privacy':
      return `${appRoot(locale)}/privacy`
    case 'verify-email':
      return `${appRoot(locale)}/verify-email`
  }
}

function unlocalizedAppPath(pathname: string): string {
  const candidate = pathname.split('/')[1]
  if (APP_SHELL_LOCALES.includes(candidate as AppShellLocale)) {
    return `/${pathname.split('/').slice(2).join('/')}`.replace(/\/+$/u, '') || '/'
  }
  return pathname.replace(/\/+$/u, '') || '/'
}

export function parsePurchaseIntent(searchParams: URLSearchParams): PurchaseIntent {
  const allowed = new Set(['plan', 'interval', 'quantity'])
  for (const key of searchParams.keys()) {
    if (!allowed.has(key)) {
      throw new AppNavigationError(
        'APP_INVALID_PURCHASE_INTENT',
        `Unsupported app parameter: ${key}`,
      )
    }
  }

  const rawPlan = searchParams.get('plan')
  const rawInterval = searchParams.get('interval')
  const rawQuantity = searchParams.get('quantity')
  if (!rawPlan && !rawInterval && !rawQuantity) {
    return {}
  }
  if (!rawPlan || !plans.has(rawPlan as PlanCode)) {
    throw new AppNavigationError(
      'APP_INVALID_PURCHASE_INTENT',
      'The public plan code is invalid',
    )
  }
  if (rawInterval && !intervals.has(rawInterval as BillingInterval)) {
    throw new AppNavigationError(
      'APP_INVALID_PURCHASE_INTENT',
      'The billing interval is invalid',
    )
  }
  if (rawPlan === 'unlimited' && rawQuantity !== null) {
    throw new AppNavigationError(
      'APP_INVALID_PURCHASE_INTENT',
      'The unlimited plan does not accept a channel quantity',
    )
  }

  let quantity: number | undefined
  if (rawQuantity !== null) {
    if (!/^[1-9][0-9]*$/u.test(rawQuantity)) {
      throw new AppNavigationError(
        'APP_INVALID_PURCHASE_INTENT',
        'The channel quantity must be a positive integer',
      )
    }
    quantity = Number(rawQuantity)
    if (!Number.isSafeInteger(quantity)) {
      throw new AppNavigationError(
        'APP_INVALID_PURCHASE_INTENT',
        'The channel quantity must be a safe integer',
      )
    }
  }

  return {
    plan: rawPlan as PlanCode,
    interval: rawInterval as BillingInterval | null ?? undefined,
    quantity,
  }
}

export function sanitizeAppDestination(value: string): string {
  const parsed = parseLocal(value)
  const path = unlocalizedAppPath(parsed.pathname)
  if (path !== '/app' && !path.startsWith('/app/')) {
    throw new AppNavigationError(
      'APP_INVALID_DESTINATION',
      'The destination is outside the product application',
    )
  }
  if (path === '/app') {
    parsePurchaseIntent(parsed.searchParams)
  }
  return `${parsed.pathname}${parsed.search}${parsed.hash}`
}

export function safeAppDestination(
  value: unknown,
  locale: AppShellLocale = 'en',
): string {
  if (typeof value !== 'string') {
    return appRoot(locale)
  }
  try {
    return sanitizeAppDestination(value)
  } catch {
    return appRoot(locale)
  }
}

export function onboardingDestination(returnTo: string): string {
  const locale = localeFromAppPath(returnTo)
  const target = new URL(sanitizeAppDestination(returnTo), localOrigin)
  const onboarding = new URL(appRoot(locale) + '/onboarding', localOrigin)
  onboarding.searchParams.set(
    'return_to',
    `${target.pathname}${target.search}${target.hash}`,
  )
  return `${onboarding.pathname}${onboarding.search}`
}

export function authenticatedDestination(
  returnTo: string,
  onboardingRequired: boolean,
): string {
  const safe = sanitizeAppDestination(returnTo)
  if (onboardingRequired) {
    return onboardingDestination(safe)
  }
  const parsed = new URL(safe, localOrigin)
  const path = unlocalizedAppPath(parsed.pathname)
  if (path === '/app') {
    const locale = localeFromAppPath(safe)
    const home = new URL(appRoute(locale, 'home'), localOrigin)
    home.search = parsed.search
    return `${home.pathname}${home.search}`
  }
  return safe
}
