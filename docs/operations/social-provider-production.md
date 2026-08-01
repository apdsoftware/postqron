# F5 social providers in production

This runbook is the authoritative F5/F16 handoff for production social OAuth.
It contains names and expected states only. Never paste credential values into
issues, pull requests, workflow logs, screenshots, or support messages.

## Confirmed diagnosis

The production failure had two independent causes:

1. `RUNTIME_ENV` was copied to the host and used for Docker Compose
   interpolation, but `infra/deploy/compose.yaml` did not forward any
   `POSTQRON_F05_*` value to the API container. F5 therefore bootstrapped every
   catalog entry as `not_configured`, even if social values were present in the
   GitHub Environment secret.
2. GitHub Environment metadata on 2026-07-31 showed the production
   `RUNTIME_ENV` secret but no independently verifiable F5 secret names. GitHub
   does not permit reading secret values back, so repository access cannot
   prove that valid provider credentials or approvals are currently present.

The public relay and server callback were both reachable during diagnosis:

- `https://postqron.com/app/social-oauth/callback` redirected to the localized
  public relay and returned HTTP 200 without OAuth parameters;
- `https://api.postqron.com/api/v1/social-authorizations/callback` returned the
  expected fail-closed HTTP 409 for missing state;
- `https://api.postqron.com/healthz` returned HTTP 200.

These probes prove routing only. They do not prove a provider credential,
provider-console registration, authorization-code exchange, or channel
creation.

## Canonical redirect

Every enabled provider must use this exact, unlocalized production redirect,
with no trailing slash:

```text
https://postqron.com/app/social-oauth/callback
```

The browser relay removes OAuth parameters from history and forwards only the
allowed response fields to the authoritative API callback. Do not register the
API-domain callback in a provider console.

The deploy validator derives the callback from `APP_DOMAIN`; for the
`production` GitHub Environment it also requires `APP_DOMAIN=postqron.com`.
Every configured `POSTQRON_F05_*_REDIRECT_URL` must equal the derived callback
or the release stops before upload.

## GitHub Environment secret boundary

The existing `RUNTIME_ENV` GitHub Environment secret is opaque and must remain
the source of all legacy runtime entries. The release workflow copies it
without reconstructing it, atomically appends an allowlisted dedicated F5/X
inventory, validates the composed file, and uploads it with mode `0600`. It
never prints values. Any dedicated key already present in `RUNTIME_ENV` stops
the release before the source file is changed or uploaded; operators must not
guess, overwrite, or discard unrelated legacy entries to resolve a conflict.

The protected `production` GitHub Environment secrets are:

| Secret | Required state |
| --- | --- |
| `POSTQRON_F05_X_CLIENT_ID` | X OAuth client ID; stored as a secret even though it is an identifier |
| `POSTQRON_F05_X_CLIENT_SECRET` | X OAuth client secret |
| `POSTQRON_F05_CIPHER_KEY_BASE64` | base64 of exactly 32 random bytes; shared by F5 credential encryption |

As of 2026-07-31 the two X credential secrets are present, transferred from
the approved Keychain without disclosure. `POSTQRON_F05_CIPHER_KEY_BASE64` is
absent from both Keychain and GitHub. A production release is intentionally
blocked until the one-time creation procedure below is completed; do not
invent a placeholder and do not reuse an unrelated encryption key.

The non-secret production GitHub Environment variables are:

| Variable | Required value before first smoke |
| --- | --- |
| `POSTQRON_F05_ENABLED` | exact `true` |
| `POSTQRON_F05_CIPHER_KEY_ID` | stable, unique key identifier/version |
| `POSTQRON_F05_X_ENABLED` | exact `true` |
| `POSTQRON_F05_X_REDIRECT_URL` | canonical redirect above |
| `POSTQRON_F05_X_API_ACCESS_APPROVED` | exact `true` only with recorded approval evidence |
| `POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED` | exact `true` only after the offline runtime/security audit |
| `POSTQRON_F05_X_SMOKE_TEST_VERIFIED` | exact `false` until the real production smoke succeeds |
| `POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED` | exact `true` only for the controlled first-smoke release |
| `POSTQRON_F05_X_FIRST_SMOKE_CANARY_WORKSPACE_ID` | exact dedicated test workspace ID |
| `POSTQRON_F05_X_FIRST_SMOKE_CANARY_ACTOR_ACCOUNT_ID` | exact authorized Owner account ID |
| `POSTQRON_F05_X_FIRST_SMOKE_CANARY_EXPIRES_AT` | UTC `YYYY-MM-DDTHH:MM:SSZ`, future and at most two hours from release validation |

