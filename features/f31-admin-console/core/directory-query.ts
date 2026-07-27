import type {
  DirectoryDirection,
  UserDirectoryParams,
  WorkspaceDirectoryParams,
} from './contracts.ts'

const pageSizes = new Set([10, 25, 50, 100])

function first(value: unknown): string {
  if (Array.isArray(value)) {
    return typeof value[0] === 'string' ? value[0] : ''
  }
  return typeof value === 'string' ? value : ''
}

function optional(
  query: Readonly<Record<string, unknown>>,
  key: string,
): string | undefined {
  const value = first(query[key]).trim()
  return value || undefined
}

function pageValue(value: unknown, fallback: number): number {
  const parsed = Number(first(value))
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : fallback
}

function pageSizeValue(value: unknown): number {
  const parsed = pageValue(value, 25)
  return pageSizes.has(parsed) ? parsed : 25
}

function directionValue(value: unknown): DirectoryDirection {
  return first(value) === 'asc' ? 'asc' : 'desc'
}

export function userDirectoryParamsFromQuery(
  query: Readonly<Record<string, unknown>>,
): UserDirectoryParams {
  const verified = first(query.email_verified)
  return compact({
    q: optional(query, 'q'),
    status: optional(query, 'status'),
    email_verified: verified === 'true'
      ? true
      : verified === 'false'
        ? false
        : undefined,
    plan: optional(query, 'plan'),
    login_method: optional(query, 'login_method'),
    registered_from: optional(query, 'registered_from'),
    registered_to: optional(query, 'registered_to'),
    last_login_from: optional(query, 'last_login_from'),
    last_login_to: optional(query, 'last_login_to'),
    page: pageValue(query.page, 1),
    page_size: pageSizeValue(query.page_size),
    sort: optional(query, 'sort') ?? 'registered_at',
    direction: directionValue(query.direction),
  })
}

export function workspaceDirectoryParamsFromQuery(
  query: Readonly<Record<string, unknown>>,
): WorkspaceDirectoryParams {
  return compact({
    q: optional(query, 'q'),
    status: optional(query, 'status'),
    plan: optional(query, 'plan'),
    owner: optional(query, 'owner'),
    created_from: optional(query, 'created_from'),
    created_to: optional(query, 'created_to'),
    updated_from: optional(query, 'updated_from'),
    updated_to: optional(query, 'updated_to'),
    page: pageValue(query.page, 1),
    page_size: pageSizeValue(query.page_size),
    sort: optional(query, 'sort') ?? 'updated_at',
    direction: directionValue(query.direction),
  })
}

export function directorySearchParams(
  parameters: UserDirectoryParams | WorkspaceDirectoryParams,
  includePage = true,
): URLSearchParams {
  const output = new URLSearchParams()
  for (const [key, value] of Object.entries(parameters)) {
    if (
      value === undefined
      || value === ''
      || (!includePage && (key === 'page' || key === 'page_size'))
    ) {
      continue
    }
    output.set(key, String(value))
  }
  return output
}

export function directoryRouteQuery(
  parameters: UserDirectoryParams | WorkspaceDirectoryParams,
): Record<string, string> {
  return Object.fromEntries(directorySearchParams(parameters))
}

function compact<T extends object>(value: T): T {
  return Object.fromEntries(
    Object.entries(value).filter(([, item]) => item !== undefined),
  ) as T
}
