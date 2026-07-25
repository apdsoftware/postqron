# Launch readiness — manual production checklist

These checks require production account access or legal authority and must not
be simulated by the automated suite. Record the reviewer, UTC timestamp,
evidence link and outcome for every item before go-live.

## Paddle production

- Confirm the production Paddle client token, API key and webhook destination
  belong to the Postqron production account and are stored only in the runtime
  secret store.
- Run the read-only production catalog check and compare all twelve product and
  price mappings with D07; do not create or update catalog objects from CI.
- Complete one production-mode checkout with an approved low-value test
  procedure, confirm the signed webhook, entitlement transition and temporary
  customer-portal session, then cancel/reconcile the test subscription.
- Verify taxes, Merchant of Record disclosure, refund/cancellation copy and the
  final amount before customer consent.

## Transactional email DNS and Mailronix

- Confirm the sender domain in Mailronix and independently verify SPF, DKIM and
  DMARC from public DNS.
- Send approved previews for every transactional template to the agreed client
  matrix; verify plain text, mobile layout, zoom and assistive-technology use.
- Confirm the Mailronix contract/DPA, subprocessor record, retention and
  transfer safeguards, plus the documented limitations for Reply-To, delivery
  webhooks, provider idempotency and test mode.
- Confirm no production key, recipient address or full provider payload is
  present in browser logs, application logs or CI artifacts.

## Legal approval

- Obtain independent counsel approval for the exact immutable digests of
  Terms, Privacy Policy, Cookie Policy, DPA and subprocessor register in
  en/it/es/fr/de for every launch market.
- Confirm controller/contact identity, effective dates, version history,
  retention periods, cookie inventory and Paddle/Mailronix disclosures.
- Verify every public legal URL returns the approved release with HTTP 200 and
  that older effective versions remain addressable.
- Record the legal release approval reference before changing the launch gate.

## Final operations sign-off

- Verify both explicit pre-launch states using production configuration and
  confirm rollback to `PRELAUNCH_MODE=true`.
- Confirm the launch-readiness workflow report is green for the deployed
  preview SHA and attach this completed checklist to the release record.
