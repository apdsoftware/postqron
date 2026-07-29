# Delivery to Hetzner and Cloudflare

The delivery workflow builds immutable images, provisions a Hetzner server and
Cloudflare-proxied DNS records with Terraform, applies migrations, and starts
the stack through systemd and Docker Compose.

## Environments

GitHub environments named `staging` and `production` isolate approvals,
variables, and secrets. The Deploy workflow is manual and serial per
environment. It exposes a `provision` operation for the first infrastructure
apply and a `release` operation for application delivery. Images use the commit
SHA; no mutable `latest` tag is deployed.

Required GitHub environment variables:

- `ADMIN_CIDRS_JSON`: non-empty JSON list of controlled administrative SSH
  source CIDRs. The workflow temporarily adds its current runner IPv4 as a
  `/32` and removes it in an `always()` cleanup step.
- `API_DOMAIN` and `APP_DOMAIN`.
- `CLOUDFLARE_ZONE_ID`.
- `DEPLOYMENT_SSH_PUBLIC_KEY`.
- `POSTQRON_MAILRONIX_ENDPOINT`: must be an HTTPS Mailronix delivery endpoint
  ending with `/email/send`.
- `POSTQRON_MAILRONIX_API_KEY_SECRET_NAME`: must be the exact string
  `MAILRONIX_TRANSACTIONAL_API_KEY`.
- `POSTQRON_MAILRONIX_SENDER_EMAIL`.
- `POSTQRON_MAILRONIX_DOMAIN_VERIFIED`: must be the exact string `true` before
  a production release.
- `PRELAUNCH_MODE`: the exact string `true` or `false`. Missing values, different
  casing, whitespace, and every other value block a release before upload.

Required GitHub environment secrets:

- `HCLOUD_TOKEN` and `CLOUDFLARE_API_TOKEN`, both least-privilege.
- `TF_STATE_ACCESS_KEY`, `TF_STATE_SECRET_KEY`, and `TF_BACKEND_CONFIG` for an
  encrypted, versioned remote Terraform state bucket.
- `DEPLOYMENT_SSH_PRIVATE_KEY` and a pinned `SSH_KNOWN_HOSTS` entry.
- `GHCR_READ_TOKEN` scoped only to package reads.
- `RUNTIME_ENV`, containing `POSTGRES_PASSWORD` and `DATABASE_URL`.
- `MAILRONIX_TRANSACTIONAL_API_KEY`, containing the live Mailronix
  transactional API key referenced by
  `POSTQRON_MAILRONIX_API_KEY_SECRET_NAME`.
- `ADMIN_PASSWORD_HASH_B64` for the mounted F3 password runtime.
- `AUTH_ENCRYPTION_KEY_B64` and `PRIVACY_ARTIFACT_KEY_B64`, each encoded as
  base64 of exactly 32 random bytes, for auth state and privacy artifacts.

`RUNTIME_ENV` must not include image tags, public domains, or `PRELAUNCH_MODE`;
the workflow appends those from the reviewed commit and dedicated environment
variables. `RUNTIME_ENV` must also not contain any `POSTQRON_MAILRONIX_*` entry
or `MAILRONIX_TRANSACTIONAL_API_KEY`. A conflicting assignment blocks the
release instead of creating ambiguous precedence. The generated runtime contains
exactly one canonical line per dedicated delivery variable. The remote file is
mode `0600`. No token, private key, password, state credential, or personal data
is stored in Git.

OAuth provider secrets remain optional and independent. Invalid or missing
Google, Apple, Facebook, or LinkedIn values must never block password flows.
Redirect URLs are accepted only as HTTPS callback URLs. Apple client-secret
rotation must overlap old and new values long enough to drain in-flight
callbacks. Rotation of `POSTQRON_AUTH_ENCRYPTION_KEY_B64` must be coordinated
after expiring outstanding OAuth state; privacy artifact key rotation requires
a separate migration plan for existing artifacts.

## Configure GitHub

Use the interactive helper from an authenticated workstation. It skips names
that already exist and never echoes secret input:

```sh
./scripts/deploy/configure-github-environment.sh \
  --environment production \
  --phase provision
```

