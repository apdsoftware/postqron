// Client-safe view of the F5 social connections contract (#282 / PR #287).
//
// This module mirrors `features/f05-social-connections/contracts/openapi.yaml`
// and deliberately never models, reads, or surfaces token material. Parsers
// rebuild every value from an allow-list of known-safe fields, so any token,
// secret, or provider credential accidentally present in a payload is dropped
// before it can reach the browser rendering layer.

export type SocialProvider = 'facebook_pages' | 'instagram_professional'
export type SocialResourceType = 'facebook_page' | 'instagram_professional'
export type SocialAccountType = 'page' | 'business' | 'creator'
export type SocialProviderStatus = 'available' | 'unavailable'
export type SocialConnectionStatus =
  | 'connected'
  | 'reconnect_required'
  | 'revoked'
export type SocialReconnectReason =
  | 'authentication_revoked'
  | 'permission_missing'
  | 'resource_gone'
  | 'not_refreshable'

export const SOCIAL_PROVIDERS: readonly SocialProvider[] = [
  'facebook_pages',
  'instagram_professional',
]

export interface SocialProviderAvailability {
  provider: SocialProvider
  status: SocialProviderStatus
  retryable: boolean
}

export interface SocialBootstrap {
  providers: SocialProviderAvailability[]
}

export interface SocialAuthorization {
  authorization_url: string
  expires_at: string
}

export interface SocialCandidate {
  remote_id: string
  resource_type: SocialResourceType
  account_type: SocialAccountType
  display_name: string
  handle?: string
  picture_url?: string
  scopes: string[]
}

export interface SocialSelection {
  selection_id: string
  provider: SocialProvider
  resources: SocialCandidate[]
  expires_at: string
}

export interface SocialConnection {
  id: string
  provider: SocialProvider
  remote_id: string
  resource_type: SocialResourceType
  account_type: SocialAccountType
  display_name: string
  handle?: string
  picture_url?: string
  scopes: string[]
  status: SocialConnectionStatus
  reconnect_reason?: SocialReconnectReason
  last_verified_at?: string
  created_at: string
  updated_at: string
  revoked_at?: string
}

export interface SocialRevocation {
  connection: SocialConnection
  provider_revoked: boolean
}

const providerValues = new Set<SocialProvider>(SOCIAL_PROVIDERS)
const resourceTypes = new Set<SocialResourceType>([
  'facebook_page',
  'instagram_professional',
])
const accountTypes = new Set<SocialAccountType>(['page', 'business', 'creator'])
const connectionStatuses = new Set<SocialConnectionStatus>([
  'connected',
  'reconnect_required',
  'revoked',
])
const reconnectReasons = new Set<SocialReconnectReason>([
  'authentication_revoked',
  'permission_missing',
  'resource_gone',
  'not_refreshable',
])

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function text(value: unknown, code: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new Error(code)
  }
  return value
}

function optionalText(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value : undefined
}

function isoDateTime(value: unknown, code: string): string {
  const resolved = text(value, code)
  if (Number.isNaN(Date.parse(resolved))) {
    throw new Error(code)
  }
  return resolved
}

function optionalIsoDateTime(value: unknown, code: string): string | undefined {
  if (value === undefined || value === null) {
    return undefined
  }
  return isoDateTime(value, code)
}

// Only an https:// picture URL is surfaced; anything else is dropped so the
// browser never renders an attacker-controlled or credential-bearing URL.
function safePictureURL(value: unknown): string | undefined {
  const candidate = optionalText(value)
  if (!candidate) {
    return undefined
  }
  try {
    const parsed = new URL(candidate)
    return parsed.protocol === 'https:' ? parsed.href : undefined
  } catch {
    return undefined
  }
}

function scopes(value: unknown, code: string): string[] {
  if (!Array.isArray(value) || value.some(scope => typeof scope !== 'string')) {
    throw new Error(code)
  }
  return value.map(String)
}

const INVALID_BOOTSTRAP = 'SOCIAL_INVALID_BOOTSTRAP_PAYLOAD'
const INVALID_AUTHORIZATION = 'SOCIAL_INVALID_AUTHORIZATION_PAYLOAD'
const INVALID_SELECTION = 'SOCIAL_INVALID_SELECTION_PAYLOAD'
const INVALID_CONNECTION = 'SOCIAL_INVALID_CONNECTION_PAYLOAD'
const INVALID_REVOCATION = 'SOCIAL_INVALID_REVOCATION_PAYLOAD'

