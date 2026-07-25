# Operational support email runbook

`help@postqron.com` is the single public operational support address. Cloudflare
Email Routing forwards inbound mail to a verified destination mailbox, and
Cloudflare Email Sending provides authenticated outbound SMTP/API delivery from
the alias. This channel is separate from F14 transactional and marketing email.

## Ownership and service objective

| Responsibility | Accountable role | Duties |
| --- | --- | --- |
| Owner | APDSoftware Support Lead | Daily queue, replies, access review, delivery probes |
| Backup owner | APDSoftware Operations Lead | Holiday cover, failover, owner revocation |
| Escalation | APDSoftware Product Owner | Priority, customer-impact and policy decisions |
| Security escalation | APDSoftware Security/Incident Lead | Abuse, phishing, leaked credentials, personal-data incidents |

The named people currently filling these roles and their verified destination
addresses belong in the access-controlled operations register, not Git. The
Support Lead acknowledges normal requests within one business day. A P1
(service unavailable, suspected compromise, or material data-loss risk) is
acknowledged within 30 minutes during on-call coverage and immediately enters
the incident process.

Minimum triage is: acknowledge, assign priority and owner, remove spam, link any
existing incident, answer or escalate, then close with the outcome and retention
date. P1 goes to the Incident Lead; billing/privacy/legal requests go to the
Product Owner and designated specialist; product defects receive a GitHub issue
without copying secrets or unnecessary personal data.

## Security and data handling

- Cloudflare and both destination mailbox accounts require phishing-resistant
  MFA where supported. Shared passwords and shared user accounts are forbidden.
- The administrative provisioning token has only Zone DNS Edit, Email Routing
  Addresses Write, Email Routing Rules Write, and Email Sending Edit for
  `postqron.com`. The outbound token has only Email Sending Edit. Both live in
  the production secret store and are injected into the operator process.
- Never pass a token on the command line, enable shell tracing, paste it in a
  ticket, or store it in a config file. Scripts print neither authorization
  headers nor provider responses containing credentials.
- Provider spam filtering and the destination mailbox's malware/phishing
  filtering stay enabled. Do not create allow-all rules. Quarantine is reviewed
  each business day without opening active content outside the protected client.
- Support messages are retained for 24 months after closure, then deleted from
  the destination mailbox and trash within 30 days. A documented legal hold
  suspends deletion only for the scoped thread. DMARC aggregate reports are kept
  for 90 days. Do not copy message bodies into telemetry.
- Review destination membership and provider audit logs quarterly and after
  every personnel change. Export only redacted delivery evidence to the change
  ticket.

## Repeatable provisioning

Cloudflare Email Routing requires verified destination addresses and permits one
forward destination per rule. The owner is active; the independently verified
backup is warm standby. This avoids duplicate replies while preserving a tested
failover path.

1. Copy `infra/support-email/config.example.env` to a protected local file.
   Replace public IDs and destination addresses. Do not add tokens.
2. Create the least-privilege administrative token in Cloudflare and retrieve it
   directly from the secret store into the current process:

   ```sh
   read -r -s CLOUDFLARE_EMAIL_ADMIN_TOKEN
   export CLOUDFLARE_EMAIL_ADMIN_TOKEN
   ```

3. Review the credential-free plan:

   ```sh
   infra/support-email/provision.sh --config /secure/path/support-email.env
   ```

4. Apply once. Cloudflare sends verification messages to both destinations:

   ```sh
   infra/support-email/provision.sh \
     --config /secure/path/support-email.env \
     --apply
   ```

5. Each owner signs in with MFA and completes their own verification. Rerun the
   same apply command. It enables the `help` and `dmarc` routes, onboards the
   sending domain, and sets one strict-alignment DMARC record. It never changes
   the zone catch-all.
6. Create a separate Email Sending Edit token for the mail client/API, store it
   as `CLOUDFLARE_EMAIL_SEND_TOKEN`, and configure outbound SMTP as
   `smtps://smtp.mx.cloudflare.net:465`, username `api_token`, from address
   `help@postqron.com`. Retrieve the password from the secret store; never save
   it in an unmanaged client.

