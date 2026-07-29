import type { AppShellLocale } from './catalogs.ts'

export type OAuthProvider = 'google' | 'apple' | 'facebook' | 'linkedin'
export type AccountProviderKind = 'identity' | 'social'
export type ExportScope = 'account' | 'workspace'
export type ExportStatus = 'queued' | 'ready' | 'failed' | 'expired'
export type DeletionScope = 'account' | 'workspace'
export type DeletionStatus =
  | 'deactivating'
  | 'grace_period'
  | 'finalizing'
  | 'completed'
  | 'cancelled'
  | 'deactivation_failed'
  | 'finalization_failed'

export interface LegalDocument {
  digest_sha256: string
  href: string
  key: 'terms' | 'privacy'
  version: string
}

export interface WorkspaceSummary {
  id: string
  name: string
  role: 'owner' | 'member'
}

export interface AppSession {
  account: {
    display_name: string
    email?: string
    email_verified: boolean
    id: string
    locale: AppShellLocale
  }
  authenticated_at: string
  current_workspace?: WorkspaceSummary
  onboarding_required: boolean
  workspaces: WorkspaceSummary[]
}

export interface AppBootstrap {
  auth_methods: Array<'password'>
  legal_documents: LegalDocument[]
  providers: OAuthProvider[]
  session?: AppSession
}

export interface ConsentReceipt {
  action: 'accepted'
  control_text_id: string
  digest_sha256: string
  document_key: 'terms' | 'privacy'
  locale: AppShellLocale
  purpose: 'contract' | 'privacy_acknowledgement'
  surface: 'app_onboarding'
  version: string
}

export interface RegistrationConsentReceipt {
  action: 'accepted' | 'acknowledged'
  control_text_id: string
  digest_sha256: string
  document_key: 'terms_it' | 'privacy_it'
  locale: 'it-IT'
  purpose: 'contract' | 'privacy_notice'
  surface: 'signup'
  version: string
}

export interface AccountProfile {
  account_id: string
  display_name: string
  locale: string
  timezone: string
  updated_at: string
}

export interface AccountProvider {
  connected_at: string
  external_label?: string
  id: string
  kind: AccountProviderKind
  name: string
  only_login_method: boolean
}

export interface WorkspacePlan {
  workspace: WorkspaceSummary
  plan: {
    code: string
    limits: Record<string, number>
    manageable: boolean
    name: string
    renews_at?: string
    state: string
    usage: Record<string, number>
  }
}

export interface AccountArea {
  profile: AccountProfile
  providers: AccountProvider[]
  workspaces: WorkspacePlan[]
}

export interface WorkspaceMember {
  account_id: string
  created_at: string
  email: string
  id: string
  invited_by_account_id?: string
  role: 'owner' | 'member'
  status: string
}

export interface ExportRequest {
  account_id: string
  expires_at: string
  id: string
  requested_at: string
  scope: ExportScope
  status: ExportStatus
  workspace_id?: string
}

export interface ExportDownload {
  expires_at: string
  sha256: string
  size_bytes: number
  url: string
}

export interface OwnershipAction {
  action: 'transfer' | 'delete'
  transfer_account_id?: string
  workspace_id: string
}

export function buildAccountDeletionOwnershipActions(
  accountArea: AccountArea | undefined,
): OwnershipAction[] {
  if (!accountArea) {
    throw new Error('APP_ACCOUNT_DELETION_OWNERSHIP_UNAVAILABLE')
  }
  const actions = accountArea.workspaces
    .filter(item => item.workspace.role === 'owner')
    .map(item => ({
      workspace_id: item.workspace.id,
      action: 'delete' as const,
    }))
  if (actions.length === 0) {
    throw new Error('APP_ACCOUNT_DELETION_OWNERSHIP_UNAVAILABLE')
  }
  return actions
}

export interface DeletionRequest {
  account_id: string
  grace_ends_at: string
  id: string
  ownership: {
    actions: OwnershipAction[]
  }
  requested_at: string
  scope: DeletionScope
  status: DeletionStatus
  workspace_id?: string
}

export interface DeletionCancelCapability {
  expires_at: string
}

export interface AppErrorPayload {
  error: {
    code: string
    message: string
    retryable: boolean
  }
}

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

