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

export interface UsageSummary {
  used: number
  limit: number | null
  remaining: number | null
  unlimited: boolean
}

export interface PlanRow {
  workspace_id: string
  workspace_name: string
  owner_email: string
  plan_code: string
  status: string
  internal: boolean
  usage: {
    members: UsageSummary
    channels: UsageSummary
    scheduled_publications: UsageSummary
  }
  workspace_created_at: string
  plan_updated_at: string
  period_start: string
  period_end: string
  internal_assigned_at: string | null
}

export interface PageInfo {
  page: number
  page_size: number
  total: number
}

export interface PlanList {
  items: PlanRow[]
  pagination: PageInfo
}

export interface AuditList {
  items: AuditEvent[]
  pagination: PageInfo
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

export type DirectoryDirection = 'asc' | 'desc'
export type ExportFormat = 'csv' | 'xlsx'

export interface UserWorkspaceMembership {
  id: string
  name: string
  role: string
  plan_code: string
  plan_status: string
}

export interface UserDirectoryItem {
  id: string
  email: string
  display_name: string
  account_status: string
  email_verified: boolean
  login_methods: string[]
  registered_at: string
  last_login_at: string | null
  active_sessions: number
  workspaces: UserWorkspaceMembership[]
}

export interface UserDirectoryPage {
  items: UserDirectoryItem[]
  page: number
  page_size: number
  total: number
  sort: string
  direction: DirectoryDirection
}

export interface UserDirectoryParams {
  q?: string
  status?: string
  email_verified?: boolean
  plan?: string
  login_method?: string
  registered_from?: string
  registered_to?: string
  last_login_from?: string
  last_login_to?: string
  page?: number
  page_size?: number
  sort?: string
  direction?: DirectoryDirection
}

export interface WorkspaceDirectoryItem {
  id: string
  name: string
  owner_id: string
  owner_email: string
  owner_display_name: string
  status: string
  plan_code: string
  plan_status: string
  member_count: number
  channel_count: number
  post_count: number
  created_at: string
  updated_at: string
}

export interface WorkspaceDirectoryPage {
  items: WorkspaceDirectoryItem[]
  page: number
  page_size: number
  total: number
  sort: string
  direction: DirectoryDirection
}

export interface WorkspaceDirectoryParams {
  q?: string
  status?: string
  plan?: string
  owner?: string
  created_from?: string
  created_to?: string
  updated_from?: string
  updated_to?: string
  page?: number
  page_size?: number
  sort?: string
  direction?: DirectoryDirection
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

function integer(value: unknown, field: string): number {
  if (
    typeof value !== 'number'
    || !Number.isSafeInteger(value)
    || value < 0
  ) {
    throw new Error(`ADMIN_INVALID_RESPONSE:${field}`)
  }
  return value
}

function nullableInstant(value: unknown, field: string): string | null {
  return value === null ? null : instant(value, field)
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

function nonNegativeInteger(value: unknown, field: string): number {
  if (
    typeof value !== 'number'
    || !Number.isSafeInteger(value)
    || value < 0
  ) {
    throw new Error(`ADMIN_INVALID_RESPONSE:${field}`)
  }
  return value
}

function direction(value: unknown): DirectoryDirection {
  if (value !== 'asc' && value !== 'desc') {
    throw new Error('ADMIN_INVALID_RESPONSE:direction')
  }
  return value
}

function parseUserDirectoryItem(value: unknown): UserDirectoryItem {
  const user = record(value)
  if (typeof user.email_verified !== 'boolean') {
    throw new Error('ADMIN_INVALID_RESPONSE:users.email_verified')
  }
  return {
    id: text(user.id, 'users.id', true),
    email: text(user.email, 'users.email'),
    display_name: text(user.display_name, 'users.display_name'),
    account_status: text(user.account_status, 'users.account_status', true),
    email_verified: user.email_verified,
    login_methods: list(user.login_methods, 'users.login_methods', method =>
      text(method, 'users.login_methods.method', true)),
    registered_at: instant(user.registered_at, 'users.registered_at'),
    last_login_at: nullableInstant(user.last_login_at, 'users.last_login_at'),
    active_sessions: nonNegativeInteger(user.active_sessions, 'users.active_sessions'),
    workspaces: list(user.workspaces, 'users.workspaces', (item) => {
      const workspace = record(item)
      return {
        id: text(workspace.id, 'users.workspaces.id', true),
        name: text(workspace.name, 'users.workspaces.name'),
        role: text(workspace.role, 'users.workspaces.role', true),
        plan_code: text(workspace.plan_code, 'users.workspaces.plan_code', true),
        plan_status: text(workspace.plan_status, 'users.workspaces.plan_status', true),
      }
    }),
  }
}

function parseWorkspaceDirectoryItem(value: unknown): WorkspaceDirectoryItem {
  const workspace = record(value)
  return {
    id: text(workspace.id, 'workspaces.id', true),
    name: text(workspace.name, 'workspaces.name'),
    owner_id: text(workspace.owner_id, 'workspaces.owner_id', true),
    owner_email: text(workspace.owner_email, 'workspaces.owner_email'),
    owner_display_name: text(
      workspace.owner_display_name,
      'workspaces.owner_display_name',
    ),
    status: text(workspace.status, 'workspaces.status', true),
    plan_code: text(workspace.plan_code, 'workspaces.plan_code', true),
    plan_status: text(workspace.plan_status, 'workspaces.plan_status', true),
    member_count: nonNegativeInteger(workspace.member_count, 'workspaces.member_count'),
    channel_count: nonNegativeInteger(workspace.channel_count, 'workspaces.channel_count'),
    post_count: nonNegativeInteger(workspace.post_count, 'workspaces.post_count'),
    created_at: instant(workspace.created_at, 'workspaces.created_at'),
    updated_at: instant(workspace.updated_at, 'workspaces.updated_at'),
  }
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
    recent_audit: list(source.recent_audit, 'recent_audit', parseAuditEvent),
  }
}

function parsePageInfo(value: unknown): PageInfo {
  const source = record(value)
  const page = integer(source.page, 'pagination.page')
  const pageSize = integer(source.page_size, 'pagination.page_size')
  if (page < 1 || pageSize < 1 || pageSize > 100) {
    throw new Error('ADMIN_INVALID_RESPONSE:pagination')
  }
  return {
    page,
    page_size: pageSize,
    total: integer(source.total, 'pagination.total'),
  }
}

function parseUsageSummary(value: unknown, field: string): UsageSummary {
  const source = record(value)
  if (typeof source.unlimited !== 'boolean') {
    throw new Error(`ADMIN_INVALID_RESPONSE:${field}.unlimited`)
  }
  const parseNullable = (candidate: unknown, name: string): number | null =>
    candidate === null ? null : integer(candidate, `${field}.${name}`)
  const limit = parseNullable(source.limit, 'limit')
  const remaining = parseNullable(source.remaining, 'remaining')
  if (
    (source.unlimited && (limit !== null || remaining !== null))
    || (!source.unlimited && (limit === null || remaining === null))
  ) {
    throw new Error(`ADMIN_INVALID_RESPONSE:${field}.capacity`)
  }
  return {
    used: integer(source.used, `${field}.used`),
    limit,
    remaining,
    unlimited: source.unlimited,
  }
}

export function parsePlanList(value: unknown): PlanList {
  const source = record(value)
  return {
    items: list(source.items, 'items', (item) => {
      const plan = record(item)
      const usage = record(plan.usage)
      if (typeof plan.internal !== 'boolean') {
        throw new Error('ADMIN_INVALID_RESPONSE:plans.internal')
      }
      return {
        workspace_id: text(plan.workspace_id, 'plans.workspace_id', true),
        workspace_name: text(plan.workspace_name, 'plans.workspace_name'),
        owner_email: text(plan.owner_email, 'plans.owner_email'),
        plan_code: text(plan.plan_code, 'plans.plan_code', true),
        status: text(plan.status, 'plans.status', true),
        internal: plan.internal,
        usage: {
          members: parseUsageSummary(usage.members, 'plans.usage.members'),
          channels: parseUsageSummary(usage.channels, 'plans.usage.channels'),
          scheduled_publications: parseUsageSummary(
            usage.scheduled_publications,
            'plans.usage.scheduled_publications',
          ),
        },
        workspace_created_at: instant(
          plan.workspace_created_at,
          'plans.workspace_created_at',
        ),
        plan_updated_at: instant(plan.plan_updated_at, 'plans.plan_updated_at'),
        period_start: instant(plan.period_start, 'plans.period_start'),
        period_end: instant(plan.period_end, 'plans.period_end'),
        internal_assigned_at: nullableInstant(
          plan.internal_assigned_at,
          'plans.internal_assigned_at',
        ),
      }
    }),
    pagination: parsePageInfo(source.pagination),
  }
}

export function parseAuditEvent(value: unknown): AuditEvent {
  const event = record(value)
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
}

export function parseAuditList(value: unknown): AuditList {
  const source = record(value)
  return {
    items: list(source.items, 'items', parseAuditEvent),
    pagination: parsePageInfo(source.pagination),
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

export function parseUserDirectoryPage(value: unknown): UserDirectoryPage {
  const source = record(value)
  const page = nonNegativeInteger(source.page, 'page')
  if (page < 1) {
    throw new Error('ADMIN_INVALID_RESPONSE:page')
  }
  const pageSize = nonNegativeInteger(source.page_size, 'page_size')
  if (![10, 25, 50, 100].includes(pageSize)) {
    throw new Error('ADMIN_INVALID_RESPONSE:page_size')
  }
  return {
    items: list(source.items, 'items', parseUserDirectoryItem),
    page,
    page_size: pageSize,
    total: nonNegativeInteger(source.total, 'total'),
    sort: text(source.sort, 'sort', true),
    direction: direction(source.direction),
  }
}

export function parseUserDirectoryDetail(value: unknown): UserDirectoryItem {
  return parseUserDirectoryItem(value)
}

export function parseWorkspaceDirectoryPage(
  value: unknown,
): WorkspaceDirectoryPage {
  const source = record(value)
  const page = nonNegativeInteger(source.page, 'page')
  if (page < 1) {
    throw new Error('ADMIN_INVALID_RESPONSE:page')
  }
  const pageSize = nonNegativeInteger(source.page_size, 'page_size')
  if (![10, 25, 50, 100].includes(pageSize)) {
    throw new Error('ADMIN_INVALID_RESPONSE:page_size')
  }
  return {
    items: list(source.items, 'items', parseWorkspaceDirectoryItem),
    page,
    page_size: pageSize,
    total: nonNegativeInteger(source.total, 'total'),
    sort: text(source.sort, 'sort', true),
    direction: direction(source.direction),
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