Use `--target backup --apply` for planned cover or owner lockout. The command
updates both `help` and DMARC routes in one operator run. Run verification
immediately afterward and record the change. Returning to the owner uses
`--target owner --apply`.

Cloudflare is the only support-email sender authorized here. If F14 Mailrox
begins sending aligned mail from the same organizational domain, reconcile the
single SPF/DMARC policy in a separate reviewed infrastructure change before
enforcement; never publish a second SPF record.

## Acceptance and recurring tests

Run local validation first:

```sh
infra/support-email/tests/test.sh
shellcheck infra/support-email/*.sh infra/support-email/tests/*.sh
infra/support-email/provision.sh --config /secure/path/support-email.env
```

After apply and after every DNS/provider change:

```sh
infra/support-email/verify.sh \
  --config /secure/path/support-email.env \
  --check-open-relay
```

The DNS test requires exactly the three Cloudflare MX records, one root SPF,
separate sending SPF, sending DKIM, and one reviewed DMARC record. With the
administrative token present it also requires routing ready, both owners
verified, an enabled `help` rule, and an enabled sending domain. The relay probe
must reject an unauthenticated external-to-external SMTP transaction.

Complete this two-provider delivery matrix and attach redacted evidence to the
private change ticket:

1. From an external provider A, send a unique probe to `help@postqron.com`.
   Confirm one copy arrives at the active destination and spam/authentication
   headers are present.
2. Reply from the support client through authenticated Cloudflare SMTP. Confirm
   provider A sees `From: help@postqron.com`, aligned SPF or DKIM and DMARC pass.
3. Run `send-test.sh --to` against an external provider B and confirm delivery:

   ```sh
   infra/support-email/send-test.sh \
     --config /secure/path/support-email.env \
     --to external-probe@example.net
   ```

4. Reply from B. Confirm the `Reply-To` path returns once to the active support
   mailbox, retains the external sender/thread headers, and creates no
   auto-forward or auto-reply loop. Never configure `help@postqron.com` as its
   own forwarding destination.
5. Record UTC time, provider, redacted message ID, `Authentication-Results`,
   inbox/spam placement and loop count. Do not record message bodies or tokens.

Repeat the external matrix quarterly and after SPF, DKIM, DMARC, routing, token,
or destination changes.

## Rotation, revocation, and incidents

Rotate both tokens every 90 days and immediately after suspected exposure.
Create the replacement with identical least privilege, update the secret store,
test outbound and provisioning, then revoke the old token and review Cloudflare
audit logs. Rotation is not complete until the old token fails verification.

When an owner leaves or loses the role, the backup owner first switches the
route to `--target backup`, revokes the person's destination-mailbox sessions
and app passwords, removes their provider role, rotates tokens they could
access, and performs the full test matrix. A new owner uses a new identity with
MFA; never transfer an account.

For missing mail, check routing status/activity, destination quarantine, MX and
authentication records, then run `verify.sh`. For outbound failure, stop retries
on permanent bounces, inspect provider status and redacted message IDs, and test
with both external providers. For spam bursts, credential misuse, loops, or
domain spoofing, disable the affected client/token, preserve audit evidence,
page the Incident Lead, and follow the incident runbook. Do not weaken DMARC or
spam controls as a workaround.

## Dismission

Obtain Product Owner and Security approval, export only records covered by
retention/legal hold, notify active correspondents through an approved
replacement channel, and stop new replies. Disable the two explicit routing
rules, revoke outbound and administrative tokens, remove both destination
addresses only after confirming no other zone uses them, and disable Email
Sending. Remove MX/SPF/DKIM/DMARC only after a DNS inventory proves no authorized
mail service still depends on them. Preserve the redacted change record and
verify that inbound mail rejects cleanly and unauthenticated relay remains
impossible.
