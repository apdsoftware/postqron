import {
  SUPPORTED_LOCALES,
  type Locale,
} from '../../f36-i18n/src/locales.ts'
import { localizeUrl } from '../../f36-i18n/src/routing.ts'

export type PrelaunchRouteAction =
  | { readonly action: 'allow' }
  | { readonly action: 'redirect', readonly location: string }

const localizedPrefix = new RegExp(
  `^/(?:${SUPPORTED_LOCALES.join('|')})(?=/|$)`,
  'u',
)

const exactAllowedPaths = new Set([
  '/app',
  '/app/oauth/callback',
  '/contatti',
  '/prelaunch',
  '/prelaunch/access',
])

// Each entry is an explicit, individually justified API surface — never a
// blanket "/api" wildcard. A wildcard would silently exempt any future
// same-origin product API (e.g. publishing, social connections, billing)
// from the pre-launch gate the moment it exists, which violates the
// requirement that only necessary APIs stay reachable, explicitly.
const allowedPrefixes = [
  '/admin',
  '/api/cookie-preferences',
  '/api/features',
  '/api/health',
  '/api/legal',
  '/api/v1/admin',
  '/api/v1/prelaunch',
  '/brand',
  '/healthz',
  '/legal',
  '/manifest.webmanifest',
  '/pwa',
  '/readyz',
  '/robots.txt',
  '/service-worker.js',
  '/sitemap.xml',
  '/_nuxt',
]

function pathname(value: string): string {
  try {
    return new URL(value, 'https://postqron.invalid').pathname
  } catch {
    return '/'
  }
}

export function unlocalizedPath(value: string): string {
  const path = pathname(value).replace(localizedPrefix, '')
  return path === '' ? '/' : path
}

export function isPrelaunchPath(value: string): boolean {
  const path = unlocalizedPath(value)
  return path === '/prelaunch' || path.startsWith('/prelaunch/')
}

export function isExplicitlyAllowedPath(value: string): boolean {
  const path = unlocalizedPath(value)
  if (exactAllowedPaths.has(path)) {
    return true
  }
  return allowedPrefixes.some(prefix =>
    path === prefix || path.startsWith(`${prefix}/`))
}

export function shouldNoIndex(value: string): boolean {
  const path = unlocalizedPath(value)
  return path === '/prelaunch/access'
    || path.startsWith('/app')
    || path.startsWith('/admin')
}

export function prelaunchRouteDecision(input: {
  readonly enabled: boolean
  readonly locale: Locale
  readonly url: string
}): PrelaunchRouteAction {
  if (input.enabled) {
    if (isExplicitlyAllowedPath(input.url)) {
      return { action: 'allow' }
    }
    return {
      action: 'redirect',
      location: localizeUrl(input.locale, '/prelaunch'),
    }
  }

  if (isPrelaunchPath(input.url)) {
    return { action: 'redirect', location: '/app' }
  }
  return { action: 'allow' }
}
