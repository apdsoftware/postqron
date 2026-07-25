const safeIDPattern = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/u

export interface AdminSession {
  account: {
    id: string
    email: string
  }
  authenticated_at: string
  csrf_token: string
}

export interface ServiceHealth {
  code: string
  status: string
  checked_at: string
}

export interface EntitlementSummary {
  workspace_id: string
  plan_code: string
  internal: boolean
}

export interface AuditEvent {
  id: string
  code: string
  actor_id: string
  subject_id: string
  reason: string
  outcome: string
  correlation_id: string
  occurred_at: string
}

export interface AdminDashboard {
  services: ServiceHealth[]
  entitlements: EntitlementSummary[]
  recent_audit: AuditEvent[]
}

export interface UserSummary {
  id: string
  email: string
  display_name: string
  email_verified: boolean
}

export interface WorkspaceSummary {
  id: string
  name: string
  owner_email: string
  member_count: number
}

export interface SearchResults {
  users: UserSummary[]
  workspaces: WorkspaceSummary[]
}

export interface MutationResult {
  code: string
  correlation_id: string
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('ADMIN_INVALID_RESPONSE')
  }
  return value as Record<string, unknown>
}

function text(value: unknown, field: string, identifier = false): string {
  if (
    typeof value !== 'string'
    || value.length === 0
    || value.length > 500
    || (identifier && !safeIDPattern.test(value))
  ) {
    throw new Error(`ADMIN_INVALID_RESPONSE:${field}`)
  }
  return value
}

function instant(value: unknown, field: string): string {
  const result = text(value, field)
  if (!Number.isFinite(Date.parse(result))) {
    throw new Error(`ADMIN_INVALID_RESPONSE:${field}`)
  }
  return result
}

function list<T>(
  value: unknown,
  field: string,
  parser: (item: unknown) => T,
): T[] {
  if (!Array.isArray(value) || value.length > 250) {
    throw new Error(`ADMIN_INVALID_RESPONSE:${field}`)
  }
  return value.map(parser)
}

export function parseAdminSession(value: unknown): AdminSession {
  const source = record(value)
  const account = record(source.account)
  return {
    account: {
      id: text(account.id, 'account.id', true),
      email: text(account.email, 'account.email'),
    },
    authenticated_at: instant(source.authenticated_at, 'authenticated_at'),
    csrf_token: text(source.csrf_token, 'csrf_token'),
  }
}

export function parseDashboard(value: unknown): AdminDashboard {
  const source = record(value)
  return {
    services: list(source.services, 'services', (item) => {
      const health = record(item)
      return {
        code: text(health.code, 'services.code', true),
        status: text(health.status, 'services.status', true),
        checked_at: instant(health.checked_at, 'services.checked_at'),
      }
    }),
    entitlements: list(source.entitlements, 'entitlements', (item) => {
      const entitlement = record(item)
      if (typeof entitlement.internal !== 'boolean') {
        throw new Error('ADMIN_INVALID_RESPONSE:entitlements.internal')
      }
      return {
        workspace_id: text(entitlement.workspace_id, 'entitlements.workspace_id', true),
        plan_code: text(entitlement.plan_code, 'entitlements.plan_code', true),
        internal: entitlement.internal,
      }
    }),
    recent_audit: list(source.recent_audit, 'recent_audit', (item) => {
      const event = record(item)
      return {
        id: text(event.id, 'audit.id', true),
        code: text(event.code, 'audit.code', true),
        actor_id: text(event.actor_id, 'audit.actor_id', true),
        subject_id: text(event.subject_id, 'audit.subject_id', true),
        reason: text(event.reason, 'audit.reason'),
        outcome: text(event.outcome, 'audit.outcome', true),
        correlation_id: text(event.correlation_id, 'audit.correlation_id', true),
        occurred_at: instant(event.occurred_at, 'audit.occurred_at'),
      }
    }),
  }
}

export function parseSearchResults(value: unknown): SearchResults {
  const source = record(value)
  return {
    users: list(source.users, 'users', (item) => {
      const user = record(item)
      if (typeof user.email_verified !== 'boolean') {
        throw new Error('ADMIN_INVALID_RESPONSE:users.email_verified')
      }
      return {
        id: text(user.id, 'users.id', true),
        email: text(user.email, 'users.email'),
        display_name: text(user.display_name, 'users.display_name'),
        email_verified: user.email_verified,
      }
    }),
    workspaces: list(source.workspaces, 'workspaces', (item) => {
      const workspace = record(item)
      if (
        typeof workspace.member_count !== 'number'
        || !Number.isSafeInteger(workspace.member_count)
        || workspace.member_count < 0
      ) {
        throw new Error('ADMIN_INVALID_RESPONSE:workspaces.member_count')
      }
      return {
        id: text(workspace.id, 'workspaces.id', true),
        name: text(workspace.name, 'workspaces.name'),
        owner_email: text(workspace.owner_email, 'workspaces.owner_email'),
        member_count: workspace.member_count,
      }
    }),
  }
}

export function parseMutationResult(value: unknown): MutationResult {
  const source = record(value)
  return {
    code: text(source.code, 'code', true),
    correlation_id: text(source.correlation_id, 'correlation_id', true),
  }
}

export function assertSafeIdentifier(value: string): string {
  if (!safeIDPattern.test(value)) {
    throw new Error('ADMIN_INVALID_IDENTIFIER')
  }
  return value
}