function numberRecord(value: unknown, code: string): Record<string, number> {
  if (!isRecord(value)) {
    throw new Error(code)
  }
  const result: Record<string, number> = {}
  for (const [key, entry] of Object.entries(value)) {
    if (typeof entry !== 'number' || !Number.isFinite(entry)) {
      throw new Error(code)
    }
    result[key] = entry
  }
  return result
}

const providers = new Set<OAuthProvider>([
  'google',
  'apple',
  'facebook',
  'linkedin',
])
const locales = new Set<AppShellLocale>(['en', 'it', 'es', 'fr', 'de'])
const exportStatuses = new Set<ExportStatus>(['queued', 'ready', 'failed', 'expired'])
const deletionStatuses = new Set<DeletionStatus>([
  'deactivating',
  'grace_period',
  'finalizing',
  'completed',
  'cancelled',
  'deactivation_failed',
  'finalization_failed',
])

function parseWorkspace(value: unknown): WorkspaceSummary {
  if (!isRecord(value)
    || typeof value.id !== 'string'
    || typeof value.name !== 'string'
    || (value.role !== 'owner' && value.role !== 'member')) {
    throw new Error('APP_INVALID_WORKSPACE_PAYLOAD')
  }
  return { id: value.id, name: value.name, role: value.role }
}

export function parseSession(value: unknown): AppSession {
  if (!isRecord(value)
    || !isRecord(value.account)
    || typeof value.account.id !== 'string'
    || typeof value.account.display_name !== 'string'
    || typeof value.account.email_verified !== 'boolean'
    || !locales.has(value.account.locale as AppShellLocale)
    || typeof value.onboarding_required !== 'boolean'
    || !Array.isArray(value.workspaces)) {
    throw new Error('APP_INVALID_SESSION_PAYLOAD')
  }
  const workspaces = value.workspaces.map(parseWorkspace)
  const current = value.current_workspace === undefined
    ? undefined
    : parseWorkspace(value.current_workspace)
  if (current && !workspaces.some(workspace => workspace.id === current.id)) {
    throw new Error('APP_INVALID_SESSION_PAYLOAD')
  }
  return {
    account: {
      id: value.account.id,
      display_name: value.account.display_name,
      email: optionalText(value.account.email),
      email_verified: value.account.email_verified,
      locale: value.account.locale as AppShellLocale,
    },
    authenticated_at: isoDateTime(value.authenticated_at, 'APP_INVALID_SESSION_PAYLOAD'),
    current_workspace: current,
    onboarding_required: value.onboarding_required,
    workspaces,
  }
}

function parseLegalDocument(value: unknown): LegalDocument {
  if (!isRecord(value)
    || (value.key !== 'terms' && value.key !== 'privacy')
    || typeof value.version !== 'string'
    || typeof value.href !== 'string'
    || !/^\/(?:[a-z]{2}\/)?legal\//u.test(value.href)
    || typeof value.digest_sha256 !== 'string'
    || !/^[a-f0-9]{64}$/u.test(value.digest_sha256)) {
    throw new Error('APP_INVALID_LEGAL_DOCUMENT')
  }
  return {
    key: value.key,
    version: value.version,
    href: value.href,
    digest_sha256: value.digest_sha256,
  }
}

export function parseBootstrap(value: unknown): AppBootstrap {
  if (!isRecord(value)
    || !Array.isArray(value.auth_methods)
    || value.auth_methods.length !== 1
    || value.auth_methods[0] !== 'password'
    || !Array.isArray(value.providers)
    || !value.providers.every(provider => providers.has(provider))
    || !Array.isArray(value.legal_documents)) {
    throw new Error('APP_INVALID_BOOTSTRAP_PAYLOAD')
  }
  const legalDocuments = value.legal_documents.map(parseLegalDocument)
  if (
    legalDocuments.length !== 0 && (
      legalDocuments.length !== 2
      || new Set(legalDocuments.map(document => document.key)).size !== 2
    )
  ) {
    throw new Error('APP_INVALID_BOOTSTRAP_PAYLOAD')
  }
  return {
    auth_methods: ['password'],
    providers: [...value.providers],
    legal_documents: legalDocuments,
    session: value.session === undefined ? undefined : parseSession(value.session),
  }
}

