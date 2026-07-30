# F29 — Plan, usage, and Paddle billing UI

F29 connects the D09 v2 public catalog to the authenticated billing experience.
The pricing page receives every Postqron amount from
`GET /api/v1/billing/plans`; the browser never contains Paddle price IDs or
reimplements the server-side price mapping.

The authenticated plan page combines the F10 overview and public catalog. It
shows the active plan, state or trial, billing interval, renewal/period end,
subscribed channel capacity, catalog-derived base price, and separate localized
usage indicators for users, social channels, and scheduled posts. Nullable F10
limits are rendered as Unlimited rather than converted to numeric sentinels.

The plan comparison uses all four server plans and both billing intervals. It
preselects the lowest plan compatible with the latest overview, disables plans
whose catalog limits cannot contain current usage, preserves an eligible
channel quantity, and derives every displayed total from the catalog. Owners
with an existing paid subscription review changes through
`POST .../subscription/preview` and confirm them through
`PATCH .../subscription`. The same idempotency key is retained between preview,
request, and safe retry. A first paid subscription still uses the F10 checkout
endpoint, as required by the Start change contract.

A `202` plan-change result remains pending: the UI does not claim activation
until a matching overview is visible after the verified Paddle webhook.
Downgrades scheduled for period end stay pending without changing the current
entitlement. A `downgrade_limit_exceeded` response lists every authoritative
`resource`, `used`, `limit`, and `excess` value with localized guidance. F29
does not call deletion endpoints, disconnect channels, revoke members, or
modify scheduled posts.

Paid CTAs carry `plan`, `interval`, and `quantity` through the F30 login and
onboarding return URL. After authentication this slice creates one idempotent
server transaction and opens it with Paddle.js using
`NUXT_PUBLIC_PADDLE_CLIENT_TOKEN`. Only `test_` and `live_` client-side tokens
are accepted. Paddle API keys are rejected and must remain in F10.

The returned intent is only ever a candidate: the checkout page cross-checks
`plan`/`quantity`/`interval` against the fetched `d09-v2` catalog before
opening Paddle. No channel ceiling is duplicated client-side — an
incompatible combination (wrong quantity for the plan, a stale link, a
tampered URL) is rejected and sent back to pricing instead of reaching
Paddle. Unlimited never carries a quantity. For annual billing the checkout
summary states the upfront total, that it covers 12 months of service, the
equivalent of 10 monthly payments, the monthly-equivalent price, the amount
saved versus paying monthly, and that it renews automatically each year —
every figure derived from the catalog's own monthly/annual prices, never a
hardcoded percentage.

`checkout.completed` is not treated as entitlement confirmation. The UI stays
in the processing state until the verified F10 webhook changes the billing
overview. Repeated checkout requests reuse the same idempotency key, while
close and payment-failure events remain retryable without granting access.

The public D07 amounts are before transaction taxes. Paddle Checkout displays
localized subtotal, taxes, total, recurrence, and renewal terms before consent.
Paddle is the Merchant of Record and owns fiscal receipts and mandatory Paddle
communications.

F29 never sends billing email. F10 lifecycle events feed the F14/Mailronix
matrix for plan changes, payment failures, grace periods, downgrades, and
cancellations; Paddle receipts are intentionally not duplicated.

For a real sandbox pass, configure a Paddle sandbox catalog in F10, a
`test_...` client token, and an authenticated Owner workspace, then verify
success, overlay close, and payment failure. The deterministic test suite uses
the same event/state boundary without requiring repository secrets. The
plan-change fixture covers an upgrade, an allowed period-end downgrade, and a
blocked downgrade with no resource mutation.
