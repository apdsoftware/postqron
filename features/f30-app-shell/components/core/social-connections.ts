// Client-safe mirror of the authoritative F5 contract from PRs #315/#321.
// Parsers rebuild responses from an allow-list, so credentials and unexpected
// provider fields can never reach the rendering layer.

export const SOCIAL_PROVIDERS = [
  'facebook_pages',
  'facebook_groups',
  'instagram_professional',
  'instagram_personal',
  'x',
  'linkedin',
  'pinterest',
  'tiktok',
  'google_business_profile',
  'mastodon',
  'youtube',
  'threads',
  'bluesky',
] as const

export type SocialProvider = typeof SOCIAL_PROVIDERS[number]
export type SocialProviderStatus = 'available' | 'unavailable'
export type SocialConfigurationState =
  | 'not_configured'
  | 'review_required'
  | 'audit_required'
  | 'ready'
export type SocialPublishingMode = 'auto' | 'notification'
export type SocialDiscoveryInputKind =
  | 'instance_origin'
  | 'handle'
  | 'did'
  | 'pds_origin'
export type SocialResourceType =
  | 'facebook_page'
  | 'facebook_group'
  | 'instagram_professional'
  | 'instagram_personal'
  | 'x_profile'
  | 'linkedin_profile'
  | 'linkedin_page'
  | 'pinterest_board'
  | 'tiktok_profile'
  | 'google_business_profile_location'
  | 'mastodon_account'
  | 'youtube_channel'
  | 'threads_profile'
  | 'bluesky_account'
export type SocialAccountType =
  | 'page'
  | 'group'
  | 'business'
  | 'creator'
  | 'personal'
  | 'profile'
  | 'organization'
  | 'board'
  | 'location'
  | 'channel'
export type SocialConnectionStatus =
  | 'connected'
  | 'reconnect_required'
  | 'revoked'
export type SocialReconnectReason =
  | 'authentication_revoked'
  | 'permission_missing'
  | 'resource_gone'
  | 'not_refreshable'

export interface SocialProviderAvailability {
  provider: SocialProvider
  status: SocialProviderStatus
  configuration_state: SocialConfigurationState
  retryable: boolean
}

export interface SocialAdapterCapabilities {
  authorization: boolean
  authenticated_http: boolean
  access_token_hash: boolean
  dpop: boolean
  dynamic_discovery: boolean
  par: boolean
  pkce: boolean
  resource_selection: boolean
  token_refresh: boolean
  remote_revocation: boolean
}

export interface SocialDiscoveryInput {
  kind: SocialDiscoveryInputKind
  value: string
}

export interface SocialResourceCapability {
  resource_type: SocialResourceType
  account_types: SocialAccountType[]
  publishing_modes: SocialPublishingMode[]
}

export interface SocialProviderCatalogEntry extends SocialProviderAvailability {
  resources: SocialResourceCapability[]
  capabilities: SocialAdapterCapabilities
}

