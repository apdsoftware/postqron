# Mailronix server-to-server contract 1.0.0

Verified on 2026-07-25 from the official, public Mailronix console artifact:

- OpenAPI: `https://app.mailronix.com/openapi.yaml`
- SHA-256 at verification time:
  `a338995779a96cf82dea8c8293cfa0296b299685fee1ab902cfbf978e6ad6f39`
- OpenAPI version: 3.1.0
- API version: 1.0.0
- Production server: `https://api.mailronix.com`
- Only server-to-server operation: `POST /email/send`
- Authentication: `Authorization: Bearer mrx_live_<secret>`
- Direct request fields: `from`, `to`, `subject`, and at least one of
  `html_body`/`text_body`
- Success: HTTP 202 with `status=queued` and UUID `email_log_id`
- Documented errors: 400, 401, 403, 404, 429, 500, and 503 with
  `{error:{code,message}}`

The official contract says the API response is intentionally identical when a
recipient is accepted or silently discarded by Mailronix suppression handling.
Mailronix publicly states that sender-domain verification is mandatory and
bounce/complaint handling is automatic.

## Contract gaps that block complete go-live acceptance

The verified API does not expose SMTP, webhooks, delivery/bounce/complaint
events, Reply-To, arbitrary headers, provider idempotency keys, a sandbox
server, or numeric rate limits. The local server listed by the OpenAPI document
is not an official remotely reachable fake. Therefore F14:

- does not invent webhook routes, signatures, SMTP credentials, or payloads;
- uses a recipient-safe in-process fake for development and CI;
- persists an application idempotency key before sending, but cannot guarantee
  provider-side deduplication after an ambiguous timeout;
- cannot record delivered/bounced/complained states or mirror suppressions;
- cannot set `Reply-To: help@postqron.com` until Mailronix publishes support;
- treats 429, 500, and 503 as retryable and does not claim an undocumented
  numeric rate.

Before production activation, obtain written Mailronix confirmation or a newer
official contract for those capabilities, verify the Postqron sender domain in
the console, and record successful SPF, DKIM, DMARC and two-provider
deliverability checks. Production construction is deliberately rejected unless
the deployment asserts that domain verification has completed.
