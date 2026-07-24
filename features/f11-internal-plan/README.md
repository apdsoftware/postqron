# F11 private internal entitlement

This autonomous API slice administers the unlimited enforcement override that
F10 already consumes server-side.

- It has no public catalog entry, checkout product, billing state, or public
  API contract.
- Its HTTP handler is deliberately named `NewInternalHTTPHandler` and must be
  mounted only on a private administration listener.
- The actor identity and strong-auth state come from server authentication,
  never from request JSON.
- Assignment requires both server-side admin authorization and an active
  account/workspace tuple in `f11_internal_plan_allowlist`.
- Revocation remains available to an authorized admin even after an allowlist
  entry is disabled.
- Assignment/revocation state and the F10 enforcement override change in one
  database transaction.
- Successful operations, allowlist denials, binding conflicts, non-admin
  attempts, and strong-auth failures write F15 append-only sensitive audit
  events. Audit failure is fail-closed.

The allowlist has no HTTP mutation endpoint. Operations staff manage it through
an access-controlled database workflow, recording the granting/revoking admin
IDs and timestamps. Values are opaque UUIDs; no email address or other personal
data is stored.

## Runtime discovery

F11 declares `f10-entitlements` and `operations` dependencies in `feature.yaml`.
Include the feature root used by F16 discovery; no central registry is needed:

```sh
POSTQRON_FEATURE_ROOTS="services/api/features:features" go run ./services/api/cmd/api
```

Never add this slice's internal handler or OpenAPI contract to the public API
router or documentation bundle.

## Tests

```sh
cd features/f11-internal-plan
GOWORK=off go test ./...
```

Set `TEST_DATABASE_URL` to enable the PostgreSQL integration test.
