# F5 — Social connections

This slice connects the launch channels selected by D2:

- Facebook Pages managed with the official Meta Graph API;
- Instagram Professional Business or Creator accounts through Instagram Login.

The domain flow is deliberately two-step. `Begin`/`Callback` discovers
publishable resources and returns only safe metadata; `Select` requires the
Owner to name one exact remote resource before a connection exists. OAuth
state is one-time, and PKCE is applied when the configured Meta flow supports
it.

Access and refresh tokens are AES-256-GCM ciphertexts with random nonces,
external key identifiers, and workspace/provider/resource-bound additional
authenticated data. Plaintext tokens are never returned by list operations,
events, API contracts, or persistence models.

Only `channels.manage` may start, select, reconnect, or revoke a channel.
Workspace members with `workspace.view` can list safe connection state. Every
state change and token refresh writes an outbox event in the same repository
transaction as the connection change.

An authentication, missing-scope, or lost-resource response transitions the
connection once to `reconnect_required`, clears stored credentials, and emits
one event. Later automatic access attempts fail locally without calling Meta,
preventing infinite refresh/publish loops. A new explicit OAuth selection for
the same resource restores the existing connection as `connected`.

## Runtime configuration

Construct one `MetaAdapter` per supported provider. Supply a reviewed,
explicit Graph API version (for example `v25.0`) rather than an unversioned
endpoint. Secrets and the 32-byte encryption key must come from the runtime
secret store and must not be committed.

The Facebook adapter requests exactly `pages_show_list`,
`pages_read_engagement`, and `pages_manage_posts`, and accepts only Pages with
the `CREATE_CONTENT` task. The Instagram adapter requests exactly
`instagram_business_basic` and `instagram_business_content_publish`, and
accepts only Business or Creator accounts.

## Verification

```sh
cd features/f05-social-connections
go test -race ./...
go vet ./...
```

Set `F05_DATABASE_URL` to run the optional PostgreSQL integration test when a
database with the slice migration is available.
