# F12 — Account and privacy

This autonomous API slice owns the account profile view and orchestrates privacy
rights without importing another feature's implementation.

## Capabilities

- returns the profile, connected identity/social providers, workspaces, active
  public plan, usage, and limits;
- validates profile updates, including IANA timezones;
- requires authentication performed within five minutes for provider
  disconnection, exports, deletion, and cancellation;
- rate-limits sensitive authenticated mutations and signed-download issuance;
- prevents removal of the last identity provider and delegates remote revocation
  plus local credential removal through `ProviderDisconnecter`;
- queues or reuses account or Owner-authorized workspace exports, accepts only
  SHA-256 described artifacts, keeps packages for seven days, and issues
  authenticated signed links for at most 24 hours;
- requires explicit ownership decisions before account deletion;
- freezes access, revokes sessions, attempts provider revocation, deletes local
  tokens, and cancels future jobs before entering the 28-day grace period;
- supports cancellation during the grace period without restoring sessions,
  tokens, or cancelled jobs;
- finalizes deletion/irreversible anonymization, records a 45-day opaque
  tombstone, and persists auditable success/failure transitions.

The retention durations come from D05. Storage adapters must encrypt export
objects at rest, use TLS, delete the package at expiry, and remove audit events
after 12 months. `DeletionSafety` implementations must complete session and job
revocation within five minutes and local token deletion within fifteen minutes.
Workers must re-check deletion state immediately before every external effect.

## Integration

Discovery reads `feature.yaml`; no central registry is required. The composition
root supplies adapters for:

- F3 authentication/session and identity-provider operations;
- F4 membership, Owner authorization, and ownership resolution;
- F5 social-token revocation;
- F7/F8 durable job cancellation and pre-effect deletion checks;
- F10 client-safe plan/usage summaries;
- encrypted object storage, signed URLs, export queue, and erasure workers.

Every `Repository` transition is transactional with its minimal audit event.
The included memory repository applies the same state-machine checks for tests
and local composition. The forward-only migration owns the durable tables.
A PostgreSQL repository persists the F12-owned state while keeping providers and
workspaces behind injected projection readers.

## Verification

The feature is intentionally not added to the root `go.work`, because F12 may
only modify this directory. Run its checks independently:

```sh
GOWORK=off GOCACHE=$PWD/.gocache go test ./...
GOWORK=off GOCACHE=$PWD/.gocache go test -race ./...
GOWORK=off go vet ./...
```

Set `F12_DATABASE_URL` to enable PostgreSQL integration tests.
