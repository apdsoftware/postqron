---
document: subprocessors
locale: en
version: "0.1"
title: "Postqron Subprocessor Registry"
controllerName: "Apdsoftware di Carlo Zuffetti — operator of Postqron (trading as APDSoftware)"
contactEmail: help@postqron.com
status: draft_pending_legal_review
changeType: material
revisionSummary: "Initial from-scratch technical draft pending legal review."
---

## Provider identity

This registry is issued by Apdsoftware di Carlo Zuffetti (Via C. Colombo 15, 24047 Treviglio (BG), Italy, VAT number 03835250162), trading as APDSoftware, the operator of Postqron (entity information verified via a public source: https://mailronix.com/terms, consulted 2026-07-25), reachable at help@postqron.com and https://apdsoftware.it.

## Purpose of this registry

This is the public, regularly updated registry of the subprocessors and other third parties that Postqron engages to provide its service, referenced from the Terms of Service, Privacy Policy and Data Processing Agreement rather than duplicated in those documents. It distinguishes vendors acting as our subprocessors under Article 28 GDPR (processing personal data on Postqron's instructions) from independent third parties (such as OAuth identity providers) that act as their own controller for the step of the service they perform. Every entry below is built only from primary, official sources cited by URL, with the date each source was consulted. Where a fact could not be verified against an official source, that gap is stated explicitly rather than filled in.

Adding or replacing a subprocessor that will process customer content data follows the notice-and-objection process described in the Data Processing Agreement: at least 30 days' prior notice to workspace Owners, a channel to raise a reasoned objection, and suspension of activation for an objecting customer until the objection is resolved. A history of removed vendors is kept below the active table once any vendor is retired.

## Active subprocessors and third parties

| Legal name | Role | Service | Data categories | Establishment | Processing location | Transfer mechanism | DPA reference | Source (consulted 2026-07-25) |
|---|---|---|---|---|---|---|---|---|
| Paddle.com Market Limited (contracting entity); Paddle.com Inc. (DPA processor); Paddle Payments Limited; Paddle.com Canada Ltd | Subprocessor | Payment processing and Merchant of Record billing | Billing contact data; subscription/transaction metadata | United Kingdom; Ireland; United States; Canada | Not disclosed by Paddle; may be processed by any Paddle group entity | Standard Contractual Clauses | [Paddle Data Processing Addendum](https://www.paddle.com/legal/data-processing-addendum) | [Paddle DPA](https://www.paddle.com/legal/data-processing-addendum) |
| Hetzner Online GmbH | Subprocessor | Cloud hosting infrastructure (compute, storage, backups) | Account data; workspace and content data; encrypted backups | Germany | European Union/EEA when an EU server location is selected, consistent with Postqron's EU/EEA-first hosting preference | EU/EEA processing (no third-country transfer when an EU location is used) | [Hetzner Auftragsverarbeitungsvertrag (DPA)](https://www.hetzner.com/AV/DPA_en.pdf) | [Hetzner DPA](https://www.hetzner.com/AV/DPA_en.pdf) |
| Cloudflare, Inc. | Subprocessor | DNS, CDN, edge network and TLS termination | Network and traffic metadata; IP addresses | United States | Global edge network; may process outside the EEA, Switzerland and the UK depending on configured services | Standard Contractual Clauses (also EU-US Data Privacy Framework and Global CBPR certified) | [Cloudflare Customer DPA](https://www.cloudflare.com/cloudflare-customer-dpa/) | [Cloudflare DPA](https://www.cloudflare.com/cloudflare-customer-dpa/) |
| Apdsoftware di Carlo Zuffetti (operates mailronix.com) | Subprocessor | Transactional email delivery (account, security and service notifications) | Recipient email address; recipient name; transactional message content | Italy (Via C. Colombo 15, 24047 Treviglio (BG)) | Germany (primary infrastructure on Hetzner; email delivery via AWS SES, Frankfurt) | EU/EEA processing | DPA stated to be an integral part of mailronix.com's Terms; no separate DPA URL published | [mailronix.com Terms](https://mailronix.com/terms) |
| Google LLC; Google Ireland Limited | Independent third party | OAuth login ("Sign in with Google") | Email address; display name; profile picture; Google account identifier | United States; Ireland | Global | EU-US and Swiss-US Data Privacy Framework; Standard Contractual Clauses where the Framework does not apply | Not applicable — no dedicated DPA published for this feature | [Google APIs Terms of Service](https://developers.google.com/terms) |
| Apple Inc.; Apple Distribution International Limited (for EEA-relevant purposes) | Independent third party | OAuth login ("Sign in with Apple") | Email address (or Apple private relay email); name (first sign-in only); Apple account identifier | United States; Ireland (Cork) | Ireland (Cork), for EEA-relevant processing by Apple Distribution International Limited | Not applicable — no transfer mechanism stated in the official sources reviewed | Not applicable — no dedicated DPA published for this feature | [Sign in with Apple & Privacy](https://www.apple.com/legal/privacy/data/en/sign-in-with-apple/); [Irish LEI register](https://lei-ireland.ie/detailed-information/588229/54930027SQL2KPSDBM58/apple-distribution-international-limited/) |
| Meta Platforms, Inc.; Meta Platforms Ireland Limited | Independent third party | OAuth login ("Facebook Login") and the customer's own connection to Facebook Pages / Instagram Professional as a publishing destination | Email address; name; profile picture; Facebook/Instagram account identifier; content the customer chooses to publish to their connected account | Ireland (Dublin); United States | Ireland (Dublin), for EEA-relevant processing by Meta Platforms Ireland Limited | Standard Contractual Clauses; Meta Platforms, Inc. also Data Privacy Framework certified | Not applicable — no dedicated DPA published for this feature | [Meta Platform Terms](https://developers.facebook.com/terms/dfc_platform_terms/) |
| LinkedIn Corporation; LinkedIn Ireland Unlimited Company | Independent third party | OAuth login ("Sign in with LinkedIn") | Email address; name; profile picture; LinkedIn account identifier | United States; Ireland | United States | Standard Contractual Clauses; LinkedIn Corporation also Data Privacy Framework certified | Cross-referenced only — LinkedIn's Business Development DPA is linked from its API Terms of Use but does not itself name this feature | [LinkedIn API Terms of Use](https://www.linkedin.com/legal/l/api-terms-of-use) |

## Known gaps requiring resolution before publication

- **Mailronix / Apdsoftware di Carlo Zuffetti**: confirmed that mailronix.com is operated by the same legal entity that operates Postqron, Apdsoftware di Carlo Zuffetti (Via C. Colombo 15, 24047 Treviglio (BG), Italia, P.IVA 03835250162; source: https://mailronix.com/terms, consulted 2026-07-25). Because this is the same legal entity, not an independent third party, this is not an ordinary arm's-length subprocessor relationship under Art. 28 GDPR — an entity cannot be its own sub-processor. This entry remains listed here for transparency of the processing location/technology used; the precise legal characterization of this internal data flow (e.g. as an internal processing location rather than a formal subprocessor) remains to be finalized during legal review.
- **LinkedIn's applicability of its Business Development DPA to OAuth login specifically** is inferred by cross-reference only and should be confirmed directly with LinkedIn or counsel.
- **Paddle's Trust Center subprocessor list** (a JavaScript-rendered page) could not be read by automated research and its contents are not independently verified; the DPA text itself also references an outdated legacy sub-processor list link that should be clarified with Paddle.

## Removed subprocessors

None recorded as of this draft's revision.

## Contact

Questions about this registry, or objections to a listed subprocessor under the Data Processing Agreement, go to help@postqron.com.
