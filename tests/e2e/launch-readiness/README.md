# F35 launch-readiness suite

This directory is an independent release gate for issue #95. It exercises a
production Nuxt build against deterministic local API/provider fixtures and
can point the browser checks at remote preview URLs without committing
credentials.

## Local run

```sh
pnpm install --frozen-lockfile
pnpm --dir tests/e2e/launch-readiness install --frozen-lockfile
pnpm --dir tests/e2e/launch-readiness exec playwright install chromium
pnpm --dir tests/e2e/launch-readiness test
pnpm --dir tests/e2e/launch-readiness audit:artifacts
```

The runner builds the web app, starts go-live and pre-launch previews plus a
stateful fixture API, then writes the requirement matrix and diagnostics below
`artifacts/`. All fixture identities use reserved `example.test` addresses.
The reporter overrides Playwright's exit status when any requirement is
missing, skipped or covered only by failing tests, so a filtered run cannot
produce a green launch gate.

## Remote preview

Set `LAUNCH_BASE_URL` to the go-live preview and
`LAUNCH_PRELAUNCH_URL` to a preview of the same SHA with pre-launch enabled.
Set `LAUNCH_FIXTURE_URL` only when the preview is explicitly configured to use
an isolated, non-production fixture API. Never pass production session cookies
or provider credentials to this suite.

The workflow also accepts these URLs through `workflow_dispatch`. Pull requests
run the deterministic local preview, while the manual dispatch is the
deployment-SHA verification path.
