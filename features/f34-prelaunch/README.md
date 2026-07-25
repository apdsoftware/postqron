# F34 — Pre-launch

This autonomous slice keeps incomplete product and marketing routes behind a
localized pre-launch landing page. In production the switch is fail-closed:
only the exact value `PRELAUNCH_MODE=false` exposes the normal site and app.
`true`, a missing value, whitespace, different casing, and every other value
keep pre-launch enabled.

The global route middleware explicitly allows:

- the localized pre-launch landing and access-request pages;
- legal documents and `help@postqron.com` support;
- `/admin`, whose own authorization middleware still decides access;
- API, health/status, Nuxt, brand, PWA, manifest, robots and sitemap paths.

All other public pages redirect to the localized landing without retaining an
untrusted return URL. Direct visits to pre-launch pages redirect to `/app`
after go-live, so the public CTA never ends at a missing route.

## Access requests

`api/` owns `POST /api/v1/prelaunch/access-requests` and the durable schema.
The endpoint requires an explicit `prelaunch-access-v1` access consent and
rejects marketing consent. It normalizes and deduplicates email addresses,
stores the consent proof, rate-limits hashed client identifiers, and records
exactly one `f14.prelaunch_access.v1` transactional command per address. It
does not call Mailronix or create a marketing subscription.

Cross-origin web/API deployments must set `PRELAUNCH_ALLOWED_ORIGINS` to a
comma-separated list of exact origins. Production has no permissive default.
Local development allows `http://localhost:3000` and
`http://127.0.0.1:3000`.

`GET /api/v1/prelaunch/status` reports the effective boolean and resolution
source without returning the environment value or any secret.

## Activation and rollback

Activation and rollback are configuration-only operations:

1. Apply the F34 migration and deploy the slice with
   `PRELAUNCH_MODE=true`.
2. Verify `/api/v1/prelaunch/status` returns `prelaunch_mode: true`, then
   exercise the landing, legal links, admin authorization and one access
   request.
3. Go live by setting the production secret/config value to the exact string
   `false` and restart/roll the web and API processes.
4. Verify the status reports `explicit_false`, `/` serves the marketing site,
   `/prelaunch` redirects to `/app`, and the CTA targets `/app`.
5. Roll back immediately by setting the value to the exact string `true`.
   Removing or corrupting the value also fails closed, but explicit `true` is
   preferred because it makes intent visible in status.

## Verification

```sh
pnpm --dir features/f34-prelaunch test
pnpm --dir features/f34-prelaunch typecheck
(cd features/f34-prelaunch/api && GOWORK=off go test -race ./...)
pnpm --dir features/f34-prelaunch test:e2e
pnpm --dir features/f34-prelaunch test:lighthouse
```

The Lighthouse script starts its own pre-launch production preview and
requires mobile scores of at least 0.80 for performance, 0.90 for
accessibility and 1.00 for SEO. It keeps its JSON report under the ignored
`.lighthouse/` directory.
