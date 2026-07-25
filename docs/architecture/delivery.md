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

## Configure GitHub

Use the interactive helper from an authenticated workstation. It skips names
that already exist and never echoes secret input:

```sh
./scripts/deploy/configure-github-environment.sh \
  --environment production \
  --phase provision
```

The provision phase configures public domains and CIDRs, provider credentials,
the deployment key when missing, and an S3-compatible Terraform backend. After
the server host key has been verified, configure release-only values:

```sh
./scripts/deploy/configure-github-environment.sh \
  --environment production \
  --phase release
```

This second phase reads a verified `known_hosts` file, requests a GitHub classic
token limited to `read:packages`, and generates a URL-safe PostgreSQL password
directly into `RUNTIME_ENV`. Values already configured are not rotated. Replace
one intentionally with `--replace NAME`; replacing `RUNTIME_ENV` rotates the
database password and must not be done against an existing database without a
coordinated password change.

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
