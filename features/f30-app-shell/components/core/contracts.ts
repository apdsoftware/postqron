import type { AppShellLocale } from './catalogs.ts'

export type OAuthProvider = 'google' | 'apple' | 'facebook' | 'linkedin'

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
    id: string
    locale: AppShellLocale
  }
  current_workspace?: WorkspaceSummary
  onboarding_required: boolean
  workspaces: WorkspaceSummary[]
}

export interface AppBootstrap {
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

const providers = new Set<OAuthProvider>([
  'google',
  'apple',
  'facebook',
  'linkedin',
])
const locales = new Set<AppShellLocale>(['en', 'it', 'es', 'fr', 'de'])

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
  if (!isRecord(value) || !isRecord(value.account)
    || typeof value.account.id !== 'string'
    || typeof value.account.display_name !== 'string'
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
      email: typeof value.account.email === 'string'
        ? value.account.email
        : undefined,
      locale: value.account.locale as AppShellLocale,
    },
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
    || !Array.isArray(value.providers)
    || !value.providers.every(provider => providers.has(provider))
    || !Array.isArray(value.legal_documents)) {
    throw new Error('APP_INVALID_BOOTSTRAP_PAYLOAD')
  }
  const legalDocuments = value.legal_documents.map(parseLegalDocument)
  if (
    legalDocuments.length !== 2
    || new Set(legalDocuments.map(document => document.key)).size !== 2
  ) {
    throw new Error('APP_INVALID_BOOTSTRAP_PAYLOAD')
  }
  return {
    providers: [...value.providers],
    legal_documents: legalDocuments,
    session: value.session === undefined ? undefined : parseSession(value.session),
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
