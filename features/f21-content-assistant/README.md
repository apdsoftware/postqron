# F21 — Content assistant

This autonomous API slice creates channel-specific text alternatives while
keeping a human decision between every suggestion and any composer update.
F16 discovers `feature.yaml`; no central registry is required.

## Safety boundary

`DraftReader` is the narrow adapter from F6. It supplies a draft revision and
effective text for each selected destination without provider credentials.
Generating or manually entering alternatives only creates a `pending`
proposal. Neither path updates a draft, schedules a post, or publishes content.

Confirmation requires all of the following:

- an authenticated actor authorized to manage content in the workspace;
- the current optimistic proposal revision;
- `confirmation: true`;
- one explicit candidate ID per destination.

The result is a `ConfirmedChangeSet` carrying the original F6 draft revision.
The host may pass only this confirmed result to its F6 adapter, which must use
that revision as an optimistic-concurrency precondition. A stale proposal must
not overwrite newer composer work.

Rejection is also explicit and terminal. Confirmed and rejected proposals
cannot be decided again.

## Channel variants and comparison

The generator is called separately for each F6 destination with its channel,
effective original text, and requested alternative count (maximum three).
Every stored candidate contains:

- destination and channel;
- immutable original and proposed text;
- a deterministic, lossless `equal`/`delete`/`insert` diff;
- generation source and bounded provider trace identifiers.

Provider prompts, credentials, raw responses, and arbitrary audit metadata are
not part of the contract.

## Manual fallback

If generation is unavailable, the generation endpoint returns `503` with
`manual_fallback_available: true`. The manual proposal endpoint accepts text
entered by the user and creates the same pending, comparable, auditable
proposal. Manual text still requires explicit confirmation.

## Traceability

The proposal stores its source draft revision and an append-only timeline:

- `proposal.generated` or `proposal.manual_submitted`;
- `proposal.confirmed` with selected candidate IDs; or
- `proposal.rejected`.

Events contain UTC time plus opaque actor, request-correlation, proposal, and
candidate IDs. They do not duplicate content into arbitrary metadata.
Repository decisions use optimistic revision checks.

## HTTP API

The OpenAPI contract is in `contracts/openapi.yaml`. Routes are scoped by
workspace:

- `POST /drafts/{draft_id}/content-assistant/proposals`
- `POST /drafts/{draft_id}/content-assistant/manual-proposals`
- `GET /content-assistant/proposals/{proposal_id}`
- `POST /content-assistant/proposals/{proposal_id}/confirm`
- `POST /content-assistant/proposals/{proposal_id}/reject`

All responses use `Cache-Control: no-store`.

## Verification

Run the slice tests:

```sh
cd features/f21-content-assistant
GOWORK=off go test -race ./...
```

Validate discovery and the migration with F6:

```sh
go run ./services/api/cmd/migrate --check \
  --roots services/api/features:features
```
