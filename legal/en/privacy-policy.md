---
document: privacy-policy
version: 1.0.0
effective_date: [[DA CONFERMARE: data di entrata in vigore, da fissare al lancio]]
language: en
---

# Privacy Policy

This policy explains what personal data Postqron processes, why, and what you can do
about it. It is written to be read, not to be survived.

## 1. Who is responsible

The controller of your personal data is
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy â VAT 03835250162, REA BG 431224.

You can reach us at
privacy@postqron.com.

We have not appointed a Data Protection Officer: our processing does not meet the
conditions of Art. 37 GDPR — we are not a public authority, our core activity is not
large-scale systematic monitoring, and we do not process special categories of data at
scale. Privacy requests go to the address above and are handled by us directly.

## 2. What we process, and why

### 2.1 Account and authentication

Email address, password (stored only as an Argon2id hash — we never hold the password
itself), preferred language, sessions and their expiry, and the tokens used to verify
your address or reset your password.

**Why:** to provide the service you asked for. **Legal basis:** performance of a
contract (Art. 6(1)(b) GDPR).

### 2.2 Jobs and executions

The schedules you define, the destination addresses, HTTP methods, headers and bodies
you configure, and for every execution: the time it started and ended, its duration,
the outcome, the HTTP status, a truncated extract of the response and the attempt
number.

Two things worth stating plainly. First, **you decide what goes into a job**: if you
put personal data in a URL, a header or a body, we will process it because you put it
there. Second, **response extracts are stored**, so if the system you call returns
personal data, that data reaches our logs.

**Why:** to run the service and to let you see what happened. **Legal basis:**
performance of a contract.

**Retention:** execution logs are kept for the period of your plan — 3, 15, 30 or 90
days — and then deleted.

### 2.3 Repository synchronisation

If you connect a GitHub repository, we process the repository identifier, the events
GitHub sends us when you push, and the content of the `cron.yaml` file. We request
read-only access to repository contents and metadata, and nothing else.

**Legal basis:** performance of a contract.

### 2.4 Secrets and credentials

Workspace secrets, API keys and AI provider keys are encrypted at rest, never returned
in readable form after they are saved, and never written to logs.

### 2.5 Billing

Payments are handled by Paddle as Merchant of Record (§4). We receive the subscription
status, plan and the identifiers needed to reconcile it. **We never see your payment
card.**

**Legal basis:** performance of a contract and legal obligation for tax records.

### 2.6 Security and audit

Records of sensitive events: sign-ins, changes of plan, key revocation, administrative
impersonation. Technical logs are structured to exclude secrets and personal data that
is not necessary.

**Legal basis:** legitimate interest in operating a secure service (Art. 6(1)(f)),
and legal obligation where applicable.

### 2.7 Transactional email

We send email you need in order to use the service: welcome, failed-job alerts, plan
changes, security events. These are not marketing and you cannot unsubscribe from them
without closing your account, because they are how the service tells you things.

### 2.8 Marketing email

If you agree to it, we send you email about the product: new features, changes worth
knowing about, occasionally something we have written.

**This is separate from the email above in every respect.** The legal basis is your
**consent** (Art. 6(1)(a)), asked for on its own and never bundled with accepting the
terms or creating an account. Refusing costs you nothing: the service works the same.

Every marketing message carries an unsubscribe link that works with one click and
without signing in. Unsubscribing stops marketing email only — you keep receiving the
transactional email the service needs to send you, because that is not marketing.

We keep a record of when you consented and when you withdrew, which is how we can show
that we had the right to write to you.

## 3. AI features: a transfer you should understand

If you enable AI-assisted debugging, you supply **your own** API key for an AI provider
(OpenAI, Anthropic or another). When you use the feature, the content of the execution
log you are analysing is sent to that provider under your key and their terms.

This means your data leaves our infrastructure and reaches a third party **that you
chose**, under a contract **between you and them**. We are not a party to it, we do
not control what they do with the content, and their retention rules apply, not ours.

The feature is off unless you turn it on, and each analysis is a deliberate action. We
ask for your explicit consent before the first transfer.

**Legal basis:** consent (Art. 6(1)(a)), which you can withdraw at any time by
removing your key. Withdrawal does not affect transfers already made.

## 4. Who else processes your data

We use these providers. Each processes data on our instructions, under a data
processing agreement.

| Provider | Role | Where |
|---|---|---|
| Hetzner | Servers and database | Germany |
| Cloudflare | DNS, TLS, CDN, static hosting, edge protection | Global edge network |
| Paddle | Merchant of Record: payments, invoicing, tax | United Kingdom |
| Mailronix | Transactional email delivery | European Union â operated by Apdsoftware, the same entity that operates Postqron |
| GitHub | Repository synchronisation, only if you connect one | United States |

We keep this list current. If we add or change a provider in a way that affects you,
we update this policy and, where the change is material, we tell you before it takes
effect.

**Transfers outside the EEA.** Some providers process data outside the European
Economic Area. Where that happens we rely on the safeguards in Art. 46 GDPR, primarily
the European Commission's Standard Contractual Clauses, together with the provider's
own technical measures.

## 5. How long we keep things

| Data | Kept |
|---|---|
| Account and profile | While the account exists |
| Execution logs | 3, 15, 30 or 90 days, by plan |
| Audit records | 24 months |
| Billing and tax records | As required by law, typically 10 years |
| Backups | 30 days |

When you delete your account we stop execution and revoke keys immediately, then
remove the data after a grace period of
30 days,
during which you can change your mind. Data already written to backups disappears as
those backups rotate out. Records we must keep for tax or legal reasons survive
deletion, and only those.

## 6. Your rights

You can ask us to give you a copy of your data, correct it, delete it, restrict or
object to its processing, or provide it in a portable format. You can withdraw consent
where processing is based on consent.

Export and deletion are available in the application without asking us. For anything
else, write to us and we will respond within one month.

If you believe we are handling your data wrongly, you can complain to your national
supervisory authority. In Italy that is the *Garante per la protezione dei dati
personali*.

## 7. Security

We encrypt secrets at rest, hash passwords with Argon2id, keep logs free of
credentials, verify the signature of incoming webhooks, rate-limit authentication, and
record sensitive events in an audit log.

We should also tell you what we do not have: Postqron runs on a single server, chosen
deliberately so that the scheduler and the database sit next to each other. That
choice trades resilience for latency. We take backups and we have tested restoring
them, but a failure of that machine interrupts the service.

## 8. Automated decisions

We do not make decisions with legal or similarly significant effects about you by
automated means, and we do not profile you.

## 9. Children

Postqron is not intended for people under
16.
We do not knowingly collect their data.

## 10. Changes

We may update this policy. The version and effective date are at the top. When a
change is material we tell you before it takes effect and, where the law requires it,
ask for your consent again.

---

**Contact:** privacy@postqron.com
**Operated by:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy â VAT 03835250162, REA BG 431224