export interface SocialBootstrap {
  catalog_version: string
  providers: SocialProviderAvailability[]
  catalog: SocialProviderCatalogEntry[]
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

const providerValues = new Set<string>(SOCIAL_PROVIDERS)
const resourceTypes = new Set<SocialResourceType>([
  'facebook_page',
  'facebook_group',
  'instagram_professional',
  'instagram_personal',
  'x_profile',
  'linkedin_profile',
  'linkedin_page',
  'pinterest_board',
  'tiktok_profile',
  'google_business_profile_location',
  'mastodon_account',
  'youtube_channel',
  'threads_profile',
  'bluesky_account',
])
const accountTypes = new Set<SocialAccountType>([
  'page',
  'group',
  'business',
  'creator',
  'personal',
  'profile',
  'organization',
  'board',
  'location',
  'channel',
])
const configurationStates = new Set<SocialConfigurationState>([
  'not_configured',
  'review_required',
  'audit_required',
  'ready',
])
const publishingModes = new Set<SocialPublishingMode>(['auto', 'notification'])
const discoveryKinds = new Set<SocialDiscoveryInputKind>([
  'instance_origin',
  'handle',
  'did',
  'pds_origin',
])
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

function stringList(value: unknown, code: string): string[] {
  if (!Array.isArray(value) || value.some(item => typeof item !== 'string')) {
    throw new Error(code)
  }
  return value.map(String)
}

function enumList<T extends string>(
  value: unknown,
  allowed: ReadonlySet<T>,
  code: string,
): T[] {
  if (!Array.isArray(value)
    || value.length === 0
    || value.some(item => !allowed.has(item as T))) {
    throw new Error(code)
  }
  return value as T[]
}

const INVALID_BOOTSTRAP = 'SOCIAL_INVALID_BOOTSTRAP_PAYLOAD'
const INVALID_AUTHORIZATION = 'SOCIAL_INVALID_AUTHORIZATION_PAYLOAD'
const INVALID_SELECTION = 'SOCIAL_INVALID_SELECTION_PAYLOAD'
const INVALID_CONNECTION = 'SOCIAL_INVALID_CONNECTION_PAYLOAD'
const INVALID_REVOCATION = 'SOCIAL_INVALID_REVOCATION_PAYLOAD'

function parseAvailability(
  value: unknown,
  code = INVALID_BOOTSTRAP,
): SocialProviderAvailability {
  if (!isRecord(value)
    || !providerValues.has(String(value.provider))
    || (value.status !== 'available' && value.status !== 'unavailable')
    || !configurationStates.has(value.configuration_state as SocialConfigurationState)
    || typeof value.retryable !== 'boolean') {
    throw new Error(code)
  }
  return {
    provider: value.provider as SocialProvider,
    status: value.status,
    configuration_state: value.configuration_state as SocialConfigurationState,
    retryable: value.retryable,
  }
}

function parseResourceCapability(value: unknown): SocialResourceCapability {
  if (!isRecord(value)
    || !resourceTypes.has(value.resource_type as SocialResourceType)) {
    throw new Error(INVALID_BOOTSTRAP)
  }
  return {
    resource_type: value.resource_type as SocialResourceType,
    account_types: enumList(value.account_types, accountTypes, INVALID_BOOTSTRAP),
    publishing_modes: enumList(
      value.publishing_modes,
      publishingModes,
      INVALID_BOOTSTRAP,
    ),
  }
}

function parseAdapterCapabilities(value: unknown): SocialAdapterCapabilities {
  if (!isRecord(value)) {
    throw new Error(INVALID_BOOTSTRAP)
  }
  const names = [
    'authorization',
    'authenticated_http',
    'access_token_hash',
    'dpop',
    'dynamic_discovery',
    'par',
    'pkce',
    'resource_selection',
    'token_refresh',
    'remote_revocation',
  ] as const
  if (names.some(name => typeof value[name] !== 'boolean')) {
    throw new Error(INVALID_BOOTSTRAP)
  }
  return {
    authorization: value.authorization as boolean,
    authenticated_http: value.authenticated_http as boolean,
    access_token_hash: value.access_token_hash as boolean,
    dpop: value.dpop as boolean,
    dynamic_discovery: value.dynamic_discovery as boolean,
    par: value.par as boolean,
    pkce: value.pkce as boolean,
    resource_selection: value.resource_selection as boolean,
    token_refresh: value.token_refresh as boolean,
    remote_revocation: value.remote_revocation as boolean,
  }
}

function parseCatalogEntry(value: unknown): SocialProviderCatalogEntry {
  if (!isRecord(value) || !Array.isArray(value.resources) || value.resources.length === 0) {
    throw new Error(INVALID_BOOTSTRAP)
  }
  return {
    ...parseAvailability(value),
    resources: value.resources.map(parseResourceCapability),
    capabilities: parseAdapterCapabilities(value.capabilities),
  }
}

export function parseSocialBootstrap(value: unknown): SocialBootstrap {
  if (!isRecord(value)
    || !Array.isArray(value.providers)
    || !Array.isArray(value.catalog)
    || value.catalog.length !== SOCIAL_PROVIDERS.length) {
    throw new Error(INVALID_BOOTSTRAP)
  }
  const catalog = value.catalog.map(parseCatalogEntry)
  if (new Set(catalog.map(entry => entry.provider)).size !== SOCIAL_PROVIDERS.length) {
    throw new Error(INVALID_BOOTSTRAP)
  }
  return {
    catalog_version: text(value.catalog_version, INVALID_BOOTSTRAP),
    providers: value.providers.map(entry => parseAvailability(entry)),
    catalog,
  }
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
    scopes: stringList(value.scopes, INVALID_SELECTION),
  }
}

export function parseSocialSelection(value: unknown): SocialSelection {
  if (!isRecord(value)
    || !providerValues.has(String(value.provider))
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

export function parseSocialDiscoveryInput(value: unknown): SocialDiscoveryInput {
  if (!isRecord(value)
    || !discoveryKinds.has(value.kind as SocialDiscoveryInputKind)) {
    throw new Error(INVALID_AUTHORIZATION)
  }
  return {
    kind: value.kind as SocialDiscoveryInputKind,
    value: text(value.value, INVALID_AUTHORIZATION),
  }
}

export function parseSocialConnection(value: unknown): SocialConnection {
  if (!isRecord(value)
    || !providerValues.has(String(value.provider))
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
    scopes: stringList(value.scopes, INVALID_CONNECTION),
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

export function publishingModesForConnection(
  bootstrap: SocialBootstrap,
  connection: SocialConnection,
): SocialPublishingMode[] {
  return bootstrap.catalog
    .find(entry => entry.provider === connection.provider)
    ?.resources
    .find(resource => resource.resource_type === connection.resource_type)
    ?.publishing_modes ?? []
}
