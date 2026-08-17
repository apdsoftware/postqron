---
document: terms-of-service
version: 1.0.0
effective_date: 2026-08-17
language: en
---

# Terms of Service

These terms govern your use of Postqron. By creating an account you accept them,
together with the [Acceptable Use Policy](acceptable-use-policy.md) and the
[Privacy Policy](privacy-policy.md).

## 1. Who you are contracting with

Postqron is operated by
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
("we", "us").

**Purchases are made through Paddle.** Paddle acts as Merchant of Record: when you buy
a paid plan, the sale contract for that purchase is between you and Paddle, and
Paddle's own buyer terms apply to it in addition to these terms. Paddle handles
payment, invoicing and tax. We handle the service.

## 2. What the service does

Postqron runs HTTP requests to addresses you configure, at times you configure,
records the outcome and notifies you of failures. Schedules can be defined in the
application or in a `cron.yaml` file in a repository you connect.

**Postqron does not execute your code.** It makes HTTP requests. If a request triggers
work on your systems, that work is yours.

## 3. Your account

You are responsible for what happens under your account, for keeping credentials
secure, and for the people you invite into your workspace. Tell us promptly if you
believe your account has been compromised.

You must be at least 16 years old and, if you act for an organisation, authorised to
bind it.

**The free plan is open to anyone.** Use it for a side project, to try the service, or
because it is enough for what you need. Nothing here asks you to be a business to
create an account.

**Paid plans are offered for professional use.** When you buy one, you confirm that
you are acting in the course of a trade, business, craft or profession. This is why
our prices are shown excluding VAT: for someone who runs a business, the net figure is
the one that matters, because it is the one you deduct. We ask you to confirm this at
checkout, and we collect your VAT number where you have one — some perfectly legitimate
small-business regimes across Europe do not issue one, so we ask for it, we do not
require it.

Where the law grants you consumer protections despite that confirmation, the law
wins — including the withdrawal rights in §4.3.

## 4. Plans, limits and payment

Plans, prices and limits are those published on our pricing page and applied by the
service. **Limits are enforced by the engine**, not merely stated: a plan's job count,
minimum interval and log retention are real ceilings.

Prices are shown **excluding VAT**. Paddle calculates and adds the applicable tax
based on where you are.

Paid plans renew automatically for the same period until cancelled. You can cancel at
any time; cancellation takes effect at the end of the period you have paid for, and
the service continues until then.

### 4.1 Changing plan

Upgrades take effect immediately. **Downgrades take effect at the end of the current
period**, and this matters: if your usage exceeds the limits of the lower plan — more
jobs than it allows, a shorter minimum interval, longer retention — we tell you before
you confirm what will happen and let you choose which jobs stay active.

**We do not silently delete your work.** Jobs beyond the new limit are disabled, not
removed, and remain visible and exportable.

### 4.2 Failed payment

If a payment fails, Paddle retries according to its own schedule. During that period
your service continues. If payment ultimately fails, the account moves to the free
plan, with the same rule as a downgrade: jobs beyond the limit are disabled, never
deleted.

### 4.3 Refunds and withdrawal

The rule is simple: **you can stop whenever you want, and the month you have already
paid for runs to its end.** Nothing is refunded pro rata, and there is nothing to
claim or negotiate.

If you are a consumer in the European Union you also have a statutory right to
withdraw within 14 days of purchase. Because the service starts immediately, you are
asked to consent to immediate performance; that consent ends the withdrawal right once
the service has been fully performed. Where the law still requires us to refund you,
we do, and Paddle processes it.

## 5. Availability

We aim to keep the service running continuously, and we will tell you when it is not
(see the Acceptable Use Policy for how we contact you about incidents).

**We do not offer an uptime guarantee, and we want to be straightforward about why.**
The scheduler and the database run on a single server, chosen deliberately so that
dispatch is not delayed by network latency. That choice trades resilience for
precision. We take backups and we test restoring them, but a failure of that machine
interrupts the service. Any commitment we made beyond what one machine can deliver
would be a commitment we could not keep.

If we ever offer a service level agreement with measurable commitments, it will appear
here — and the architecture will have changed first, not after.

## 6. Your content and ours

**Yours stays yours.** Your schedules, configuration, logs and the data you route
through the service remain your property. You grant us only the permission we need to
operate the service for you: to store that data, execute the requests you configure
and show you the results.

Postqron itself — the software, the interface, the name and the brand — remains ours.
These terms give you the right to use the service, not to copy or resell it.

## 7. Suspension and termination

We may suspend or terminate your account for a material breach of these terms or of
the Acceptable Use Policy, in the manner and with the notice described there.

You may close your account at any time. On closure we stop execution, revoke keys and
delete your data after the grace period stated in the Privacy Policy.

## 8. Liability

Nothing here limits liability that cannot be limited by law, including liability for
death or personal injury caused by negligence, for fraud, or the rights consumers have
under mandatory law.

Subject to that: we provide the service with reasonable skill and care, but we are not
liable for indirect or consequential loss, for loss of profit or business, or for the
consequences of the work your jobs trigger on your own systems. **A scheduled request
is not a guarantee that the work behind it succeeded**, and you should design your
systems on that assumption.

Beyond those exceptions, **our liability is excluded to the fullest extent permitted by
applicable law**.

We would rather say this plainly than bury it: Postqron is a scheduler priced from
zero to a few tens of euros a month, and it cannot carry the risk of what depends on
the jobs it runs. If a missed or duplicated execution would cause you material harm,
the service is not the right place to put that dependency, and no wording here changes
that engineering reality.

## 9. Changes to these terms

We may change these terms. When a change materially affects your rights we give you
30 days'
notice. If you do not accept the change, you may close your account before it takes
effect.

## 10. Governing law and jurisdiction

These terms are governed by
the laws of Italy.
Disputes are subject to the exclusive jurisdiction of
the courts of Bergamo, Italy,
**except** that if you are a consumer you keep the protection of the mandatory rules
of the country where you live, and may bring proceedings before your local courts.

---

**Contact:** hello@postqron.com
**Operated by:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
