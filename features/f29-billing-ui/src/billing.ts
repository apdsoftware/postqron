import {
  monthlyEquivalent,
  parsePublicCatalog,
  parsePublicPlan,
  priceForChannels,
  type BillingInterval,
  type Money,
  type PricingLocale,
  type PublicCatalog,
  type PublicPlan,
  type PublicPlanCode,
} from '../../f02-marketing-site/src/catalog.ts'

export type CheckoutStatus =
  | 'idle'
  | 'creating'
  | 'open'
  | 'closed'
  | 'payment-failed'
  | 'processing'
  | 'confirmed'
  | 'error'

export type CheckoutAction =
  | 'create'
  | 'opened'
  | 'closed'
  | 'payment-failed'
  | 'completed'
  | 'entitlement-confirmed'
  | 'error'
  | 'retry'

export interface PurchaseIntent {
  plan: PublicPlanCode
  interval: BillingInterval
  // Unlimited is flat-priced: no channel quantity is accepted or
  // synthesized for it, matching the checkout contract.
  quantity?: number
}

export interface CheckoutSession {
  id: string
  url: string
  expires_at: string
}

export interface BillingUsage {
  resource: 'members' | 'channels' | 'scheduled_publications'
  used: number
  // A null limit/remaining means no commercial plan quota applies (Unlimited).
  limit: number | null
  remaining: number | null
  over_limit: boolean
}

export interface BillingOverview {
  plan: PublicPlan
  interval: BillingInterval
  state:
    | 'trialing'
    | 'active'
    | 'past_due'
    | 'trial_expired'
    | 'payment_restricted'
    | 'canceled'
  period: {
    start: string
    end: string
  }
  usage: BillingUsage[]
}

export interface BillingOverviewClient {
  overview(workspaceId: string): Promise<BillingOverview>
}

export type BillingFetch = (
  path: string,
  options?: Readonly<Record<string, unknown>>,
) => Promise<unknown>

export class BillingApiError extends Error {
  readonly status?: number
  readonly retryable: boolean

  constructor(message: string, options: {
    cause?: unknown
    retryable: boolean
    status?: number
  }) {
    super(message, { cause: options.cause })
    this.name = 'BillingApiError'
    this.status = options.status
    this.retryable = options.retryable
  }
}

const plans = new Set<PublicPlanCode>(['start', 'pro', 'team', 'unlimited'])
const intervals = new Set<BillingInterval>(['monthly', 'annual'])
const states = new Set<BillingOverview['state']>([
  'trialing',
  'active',
  'past_due',
  'trial_expired',
  'payment_restricted',
  'canceled',
])
const resources = new Set<BillingUsage['resource']>([
  'members',
  'channels',
  'scheduled_publications',
])

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function statusOf(error: unknown): number | undefined {
  if (!isRecord(error)) {
    return undefined
  }
  const response = isRecord(error.response) ? error.response : undefined
  for (const value of [error.statusCode, error.status, response?.status]) {
    if (typeof value === 'number') {
      return value
    }
  }
  return undefined
}

function queryValue(value: unknown): string | undefined {
  if (Array.isArray(value)) {
    return value.length === 1 && typeof value[0] === 'string'
      ? value[0]
      : undefined
  }
  return typeof value === 'string' ? value : undefined
}

export function parsePurchaseIntent(
  query: Readonly<Record<string, unknown>>,
): PurchaseIntent {
  const plan = queryValue(query.plan)
  const interval = queryValue(query.interval)
  const quantity = queryValue(query.quantity)
  if (
    !plan
    || !plans.has(plan as PublicPlanCode)
    || !interval
    || !intervals.has(interval as BillingInterval)
  ) {
    throw new Error('BILLING_INVALID_PURCHASE_INTENT')
  }
  if (plan === 'unlimited') {
    if (quantity !== undefined) {
      throw new Error('BILLING_INVALID_PURCHASE_INTENT')
    }
    return { plan: plan as PublicPlanCode, interval: interval as BillingInterval }
  }
  if (!quantity || !/^[1-9][0-9]*$/u.test(quantity)) {
    throw new Error('BILLING_INVALID_PURCHASE_INTENT')
  }
  const parsedQuantity = Number(quantity)
  if (!Number.isSafeInteger(parsedQuantity)) {
    throw new Error('BILLING_INVALID_PURCHASE_INTENT')
  }
  return {
    plan: plan as PublicPlanCode,
    interval: interval as BillingInterval,
    quantity: parsedQuantity,
  }
}

// Per-plan channel ceilings are never duplicated client-side: the fetched
// catalog's plan.limits.channels is the only source of truth for whether a
// parsed intent is actually purchasable at that quantity.
export function intentCompatibleWithPlan(
  plan: PublicPlan,
  intent: PurchaseIntent,
): boolean {
  if (plan.code !== intent.plan) {
    return false
  }
  if (plan.limits.channels === null) {
    return intent.quantity === undefined
  }
  return intent.quantity !== undefined && intent.quantity <= plan.limits.channels
}

