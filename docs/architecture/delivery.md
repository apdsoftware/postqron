# Delivery to Hetzner and Cloudflare

The delivery workflow builds immutable images, provisions a Hetzner server and
Cloudflare-proxied DNS records with Terraform, applies migrations, and starts
the stack through systemd and Docker Compose.

## Environments

GitHub environments named `staging` and `production` isolate approvals,
variables, and secrets. The Deploy workflow is manual and serial per
environment. Images use the commit SHA; no mutable `latest` tag is deployed.

Required GitHub environment variables:

- `ADMIN_CIDRS_JSON`: JSON list of restricted SSH source CIDRs.
- `API_DOMAIN` and `APP_DOMAIN`.
- `CLOUDFLARE_ZONE_ID`.
- `DEPLOYMENT_SSH_PUBLIC_KEY`.

Required GitHub environment secrets:

- `HCLOUD_TOKEN` and `CLOUDFLARE_API_TOKEN`, both least-privilege.
- `TF_STATE_ACCESS_KEY`, `TF_STATE_SECRET_KEY`, and `TF_BACKEND_CONFIG` for an
  encrypted, versioned remote Terraform state bucket.
- `DEPLOYMENT_SSH_PRIVATE_KEY` and a pinned `SSH_KNOWN_HOSTS` entry.
- `GHCR_READ_TOKEN` scoped only to package reads.
- `RUNTIME_ENV`, containing `POSTGRES_PASSWORD`, `DATABASE_URL`, and any future
  runtime secrets.

`RUNTIME_ENV` must not include image tags or public domains; the workflow appends
those from the reviewed commit and environment variables. The remote file is
mode `0600`. No token, private key, password, state credential, or personal data
is stored in Git.

## First deployment

1. Create a dedicated remote state bucket and GitHub environments.
2. Create separate least-privilege provider tokens per environment.
3. Generate one deployment SSH key pair per environment. Store only the public
   key as a GitHub variable.
4. Set `ADMIN_CIDRS_JSON` to the delivery runner or controlled bastion ranges.
5. Run the Deploy workflow for `staging`.
6. Record the server host key in `SSH_KNOWN_HOSTS` after verifying it through the
   Hetzner console.
7. Re-run delivery and verify both health endpoints through Cloudflare.

Cloud-init installs Docker, creates an unprivileged `postqron` deployment user,
and grants only the systemd restart commands required by delivery. The server
does not receive provider or Terraform credentials.

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