export function parseAccountArea(value: unknown): AccountArea {
  if (!isRecord(value)
    || !isRecord(value.profile)
    || !Array.isArray(value.providers)
    || !Array.isArray(value.workspaces)) {
    throw new Error('APP_INVALID_ACCOUNT_PAYLOAD')
  }
  const profile: AccountProfile = {
    account_id: text(value.profile.account_id, 'APP_INVALID_ACCOUNT_PAYLOAD'),
    display_name: text(value.profile.display_name, 'APP_INVALID_ACCOUNT_PAYLOAD'),
    locale: text(value.profile.locale, 'APP_INVALID_ACCOUNT_PAYLOAD'),
    timezone: text(value.profile.timezone, 'APP_INVALID_ACCOUNT_PAYLOAD'),
    updated_at: isoDateTime(value.profile.updated_at, 'APP_INVALID_ACCOUNT_PAYLOAD'),
  }
  const parsedProviders = value.providers.map((provider): AccountProvider => {
    if (!isRecord(provider)
      || typeof provider.id !== 'string'
      || (provider.kind !== 'identity' && provider.kind !== 'social')
      || typeof provider.name !== 'string'
      || typeof provider.only_login_method !== 'boolean') {
      throw new Error('APP_INVALID_ACCOUNT_PAYLOAD')
    }
    return {
      id: provider.id,
      kind: provider.kind,
      name: provider.name,
      external_label: optionalText(provider.external_label),
      connected_at: isoDateTime(provider.connected_at, 'APP_INVALID_ACCOUNT_PAYLOAD'),
      only_login_method: provider.only_login_method,
    }
  })
  const workspaces = value.workspaces.map((entry): WorkspacePlan => {
    if (!isRecord(entry) || !isRecord(entry.plan)) {
      throw new Error('APP_INVALID_ACCOUNT_PAYLOAD')
    }
    return {
      workspace: parseWorkspace(entry.workspace),
      plan: {
        code: text(entry.plan.code, 'APP_INVALID_ACCOUNT_PAYLOAD'),
        name: text(entry.plan.name, 'APP_INVALID_ACCOUNT_PAYLOAD'),
        state: text(entry.plan.state, 'APP_INVALID_ACCOUNT_PAYLOAD'),
        usage: numberRecord(entry.plan.usage, 'APP_INVALID_ACCOUNT_PAYLOAD'),
        limits: numberRecord(entry.plan.limits, 'APP_INVALID_ACCOUNT_PAYLOAD'),
        renews_at: optionalText(entry.plan.renews_at),
        manageable: Boolean(entry.plan.manageable),
      },
    }
  })
  return {
    profile,
    providers: parsedProviders,
    workspaces,
  }
}

export function parseWorkspaceMembers(value: unknown): WorkspaceMember[] {
  if (!Array.isArray(value)) {
    throw new Error('APP_INVALID_WORKSPACE_MEMBERS')
  }
  return value.map((member) => {
    if (!isRecord(member)
      || typeof member.id !== 'string'
      || typeof member.account_id !== 'string'
      || typeof member.email !== 'string'
      || (member.role !== 'owner' && member.role !== 'member')
      || typeof member.status !== 'string') {
      throw new Error('APP_INVALID_WORKSPACE_MEMBERS')
    }
    return {
      id: member.id,
      account_id: member.account_id,
      email: member.email,
      role: member.role,
      status: member.status,
      created_at: isoDateTime(member.created_at, 'APP_INVALID_WORKSPACE_MEMBERS'),
      invited_by_account_id: optionalText(member.invited_by_account_id),
    }
  })
}

export function parseExportRequest(value: unknown): ExportRequest {
  if (!isRecord(value)
    || typeof value.id !== 'string'
    || typeof value.account_id !== 'string'
    || (value.scope !== 'account' && value.scope !== 'workspace')
    || !exportStatuses.has(value.status as ExportStatus)) {
    throw new Error('APP_INVALID_EXPORT_PAYLOAD')
  }
  return {
    id: value.id,
    account_id: value.account_id,
    scope: value.scope,
    workspace_id: optionalText(value.workspace_id),
    status: value.status as ExportStatus,
    requested_at: isoDateTime(value.requested_at, 'APP_INVALID_EXPORT_PAYLOAD'),
    expires_at: isoDateTime(value.expires_at, 'APP_INVALID_EXPORT_PAYLOAD'),
  }
}