export interface AnnualCheckoutSummary {
  // The amount charged upfront today, covering 12 months of service.
  total: Money
  // The plan's nominal monthly rate for the same plan/quantity, so the
  // annual total can be shown as "10 monthly payments of X".
  monthlyPrice: Money
  // The annual total spread evenly across 12 months, for comparison.
  monthlyEquivalent: Money
  // What is saved versus paying the monthly rate for 12 months.
  savings: Money
}

// Every amount here comes from the catalog's own monthly/annual prices
// (via priceForChannels): nothing is hardcoded, so the 10-for-12 framing
// and the savings amount always match whatever the backend prices.
export function annualCheckoutSummary(
  plan: PublicPlan,
  intent: PurchaseIntent,
): AnnualCheckoutSummary | undefined {
  if (intent.interval !== 'annual' || !plan.purchasable || !intentCompatibleWithPlan(plan, intent)) {
    return undefined
  }
  const channels = intent.quantity ?? null
  const total = priceForChannels(plan, 'annual', channels)
  const monthlyPrice = priceForChannels(plan, 'monthly', channels)
  return {
    total,
    monthlyPrice,
    monthlyEquivalent: monthlyEquivalent(total, 'annual'),
    savings: {
      amount_cents: Math.max(0, monthlyPrice.amount_cents * 12 - total.amount_cents),
      currency: total.currency,
    },
  }
}

export function checkoutPath(
  locale: PricingLocale,
  intent: PurchaseIntent,
): string {
  const prefix = locale === 'en' ? '' : `/${locale}`
  const params: Record<string, string> = { plan: intent.plan, interval: intent.interval }
  if (intent.quantity !== undefined) {
    params.quantity = String(intent.quantity)
  }
  const query = new URLSearchParams(params)
  return `${prefix}/app/billing/checkout?${query}`
}

export function checkoutTransition(
  status: CheckoutStatus,
  action: CheckoutAction,
): CheckoutStatus {
  if (action === 'retry') {
    return 'idle'
  }
  if (action === 'error') {
    return status === 'processing' || status === 'confirmed'
      ? status
      : 'error'
  }
  if (action === 'completed') {
    return 'processing'
  }
  if (action === 'entitlement-confirmed') {
    return status === 'processing' ? 'confirmed' : status
  }
  if (action === 'closed') {
    return status === 'processing' || status === 'confirmed'
      ? status
      : 'closed'
  }
  if (action === 'payment-failed') {
    return status === 'processing' || status === 'confirmed'
      ? status
      : 'payment-failed'
  }
  if (action === 'create' && (status === 'idle' || status === 'closed'
    || status === 'payment-failed' || status === 'error')) {
    return 'creating'
  }
  if (action === 'opened' && status === 'creating') {
    return 'open'
  }
  return status
}

export function createIdempotencyKey(
  randomUUID: () => string = () => globalThis.crypto.randomUUID(),
): string {
  return `billing-ui:${randomUUID()}`
}

export function safePaddleClientToken(value: unknown): string | undefined {
  return typeof value === 'string'
    && /^(?:test|live)_[A-Za-z0-9]+$/u.test(value)
    ? value
    : undefined
}

function safePaddleURL(value: unknown): string | undefined {
  if (typeof value !== 'string') {
    return undefined
  }
  try {
    const url = new URL(value)
    const paddleHost = url.hostname === 'paddle.com'
      || url.hostname.endsWith('.paddle.com')
      || url.hostname === 'paddle.io'
      || url.hostname.endsWith('.paddle.io')
    return url.protocol === 'https:' && paddleHost ? url.href : undefined
  } catch {
    return undefined
  }
}

export function parseCheckoutSession(value: unknown): CheckoutSession {
  if (!isRecord(value)
    || typeof value.id !== 'string'
    || !/^txn_[A-Za-z0-9]+$/u.test(value.id)
    || !safePaddleURL(value.url)
    || typeof value.expires_at !== 'string'
    || !Number.isFinite(Date.parse(value.expires_at))) {
    throw new Error('BILLING_INVALID_CHECKOUT_SESSION')
  }
  return {
    id: value.id,
    url: safePaddleURL(value.url)!,
    expires_at: value.expires_at,
  }
}

export function parsePortalSession(value: unknown): { url: string } {
  if (!isRecord(value) || !safePaddleURL(value.url)) {
    throw new Error('BILLING_INVALID_PORTAL_SESSION')
  }
  return { url: safePaddleURL(value.url)! }
}