export function parseSocialBootstrap(value: unknown): SocialBootstrap {
  if (!isRecord(value) || !Array.isArray(value.providers)) {
    throw new Error(INVALID_BOOTSTRAP)
  }
  const providers = value.providers.map((entry): SocialProviderAvailability => {
    if (!isRecord(entry)
      || !providerValues.has(entry.provider as SocialProvider)
      || (entry.status !== 'available' && entry.status !== 'unavailable')
      || typeof entry.retryable !== 'boolean') {
      throw new Error(INVALID_BOOTSTRAP)
    }
    return {
      provider: entry.provider as SocialProvider,
      status: entry.status,
      retryable: entry.retryable,
    }
  })
  if (providers.length === 0) {
    throw new Error(INVALID_BOOTSTRAP)
  }
  return { providers }
}

export function parseSocialAuthorization(value: unknown): SocialAuthorization {
  if (!isRecord(value)) {
    throw new Error(INVALID_AUTHORIZATION)
  }
  const authorizationURL = text(value.authorization_url, INVALID_AUTHORIZATION)
  let parsed: URL
  try {
    parsed = new URL(authorizationURL)
  } catch {
    throw new Error(INVALID_AUTHORIZATION)
  }
  if (parsed.protocol !== 'https:') {
    throw new Error(INVALID_AUTHORIZATION)
  }
  return {
    authorization_url: parsed.href,
    expires_at: isoDateTime(value.expires_at, INVALID_AUTHORIZATION),
  }
}

function parseSocialCandidate(value: unknown): SocialCandidate {
  if (!isRecord(value)
    || !resourceTypes.has(value.resource_type as SocialResourceType)
    || !accountTypes.has(value.account_type as SocialAccountType)) {
    throw new Error(INVALID_SELECTION)
  }
  return {
    remote_id: text(value.remote_id, INVALID_SELECTION),
    resource_type: value.resource_type as SocialResourceType,
    account_type: value.account_type as SocialAccountType,
    display_name: text(value.display_name, INVALID_SELECTION),
    handle: optionalText(value.handle),
    picture_url: safePictureURL(value.picture_url),
    scopes: scopes(value.scopes, INVALID_SELECTION),
  }
}

export function parseSocialSelection(value: unknown): SocialSelection {
  if (!isRecord(value)
    || !providerValues.has(value.provider as SocialProvider)
    || !Array.isArray(value.resources)
    || value.resources.length === 0) {
    throw new Error(INVALID_SELECTION)
  }
  return {
    selection_id: text(value.selection_id, INVALID_SELECTION),
    provider: value.provider as SocialProvider,
    resources: value.resources.map(parseSocialCandidate),
    expires_at: isoDateTime(value.expires_at, INVALID_SELECTION),
  }
}

export function parseSocialConnection(value: unknown): SocialConnection {
  if (!isRecord(value)
    || !providerValues.has(value.provider as SocialProvider)
    || !resourceTypes.has(value.resource_type as SocialResourceType)
    || !accountTypes.has(value.account_type as SocialAccountType)
    || !connectionStatuses.has(value.status as SocialConnectionStatus)) {
    throw new Error(INVALID_CONNECTION)
  }
  const reason = optionalText(value.reconnect_reason)
  return {
    id: text(value.id, INVALID_CONNECTION),
    provider: value.provider as SocialProvider,
    remote_id: text(value.remote_id, INVALID_CONNECTION),
    resource_type: value.resource_type as SocialResourceType,
    account_type: value.account_type as SocialAccountType,
    display_name: text(value.display_name, INVALID_CONNECTION),
    handle: optionalText(value.handle),
    picture_url: safePictureURL(value.picture_url),
    scopes: scopes(value.scopes, INVALID_CONNECTION),
    status: value.status as SocialConnectionStatus,
    reconnect_reason: reason && reconnectReasons.has(reason as SocialReconnectReason)
      ? reason as SocialReconnectReason
      : undefined,
    last_verified_at: optionalIsoDateTime(value.last_verified_at, INVALID_CONNECTION),
    created_at: isoDateTime(value.created_at, INVALID_CONNECTION),
    updated_at: isoDateTime(value.updated_at, INVALID_CONNECTION),
    revoked_at: optionalIsoDateTime(value.revoked_at, INVALID_CONNECTION),
  }
}

export function parseSocialConnections(value: unknown): SocialConnection[] {
  if (!isRecord(value) || !Array.isArray(value.connections)) {
    throw new Error(INVALID_CONNECTION)
  }
  return value.connections.map(parseSocialConnection)
}

export function parseSocialRevocation(value: unknown): SocialRevocation {
  if (!isRecord(value) || typeof value.provider_revoked !== 'boolean') {
    throw new Error(INVALID_REVOCATION)
  }
  return {
    connection: parseSocialConnection(value.connection),
    provider_revoked: value.provider_revoked,
  }
}