The provision phase configures public domains and CIDRs, provider credentials,
the deployment key when missing, and an S3-compatible Terraform backend. On the
first provisioning run, the workflow uses those environment credentials to
create the private state bucket when absent, enables versioning, verifies its
status, and only then initializes Terraform. Existing buckets are reused. After
the server host key has been verified, configure release-only values:

```sh
./scripts/deploy/configure-github-environment.sh \
  --environment production \
  --phase release
```

This second phase reads a verified `known_hosts` file, generates
`AUTH_ENCRYPTION_KEY_B64` and `PRIVACY_ARTIFACT_KEY_B64` only when each secret is
absent, configures the non-secret Mailronix boundary variables, and prompts for
`MAILRONIX_TRANSACTIONAL_API_KEY` only when that secret is absent. It also
creates `PRELAUNCH_MODE` with the safe default `true` when the variable is
absent. It never reads or replaces `RUNTIME_ENV`, so it cannot rotate the
database password. Encryption keys are also never replaced by this helper;
rotation requires a separate, coordinated procedure. During release, the
workflow validates the dedicated values without printing secrets and appends
them to the remote runtime.

Go live by explicitly changing the production variable to `false`:

```sh
./scripts/deploy/configure-github-environment.sh \
  --environment production \
  --phase release \
  --replace PRELAUNCH_MODE
```

Enter the exact value `false`, review the change, then dispatch the production
release. For configuration rollback, run the same command and enter the exact
value `true`, then dispatch a production release. Missing or invalid values
block delivery; explicit `true` is the documented rollback state.

## First deployment

1. Create a dedicated remote state bucket and GitHub environments.
2. Create separate least-privilege provider tokens per environment.
3. Generate one deployment SSH key pair per environment. Store only the public
   key as a GitHub variable.
4. Set `ADMIN_CIDRS_JSON` to controlled administrator or bastion ranges. Do not
   add the shared GitHub-hosted runner ranges or expose SSH to the internet.
5. Run the Deploy workflow with the `provision` operation. It applies Terraform,
   records the server IP in the workflow summary, and does not attempt SSH.
6. From a source allowed by `ADMIN_CIDRS_JSON`, scan the Ed25519 host key. Compare
   its fingerprint with `/etc/ssh/ssh_host_ed25519_key.pub` through the Hetzner
   console before storing the complete known-hosts line in
   `SSH_KNOWN_HOSTS`.
7. Run the workflow with the `release` operation and verify both health
   endpoints through Cloudflare.

Cloud-init installs Docker, creates an unprivileged `postqron` deployment user,
and grants only the systemd restart commands required by delivery. The server
does not receive provider or Terraform credentials. Terraform apply and SSH run
on the same GitHub runner, so only that runner's validated ephemeral `/32` is
temporarily admitted by the firewall.

## Rollback and migration safety

Application rollback means redeploying a previous SHA image. Database migrations
are forward-only and are never automatically reversed. If a release changes the
schema, its migration must be backward-compatible with the previous application
until rollback is no longer required.

Terraform has `prevent_destroy` on the server. Infrastructure replacement,
database recovery, or state moves require a reviewed manual plan. Before
production launch, enable the D05 backup, PITR, retention, restore testing, and
alerting controls; they are deliberately not simulated by this foundation.

## Cloudflare posture

Terraform creates proxied `A` records for app and API. Configure the zone outside
this repository with Full (strict) TLS, an origin policy compatible with ACME,
managed WAF rules, bot/rate-limit policy, and cache rules that never cache
authenticated or API responses. Zone-wide policy is shared infrastructure and
should not be changed by an application slice without its own reviewed scope.

## Runtime integration notes

The services-side runtime mounts F3 auth, F4 app runtime, the F30 API
bootstrap/session handler, and an adapter feature for F12 account/profile
navigation. The worker consumes the F3 onboarding outbox idempotently to create
or select the personal workspace and initialize `account_privacy_profiles`, and
dispatches queued F14 verification email deliveries. Both the API and worker
derive email links from the canonical `APP_DOMAIN`; Compose rejects a release
when it is absent. After each restart, delivery also requires the worker
container to remain continuously running with no restart during the
initialization observation window.

Privacy export queueing and account/workspace deletion remain fail-closed in
the runtime adapter until Auth exposes a reviewed boundary that can freeze new
password and OAuth logins during the grace period without changing feature
internals.
