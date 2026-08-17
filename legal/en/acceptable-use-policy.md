---
document: acceptable-use-policy
version: 1.0.0
effective_date: 2026-08-17
language: en
---

# Acceptable Use Policy

This policy is part of the [Terms of Service](terms-of-service.md). It describes what
you may not do with Postqron, and what happens when you do it.

Postqron sends HTTP requests to addresses you choose, on a schedule you choose, from
our infrastructure and our IP addresses. That capability is useful, and it is also the
capability an attacker wants. This policy exists so that the difference between the
two is written down rather than left to judgement.

## 1. Who this applies to

Everyone who uses Postqron, on every plan, including the free plan. It also applies to
anyone you invite into your workspace: you are responsible for their use of the
service.

## 2. What you must not do

### 2.1 Attack, overload or probe systems

You must not use Postqron to:

- send requests to a system you do not own or are not explicitly authorised to test;
- generate load intended to degrade, exhaust or deny service to any system, including
  through high-frequency schedules, many jobs against a single target, or coordinated
  use of multiple accounts;
- scan, enumerate or probe hosts, ports, paths or credentials;
- reach systems that are not intended to be publicly reachable, including private
  networks, loopback addresses, cloud metadata endpoints and internal services — ours
  or anyone else's.

Authorisation matters more than intent. Scheduling a request against a third party's
endpoint is not made acceptable by calling it a health check.

### 2.2 Circumvent our controls

You must not attempt to bypass the technical measures that enforce this policy,
including address filtering, rate limits, plan limits or execution ceilings. This
includes using redirects, DNS entries under your control, or proxies to reach a
destination we would otherwise refuse.

### 2.3 Use the service unlawfully or abusively

You must not use Postqron to break the law, to infringe someone's rights, to
distribute malware, to send unsolicited messages, or to process content that is
unlawful in the jurisdictions where you or your recipients are.

### 2.4 Misrepresent origin

You must not present requests originating from Postqron as coming from someone else,
or use the service to conceal the origin of activity.

### 2.5 Resell or expose the service as your own

You must not offer Postqron's execution capability to third parties as a service of
your own without a written agreement. Running jobs on behalf of your own clients
within an Agency workspace is expected and permitted; building a product on top of our
scheduler and selling it is not.

## 3. Shared resources

Outbound requests leave from IP addresses shared by all customers, except where a plan
includes a dedicated address. The reputation of those addresses is a shared asset: one
customer's abuse degrades the service for everyone. We enforce this policy to protect
other customers, not to police you.

We may apply aggregate limits per destination host, and we may refuse or slow requests
to a destination that shows signs of being targeted rather than served.

## 4. What we do about violations

Where the situation allows, we contact you first and give you a chance to fix it.
Where it does not — because harm is ongoing, because a third party is being attacked,
or because we are legally required to act — we may act immediately and tell you
afterwards.

Depending on severity we may:

1. **throttle or block** specific jobs or destinations;
2. **suspend** the affected jobs while leaving your account otherwise usable;
3. **suspend the account**, stopping all execution;
4. **terminate** the account.

We suspend the narrowest thing that stops the harm. Suspension is not a refund event:
see the Terms.

Where we suspend or terminate, you keep the right to export your data
for 30 days,
unless doing so is unlawful.

## 5. Reporting abuse

If you believe someone is using Postqron to attack or abuse a system you are
responsible for, write to
abuse@postqron.com.
Include the destination address, timestamps in UTC and, where available, the source IP.
We investigate reports and will confirm receipt
within two working days.

## 6. Changes

We may update this policy. When a change materially restricts what is permitted, we
give you
30 days'
notice before it takes effect, except where a shorter period is required to stop
ongoing harm or to comply with law.

---

**Contact:** hello@postqron.com
**Operated by:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
