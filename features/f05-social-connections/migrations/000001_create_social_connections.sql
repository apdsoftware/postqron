CREATE TYPE f05_connection_status AS ENUM (
    'connected',
    'reconnect_required',
    'revoked'
);

CREATE TABLE f05_oauth_attempts (
    id text PRIMARY KEY,
    state_hash text NOT NULL UNIQUE CHECK (length(state_hash) = 64),
    workspace_id text NOT NULL,
    actor_account_id text NOT NULL,
    provider text NOT NULL CHECK (
        provider IN ('facebook_pages', 'instagram_professional')
    ),
    pkce_verifier_key_id text,
    pkce_verifier_ciphertext bytea,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CHECK (expires_at > created_at),
    CHECK (
        (pkce_verifier_ciphertext IS NULL AND pkce_verifier_key_id IS NULL)
        OR
        (octet_length(pkce_verifier_ciphertext) > 28 AND length(pkce_verifier_key_id) > 0)
    )
);

CREATE INDEX f05_oauth_attempts_expiry_idx
    ON f05_oauth_attempts (expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE f05_resource_selections (
    id text PRIMARY KEY,
    workspace_id text NOT NULL,
    actor_account_id text NOT NULL,
    provider text NOT NULL CHECK (
        provider IN ('facebook_pages', 'instagram_professional')
    ),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > created_at)
);

CREATE TABLE f05_selection_resources (
    selection_id text NOT NULL
        REFERENCES f05_resource_selections(id) ON DELETE CASCADE,
    remote_id text NOT NULL,
    resource_type text NOT NULL CHECK (
        resource_type IN ('facebook_page', 'instagram_professional')
    ),
    account_type text NOT NULL CHECK (
        account_type IN ('page', 'business', 'creator')
    ),
    display_name text NOT NULL CHECK (length(btrim(display_name)) > 0),
    handle text NOT NULL DEFAULT '',
    picture_url text NOT NULL DEFAULT '',
    scopes jsonb NOT NULL CHECK (
        jsonb_typeof(scopes) = 'array' AND jsonb_array_length(scopes) > 0
    ),
    access_token_key_id text NOT NULL CHECK (length(access_token_key_id) > 0),
    access_token_ciphertext bytea NOT NULL
        CHECK (octet_length(access_token_ciphertext) > 28),
    refresh_token_key_id text,
    refresh_token_ciphertext bytea,
    token_expires_at timestamptz,
    selected_at timestamptz,
    PRIMARY KEY (selection_id, remote_id),
    CHECK (
        (refresh_token_ciphertext IS NULL AND refresh_token_key_id IS NULL)
        OR
        (octet_length(refresh_token_ciphertext) > 28 AND length(refresh_token_key_id) > 0)
    )
);

CREATE TABLE f05_social_connections (
    id text PRIMARY KEY,
    workspace_id text NOT NULL,
    provider text NOT NULL CHECK (
        provider IN ('facebook_pages', 'instagram_professional')
    ),
    remote_id text NOT NULL,
    resource_type text NOT NULL CHECK (
        resource_type IN ('facebook_page', 'instagram_professional')
    ),
    account_type text NOT NULL CHECK (
        account_type IN ('page', 'business', 'creator')
    ),
    display_name text NOT NULL CHECK (length(btrim(display_name)) > 0),
    handle text NOT NULL DEFAULT '',
    picture_url text NOT NULL DEFAULT '',
    scopes jsonb NOT NULL CHECK (
        jsonb_typeof(scopes) = 'array' AND jsonb_array_length(scopes) > 0
    ),
    status f05_connection_status NOT NULL DEFAULT 'connected',
    reconnect_reason text NOT NULL DEFAULT '',
    access_token_key_id text,
    access_token_ciphertext bytea,
    refresh_token_key_id text,
    refresh_token_ciphertext bytea,
    token_expires_at timestamptz,
    refresh_locked_until timestamptz,
    last_verified_at timestamptz,
    connected_by_actor_id text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    revoked_at timestamptz,
    UNIQUE (workspace_id, provider, remote_id),
    CHECK (updated_at >= created_at),
    CHECK (
        (status = 'connected'
            AND access_token_key_id IS NOT NULL
            AND octet_length(access_token_ciphertext) > 28
            AND revoked_at IS NULL)
        OR
        (status <> 'connected'
            AND access_token_key_id IS NULL
            AND access_token_ciphertext IS NULL
            AND refresh_token_key_id IS NULL
            AND refresh_token_ciphertext IS NULL
            AND token_expires_at IS NULL
            AND refresh_locked_until IS NULL)
    ),
    CHECK (
        (refresh_token_ciphertext IS NULL AND refresh_token_key_id IS NULL)
        OR
        (octet_length(refresh_token_ciphertext) > 28 AND length(refresh_token_key_id) > 0)
    ),
    CHECK (
        (status = 'revoked' AND revoked_at IS NOT NULL)
        OR
        (status <> 'revoked' AND revoked_at IS NULL)
    )
);

CREATE INDEX f05_social_connections_workspace_status_idx
    ON f05_social_connections (workspace_id, status, provider);

CREATE INDEX f05_social_connections_refresh_idx
    ON f05_social_connections (token_expires_at)
    WHERE status = 'connected' AND token_expires_at IS NOT NULL;

CREATE TABLE f05_social_outbox (
    id text PRIMARY KEY,
    event_type text NOT NULL CHECK (event_type IN (
        'social.connection.connected',
        'social.connection.reconnected',
        'social.connection.reconnect-required',
        'social.connection.token-refreshed',
        'social.connection.disconnected'
    )),
    event_version integer NOT NULL CHECK (event_version = 1),
    workspace_id text NOT NULL,
    connection_id text NOT NULL,
    provider text NOT NULL CHECK (
        provider IN ('facebook_pages', 'instagram_professional')
    ),
    remote_id text NOT NULL,
    actor_account_id text,
    reason text NOT NULL DEFAULT '',
    correlation_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    published_at timestamptz
);

CREATE INDEX f05_social_outbox_pending_idx
    ON f05_social_outbox (occurred_at, id)
    WHERE published_at IS NULL;
