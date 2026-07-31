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

Production F5 values live inside the existing GitHub Environment secret
`RUNTIME_ENV`. This keeps the social configuration isolated from browser
configuration while preserving the existing encrypted, mode-`0600` delivery
path. The workflow and validator never print values.

The shared F5 entries are mandatory whenever any provider is enabled:

| Entry in `RUNTIME_ENV` | Classification | Required value |
| --- | --- | --- |
| `POSTQRON_F05_ENABLED` | gate | exact `true` |
| `POSTQRON_F05_CIPHER_KEY_ID` | non-secret identifier | non-empty key version/identifier |
| `POSTQRON_F05_CIPHER_KEY_BASE64` | secret | base64 of exactly 32 random bytes |

Rotate the cipher only with a reviewed migration for existing encrypted F5
credentials and outstanding attempts. Replacing it without migration makes
stored provider sessions unreadable.

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

1. On an authorized workstation, obtain the current `RUNTIME_ENV` from the
   approved password/secret manager. GitHub cannot return its current value.
2. Write a temporary mode-`0600` file outside the repository. Preserve the
   existing database and unrelated runtime entries; add the exact F5 entries
   above as unquoted `NAME=value` lines with no surrounding whitespace. Do not
   put the file in shell history.
3. Configure at least one complete provider. Providers left disabled stay
   unavailable. Do not add unknown `POSTQRON_F05_*` keys.
4. Validate locally without printing values:

   ```sh
   ./infra/deploy/validate-f05-runtime.sh \
     /secure/path/runtime.env \
     postqron.com \
     production
   ```

5. Replace the GitHub Environment secret through standard input:

   ```sh
   gh secret set RUNTIME_ENV \
     --repo apdsoftware/postqron \
     --env production \
     < /secure/path/runtime.env
   ```

6. Remove the temporary plaintext through the organization's approved secret
   handling procedure. Do not attach or paste it anywhere.

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

After those checks pass, an authorized Owner must perform the real smoke test:

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

This final smoke requires real external credentials and an authorized test
account. Repository fixtures, mocked HTTP responses, callback reachability,
and a successful deployment are regression evidence only; none proves that
production OAuth works.