function parseUsage(value: unknown): BillingUsage {
  if (!isRecord(value)
    || typeof value.resource !== 'string'
    || !resources.has(value.resource as BillingUsage['resource'])
    || !Number.isInteger(value.used)
    || !(value.limit === null || Number.isInteger(value.limit))
    || !(value.remaining === null || Number.isInteger(value.remaining))
    || typeof value.over_limit !== 'boolean') {
    throw new Error('BILLING_INVALID_OVERVIEW')
  }
  return value as unknown as BillingUsage
}

export function parseOverview(value: unknown): BillingOverview {
  if (!isRecord(value)
    || !isRecord(value.period)
    || !intervals.has(value.interval as BillingInterval)
    || !states.has(value.state as BillingOverview['state'])
    || typeof value.period.start !== 'string'
    || typeof value.period.end !== 'string'
    || !Number.isFinite(Date.parse(value.period.start))
    || !Number.isFinite(Date.parse(value.period.end))
    || !Array.isArray(value.usage)
    || value.usage.length !== 3) {
    throw new Error('BILLING_INVALID_OVERVIEW')
  }
  const usage = value.usage.map(parseUsage)
  if (new Set(usage.map(item => item.resource)).size !== 3) {
    throw new Error('BILLING_INVALID_OVERVIEW')
  }
  return {
    plan: parsePublicPlan(value.plan),
    interval: value.interval as BillingInterval,
    state: value.state as BillingOverview['state'],
    period: {
      start: value.period.start,
      end: value.period.end,
    },
    usage,
  }
}

export function entitlementConfirmed(
  overview: BillingOverview,
  intent: PurchaseIntent,
): boolean {
  const channels = overview.usage.find(usage => usage.resource === 'channels')
  const channelsSatisfied = intent.quantity === undefined
    ? true
    : Boolean(channels && channels.limit !== null && channels.limit >= intent.quantity)
  return overview.plan.code === intent.plan
    && overview.interval === intent.interval
    && overview.state === 'active'
    && channelsSatisfied
}

export async function loadBillingOverview(
  api: BillingOverviewClient,
  workspaceId: string,
): Promise<BillingOverview | undefined> {
  if (!workspaceId) {
    return undefined
  }
  return api.overview(workspaceId)
}

export class BillingApi {
  readonly #baseURL: string
  readonly #fetch: BillingFetch

  constructor(baseURL: string, fetch: BillingFetch) {
    this.#baseURL = baseURL.replace(/\/+$/u, '')
    this.#fetch = fetch
  }

  async #request(
    path: string,
    options: Readonly<Record<string, unknown>> = {},
  ): Promise<unknown> {
    try {
      return await this.#fetch(path, {
        baseURL: this.#baseURL,
        credentials: 'include',
        ...options,
      })
    } catch (error) {
      const status = statusOf(error)
      throw new BillingApiError('BILLING_REQUEST_FAILED', {
        cause: error,
        status,
        retryable: status === undefined || status === 0 || status >= 500,
      })
    }
  }

  async catalog(): Promise<PublicCatalog> {
    return parsePublicCatalog(await this.#request('/api/v1/billing/plans'))
  }

  async overview(workspaceId: string): Promise<BillingOverview> {
    if (!workspaceId) {
      throw new BillingApiError('BILLING_WORKSPACE_UNAVAILABLE', {
        retryable: false,
      })
    }
    return parseOverview(await this.#request(
      `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/billing`,
    ))
  }

  async checkout(
    workspaceId: string,
    intent: PurchaseIntent,
    idempotencyKey: string,
  ): Promise<CheckoutSession> {
    const body: Record<string, unknown> = {
      plan: intent.plan,
      interval: intent.interval,
      idempotency_key: idempotencyKey,
    }
    // Unlimited is flat-priced: no channels field is sent, matching the
    // UnlimitedCheckoutRequest contract which rejects any channel quantity.
    if (intent.quantity !== undefined) {
      body.channels = intent.quantity
    }
    return parseCheckoutSession(await this.#request(
      `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/billing/checkout`,
      { method: 'POST', body },
    ))
  }

  async portal(
    workspaceId: string,
    idempotencyKey: string,
  ): Promise<{ url: string }> {
    return parsePortalSession(await this.#request(
      `/api/v1/workspaces/${encodeURIComponent(workspaceId)}/billing/portal`,
      {
        method: 'POST',
        body: { idempotency_key: idempotencyKey },
      },
    ))
  }
}

export const BILLING_NOTIFICATION_BOUNDARY = Object.freeze({
  uiEmitsEmail: false,
  mailronixEvents: Object.freeze([
    'f14.payment_failed.v1',
    'f14.plan_changed.v1',
    'f14.plan_cancelled.v1',
    'f14.grace_period.v1',
  ]),
  paddleOwns: Object.freeze(['fiscal_receipt', 'mandatory_payment_notice']),
})