Other provider families may remain in the legacy secret until they receive a
separately reviewed migration. Unknown and duplicate F5 keys remain invalid.

### One-time F5 cipher creation

This is a manual, security-sensitive bootstrap step, not part of this change.
An authorized operator must perform it once on an approved workstation:

1. Create a mode-`0600` temporary file outside the repository, set a restrictive
   umask, and write 32 bytes of cryptographically secure random material as
   standard base64. Do not put the value in a command argument, clipboard,
   issue, PR, log, or shell history.

   ```sh
   umask 077
   openssl rand 32 | openssl base64 -A \
     > /secure/path/postqron-f05-cipher-key.base64
   chmod 0600 /secure/path/postqron-f05-cipher-key.base64
   test "$(wc -c < /secure/path/postqron-f05-cipher-key.base64 | tr -d ' ')" = 44
   ```

2. Store the new value in the approved durable secret manager first. Record
   the non-secret key ID, owner, creation date, recovery procedure, and review
   date. Verify recovery access before proceeding.
3. Send the file through standard input to the protected Environment secret:

   ```sh
   gh secret set POSTQRON_F05_CIPHER_KEY_BASE64 \
     --repo apdsoftware/postqron \
     --env production \
     < /secure/path/postqron-f05-cipher-key.base64
   ```

4. Remove the temporary plaintext using the organization's approved secret
   handling procedure and confirm only the secret name—not its value—appears
   in GitHub Environment metadata.

The cipher is a long-lived encryption root. Losing it makes stored F5 tokens
and outstanding OAuth state unrecoverable. Replacing or rotating it without a
reviewed data migration has the same effect and can strand connected channels.
Back it up securely; do not create a second value if its availability is
uncertain, and never rotate it as an incident workaround without an explicit
migration/revocation plan.

### Meta: Facebook Pages and Instagram Professional

`POSTQRON_F05_META_ENABLED=true` and
`POSTQRON_F05_META_GRAPH_VERSION` are shared by Facebook Pages and Instagram
Professional. A provider is considered requested when any of its credential
or redirect entries is present; partial configuration blocks release.

| Provider | Identity/config entries | Secret entry | Approval gates |
| --- | --- | --- | --- |
| Facebook Pages | `POSTQRON_F05_FACEBOOK_CLIENT_ID`, `POSTQRON_F05_FACEBOOK_REDIRECT_URL`, `POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID` | `POSTQRON_F05_FACEBOOK_CLIENT_SECRET` | `POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED=true`, `POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED=true` |
| Instagram Professional | `POSTQRON_F05_INSTAGRAM_CLIENT_ID`, `POSTQRON_F05_INSTAGRAM_REDIRECT_URL` | `POSTQRON_F05_INSTAGRAM_CLIENT_SECRET` | `POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED=true`, `POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED=true` |

Facebook Groups and Instagram Personal are notification-only catalog resources.
They intentionally have no connectable OAuth adapter and remain fail-closed.

### Static OAuth providers

Each provider gate must be exact `true`. Every redirect entry in this table is
the canonical redirect above.

