import type {
  AdminAuditQuery,
  AdminPlanQuery,
} from './api.ts'

export const ADMIN_LIST_PAGE_SIZE = 25

function value(query: Readonly<Record<string, unknown>>, key: string): string {
  const candidate = query[key]
  return typeof candidate === 'string' ? candidate : ''
}

function page(query: Readonly<Record<string, unknown>>): number {
  const candidate = Number(value(query, 'page'))
  return Number.isSafeInteger(candidate) && candidate > 0 ? candidate : 1
}

function direction(value: string): 'asc' | 'desc' {
  return value === 'asc' ? 'asc' : 'desc'
}

export function planQueryFromRoute(
  routeQuery: Readonly<Record<string, unknown>>,
): AdminPlanQuery {
  return {
    q: value(routeQuery, 'q'),
    plan: value(routeQuery, 'plan'),
    status: value(routeQuery, 'status'),
    type: value(routeQuery, 'type'),
    from: value(routeQuery, 'from'),
    to: value(routeQuery, 'to'),
    sort: value(routeQuery, 'sort') || 'updated_at',
    direction: direction(value(routeQuery, 'direction')),
    page: page(routeQuery),
    page_size: ADMIN_LIST_PAGE_SIZE,
  }
}

export function auditQueryFromRoute(
  routeQuery: Readonly<Record<string, unknown>>,
): AdminAuditQuery {
  return {
    action: value(routeQuery, 'action'),
    actor: value(routeQuery, 'actor'),
    subject: value(routeQuery, 'subject'),
    outcome: value(routeQuery, 'outcome'),
    from: value(routeQuery, 'from'),
    to: value(routeQuery, 'to'),
    sort: value(routeQuery, 'sort') || 'occurred_at',
    direction: direction(value(routeQuery, 'direction')),
    page: page(routeQuery),
    page_size: ADMIN_LIST_PAGE_SIZE,
  }
}

export function routeQuery(
  query: Readonly<Record<string, string | number | undefined>>,
): Record<string, string> {
  const result: Record<string, string> = {}
  for (const [key, candidate] of Object.entries(query)) {
    if (
      candidate !== undefined
      && candidate !== ''
      && key !== 'page_size'
      && !(key === 'page' && candidate === 1)
    ) {
      result[key] = String(candidate)
    }
  }
  return result
}

export function localDateTime(instant: string | undefined): string {
  if (!instant || !Number.isFinite(Date.parse(instant))) {
    return ''
  }
  return new Date(instant).toISOString().slice(0, 16)
}

export function utcInstant(local: string): string {
  if (!local) {
    return ''
  }
  const value = new Date(`${local}:00.000Z`)
  return Number.isFinite(value.getTime()) ? value.toISOString() : ''
}