export function parseExportDownload(value: unknown): ExportDownload {
  if (!isRecord(value)
    || typeof value.url !== 'string'
    || typeof value.sha256 !== 'string'
    || !/^[a-f0-9]{64}$/u.test(value.sha256)
    || typeof value.size_bytes !== 'number'
    || value.size_bytes < 1) {
    throw new Error('APP_INVALID_EXPORT_DOWNLOAD')
  }
  return {
    url: value.url,
    expires_at: isoDateTime(value.expires_at, 'APP_INVALID_EXPORT_DOWNLOAD'),
    sha256: value.sha256,
    size_bytes: value.size_bytes,
  }
}

export function parseDeletionRequest(value: unknown): DeletionRequest {
  if (!isRecord(value)
    || typeof value.id !== 'string'
    || typeof value.account_id !== 'string'
    || (value.scope !== 'account' && value.scope !== 'workspace')
    || !deletionStatuses.has(value.status as DeletionStatus)
    || !isRecord(value.ownership)
    || !Array.isArray(value.ownership.actions)) {
    throw new Error('APP_INVALID_DELETION_PAYLOAD')
  }
  return {
    id: value.id,
    account_id: value.account_id,
    scope: value.scope,
    workspace_id: optionalText(value.workspace_id),
    status: value.status as DeletionStatus,
    requested_at: isoDateTime(value.requested_at, 'APP_INVALID_DELETION_PAYLOAD'),
    grace_ends_at: isoDateTime(value.grace_ends_at, 'APP_INVALID_DELETION_PAYLOAD'),
    ownership: {
      actions: value.ownership.actions.map((action) => {
        if (!isRecord(action)
          || typeof action.workspace_id !== 'string'
          || (action.action !== 'transfer' && action.action !== 'delete')) {
          throw new Error('APP_INVALID_DELETION_PAYLOAD')
        }
        return {
          workspace_id: action.workspace_id,
          action: action.action,
          transfer_account_id: optionalText(action.transfer_account_id),
        }
      }),
    },
  }
}

export function parseDeletionCancelCapability(
  value: unknown,
): DeletionCancelCapability {
  if (!isRecord(value)
    || Object.keys(value).length !== 1
    || typeof value.expires_at !== 'string') {
    throw new Error('APP_INVALID_DELETION_CANCEL_CAPABILITY_PAYLOAD')
  }
  return {
    expires_at: isoDateTime(
      value.expires_at,
      'APP_INVALID_DELETION_CANCEL_CAPABILITY_PAYLOAD',
    ),
  }
}

export function buildConsentReceipts(
  documents: readonly LegalDocument[],
  locale: AppShellLocale,
): ConsentReceipt[] {
  if (documents.length !== 2) {
    throw new Error('APP_REQUIRED_DOCUMENTS_MISSING')
  }
  return documents.map(document => ({
    document_key: document.key,
    version: document.version,
    digest_sha256: document.digest_sha256,
    action: 'accepted',
    purpose: document.key === 'terms' ? 'contract' : 'privacy_acknowledgement',
    locale,
    surface: 'app_onboarding',
    control_text_id: `app.consent.${document.key}.v1`,
  }))
}

export function buildRegistrationConsents(
  documents: readonly LegalDocument[],
): RegistrationConsentReceipt[] {
  if (documents.length !== 2) {
    throw new Error('APP_REQUIRED_DOCUMENTS_MISSING')
  }
  return documents.map((document) => ({
    document_key: document.key === 'terms' ? 'terms_it' : 'privacy_it',
    version: document.version,
    digest_sha256: document.digest_sha256,
    action: document.key === 'terms' ? 'accepted' : 'acknowledged',
    purpose: document.key === 'terms' ? 'contract' : 'privacy_notice',
    locale: 'it-IT',
    surface: 'signup',
    control_text_id: document.key === 'terms'
      ? 'signup-terms-v1'
      : 'signup-privacy-v1',
  }))
}