| Provider | Enable/config entries | Secret entry | Approval/audit gates |
| --- | --- | --- | --- |
| Threads | `POSTQRON_F05_THREADS_ENABLED`, `POSTQRON_F05_THREADS_CLIENT_ID`, `POSTQRON_F05_THREADS_REDIRECT_URL` | `POSTQRON_F05_THREADS_CLIENT_SECRET` | `POSTQRON_F05_THREADS_APP_REVIEW_APPROVED`, `POSTQRON_F05_THREADS_RUNTIME_AUDIT_VERIFIED` |
| X | `POSTQRON_F05_X_ENABLED`, `POSTQRON_F05_X_CLIENT_ID`, `POSTQRON_F05_X_REDIRECT_URL` | `POSTQRON_F05_X_CLIENT_SECRET` | `POSTQRON_F05_X_API_ACCESS_APPROVED`, `POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED`, `POSTQRON_F05_X_SMOKE_TEST_VERIFIED` |
| LinkedIn | `POSTQRON_F05_LINKEDIN_ENABLED`, `POSTQRON_F05_LINKEDIN_CLIENT_ID`, `POSTQRON_F05_LINKEDIN_REDIRECT_URL`, `POSTQRON_F05_LINKEDIN_API_VERSION` (`YYYYMM`) | `POSTQRON_F05_LINKEDIN_CLIENT_SECRET` | `POSTQRON_F05_LINKEDIN_REVIEW_APPROVED`, `POSTQRON_F05_LINKEDIN_RUNTIME_AUDIT_VERIFIED`, `POSTQRON_F05_LINKEDIN_SMOKE_VERIFIED`; set `POSTQRON_F05_LINKEDIN_PROGRAMMATIC_REFRESH_APPROVED=true` only with explicit partner approval |
| Pinterest | `POSTQRON_F05_PINTEREST_ENABLED`, `POSTQRON_F05_PINTEREST_CLIENT_ID`, `POSTQRON_F05_PINTEREST_REDIRECT_URL` | `POSTQRON_F05_PINTEREST_CLIENT_SECRET` | `POSTQRON_F05_PINTEREST_ACCESS_APPROVED`, `POSTQRON_F05_PINTEREST_RUNTIME_AUDIT_VERIFIED` |
| TikTok | `POSTQRON_F05_TIKTOK_ENABLED`, `POSTQRON_F05_TIKTOK_CLIENT_KEY`, `POSTQRON_F05_TIKTOK_REDIRECT_URL` | `POSTQRON_F05_TIKTOK_CLIENT_SECRET` | `POSTQRON_F05_TIKTOK_CONTENT_POSTING_REVIEW_APPROVED`, `POSTQRON_F05_TIKTOK_CONTENT_POSTING_AUDIT_APPROVED`, `POSTQRON_F05_TIKTOK_RUNTIME_SMOKE_VERIFIED`, `POSTQRON_F05_TIKTOK_QUOTA_ACCESS_CONFIRMED` |
| YouTube | `POSTQRON_F05_YOUTUBE_ENABLED`, `POSTQRON_F05_YOUTUBE_CLIENT_ID`, `POSTQRON_F05_YOUTUBE_REDIRECT_URL` | `POSTQRON_F05_YOUTUBE_CLIENT_SECRET` | `POSTQRON_F05_YOUTUBE_OAUTH_VERIFICATION_APPROVED`, `POSTQRON_F05_YOUTUBE_API_AUDIT_APPROVED`, `POSTQRON_F05_YOUTUBE_RUNTIME_SMOKE_VERIFIED`, `POSTQRON_F05_YOUTUBE_QUOTA_ACCESS_CONFIRMED` |
| Google Business Profile | `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_ENABLED`, `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_ID`, `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_REDIRECT_URL` | `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_SECRET` | `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_API_ACCESS_APPROVED`, `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_OAUTH_REVIEW_APPROVED`, `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_RUNTIME_AUDIT_VERIFIED`, `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_SMOKE_VERIFIED` |

### Dynamic OAuth providers

| Provider | Required entries | External secret |
| --- | --- | --- |
| Mastodon | `POSTQRON_F05_MASTODON_ENABLED=true`, `POSTQRON_F05_MASTODON_REDIRECT_URL`, `POSTQRON_F05_MASTODON_RUNTIME_AUDIT_VERIFIED=true`, `POSTQRON_F05_MASTODON_RUNTIME_SMOKE_VERIFIED=true`, `POSTQRON_F05_MASTODON_COMPATIBILITY_VERSION=f05_dynamic_runtime_v1` | none; the adapter registers an app per instance and stores returned credentials only in encrypted F5 state |
| Bluesky | `POSTQRON_F05_BLUESKY_ENABLED=true`, HTTPS `POSTQRON_F05_BLUESKY_CLIENT_ID`, `POSTQRON_F05_BLUESKY_REDIRECT_URL`, `POSTQRON_F05_BLUESKY_RUNTIME_AUDIT_VERIFIED=true`, `POSTQRON_F05_BLUESKY_RUNTIME_SMOKE_VERIFIED=true`, `POSTQRON_F05_BLUESKY_COMPATIBILITY_VERSION=f05_dynamic_runtime_v1`; optional HTTPS `POSTQRON_F05_BLUESKY_PLC_DIRECTORY_ORIGIN` | none; the client ID resolves to public OAuth client metadata |

## Provider-console checklist

Perform these actions only from an authorized operator account. Keep evidence
in the approved private operations system, not in GitHub comments.

1. For every provider being enabled, confirm the production application is in
   the correct live/production mode and belongs to the expected organization.
2. Register the canonical redirect exactly. Remove stale API-domain,
   localized, HTTP, trailing-slash, preview, and localhost variants unless the
   provider requires a separately isolated non-production application.
3. Confirm the least-privilege products/scopes documented in the provider's
   F5 runbook:
   Facebook Login for Business and Pages permissions; Instagram Business
   Login; Threads publishing; X write/offline access; LinkedIn member and
   organization publishing; Pinterest content access; TikTok Direct Post;
   YouTube upload; and Google Business Profile management.
