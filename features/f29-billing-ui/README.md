# F29 — Pricing and Paddle checkout UI

F29 connects the D07 public catalog to the authenticated billing experience.
The pricing page receives every Postqron amount from
`GET /api/v1/billing/plans`; the browser never contains Paddle price IDs or
reimplements the server-side price mapping.

Paid CTAs carry `plan`, `interval`, and `quantity` through the F30 login and
onboarding return URL. After authentication this slice creates one idempotent
server transaction and opens it with Paddle.js using
`NUXT_PUBLIC_PADDLE_CLIENT_TOKEN`. Only `test_` and `live_` client-side tokens
are accepted. Paddle API keys are rejected and must remain in F10.

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
the same event/state boundary without requiring repository secrets.