4. Confirm every review, Advanced Access, audit, quota, active-user limit, and
   organization-verification prerequisite represented by a gate above. Never
   set a gate to `true` merely because a credential exists.
5. For Bluesky, publish HTTPS client metadata whose redirect URI is exactly the
   canonical redirect. For Mastodon, confirm dynamic app registration is
   allowed on the target test instance; no shared client secret is configured.
6. Record application ID, approval date, evidence owner, and next review date
   privately. Do not record client secrets or tokens.

## Safe GitHub update procedure

1. Do not fetch, replace, or reconstruct `RUNTIME_ENV`. GitHub cannot return
   its value and it may contain unrelated production configuration.
2. Complete the one-time cipher procedure above. Set the three dedicated
   secrets only through protected GitHub Environment secret input. Never paste
   a secret value into a variable, workflow input, issue, or PR.
3. Set the non-secret variables from the table above. Use the exact dedicated
   test workspace and Owner account IDs. Set the canary expiry shortly before
   dispatch; it must be in UTC, in the future, and no more than two hours away.
4. Keep `POSTQRON_F05_X_SMOKE_TEST_VERIFIED=false`. A credential, approval,
   release, reachable callback, or mocked test is not smoke evidence.
5. Review GitHub Environment metadata by name only and retain the variable
   change/deployment approvals as operational evidence. Do not echo values.
6. Dispatch no release until the implementation is merged and all protected
   Environment reviewers approve it. The composer will reject any overlap
   between the dedicated inventory and legacy `RUNTIME_ENV` before upload.

If overlap is reported, stop. Resolve it only from an authoritative recovered
copy of the legacy secret, with a separate reviewed change that preserves every
unrelated byte/entry. Do not delete or replace `RUNTIME_ENV` based on inference
from a workflow error.

## Release and non-destructive verification

Do not dispatch production until the change is merged and provider-console
work is complete. The release workflow then:

1. rejects unknown or duplicate F5 entries, invalid booleans, partial enabled
   provider configuration, wrong callbacks, missing gates, unsupported
   compatibility versions, and invalid cipher material; a configuration with
   no enabled provider remains valid but leaves the whole catalog fail-closed;
2. forwards the complete allowlisted F5 inventory explicitly to the API
   container while keeping absent providers fail-closed;
3. checks the public relay and the API's missing-state rejection after restart,
   without creating an OAuth attempt or channel.

### Controlled first-smoke sequence for X

The first release uses the canary variables above and keeps
`POSTQRON_F05_X_SMOKE_TEST_VERIFIED=false`. X remains `audit_required` in the
global catalog. Only the exact canary workspace and actor see X as available
while that actor still has Owner-equivalent channel-management permission, and
may create an OAuth attempt before expiry. A mismatched, demoted, or expired
request receives the normal `audit_required` catalog and cannot begin. The
attempt persists provider, workspace, actor, and timestamps, and
connect/disconnect actions use the existing F5 event audit trail. F8 token
access/publishing remains unavailable until the normal smoke gate is true.

The authorized canary Owner must then perform the real smoke test:

1. open Social channels in production and confirm the F5 bootstrap marks only
   approved/configured providers `available`;
2. choose **Add channel** for one enabled provider and confirm the browser opens
   that provider's real authorization page with the production application;
3. deny the first attempt and confirm the product shows a useful retryable
   provider-denied error without creating a channel;
4. repeat, authorize a dedicated non-customer test resource, select it, and
   confirm exactly one connected channel appears;
5. disconnect that test channel and confirm local removal and the documented
   provider revocation behavior;
6. confirm disabled providers remain `Non disponibile` and expose no missing
   secret name or value.

If any step fails, leave `POSTQRON_F05_X_SMOKE_TEST_VERIFIED=false`, capture
non-secret diagnostics privately, disconnect any created test channel, and let
the canary expire. Expiry closes begin/callback/selection automatically;
disconnect cleanup remains possible. An API restart with the expired runtime
stays healthy and retains the X adapter only for cleanup/revocation; it does
not mount normal X or reopen catalog, OAuth, selection, or token access. Deploy
validation still rejects that expired timestamp for any new release. Clear or
disable the four canary variables before a later release.

Only after every step succeeds may an authorized operator set
`POSTQRON_F05_X_SMOKE_TEST_VERIFIED=true`, set
`POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED=false`, clear the workspace, actor,
and expiry variables, and obtain review for a second release. Validation
rejects a verified gate that retains canary scope. That second release makes X
available through the normal catalog; the first canary release never does so
for other users.

This final smoke requires real external credentials and an authorized test
account. Repository fixtures, mocked HTTP responses, callback reachability,
and a successful deployment are regression evidence only; none proves that
production OAuth works.
