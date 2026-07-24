CREATE TABLE auth_accounts (
    id text PRIMARY KEY,
    email text NOT NULL,
    normalized_email text NOT NULL UNIQUE,
    display_name text NOT NULL DEFAULT '',
    contract_country char(2) NOT NULL,
    created_at timestamptz NOT NULL,
    CHECK (normalized_email = lower(btrim(email))),
    CHECK (normalized_email <> ''),
    CHECK (contract_country = 'IT')
);

CREATE TABLE auth_oauth_attempts (
    id text PRIMARY KEY,
    state_hash char(64) NOT NULL UNIQUE,
    pkce_verifier_ciphertext bytea NOT NULL,
    nonce_ciphertext bytea NOT NULL,
    provider text NOT NULL,
    intent text NOT NULL,
    target_account_id text REFERENCES auth_accounts(id),
    bound_session_token_hash char(64),
    return_to text NOT NULL,
    contract_country char(2),
    consent_receipts jsonb NOT NULL DEFAULT '[]'::jsonb,
    correlation_id text NOT NULL UNIQUE,
    status text NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    claimed_at timestamptz,
    completed_at timestamptz,
    CHECK (provider IN ('google', 'apple', 'facebook', 'linkedin')),
    CHECK (intent IN ('login', 'link')),
    CHECK (status IN ('pending', 'claimed', 'completed', 'failed')),
    CHECK (return_to LIKE '/%' AND return_to NOT LIKE '//%'),
    CHECK (jsonb_typeof(consent_receipts) = 'array'),
    CHECK (
        (intent = 'login' AND target_account_id IS NULL AND bound_session_token_hash IS NULL)
        OR
        (intent = 'link' AND target_account_id IS NOT NULL AND bound_session_token_hash IS NOT NULL)
    ),
    CHECK (expires_at > created_at)
);

CREATE INDEX auth_oauth_attempts_expiry_idx
    ON auth_oauth_attempts (expires_at)
    WHERE status IN ('pending', 'claimed');

CREATE TABLE auth_provider_identities (
    provider text NOT NULL,
    provider_subject text NOT NULL,
    account_id text NOT NULL REFERENCES auth_accounts(id),
    provider_email text NOT NULL DEFAULT '',
    revocation_token_ciphertext bytea,
    linked_at timestamptz NOT NULL,
    PRIMARY KEY (provider, provider_subject),
    UNIQUE (account_id, provider),
    CHECK (provider IN ('google', 'apple', 'facebook', 'linkedin'))
);

CREATE TABLE auth_sessions (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES auth_accounts(id),
    token_hash char(64) NOT NULL UNIQUE,
    created_at timestamptz NOT NULL,
    authenticated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (expires_at > created_at),
    CHECK (authenticated_at >= created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX auth_sessions_account_active_idx
    ON auth_sessions (account_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE auth_consent_events (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES auth_accounts(id),
    document_key text NOT NULL,
    document_version text NOT NULL,
    document_digest_sha256 char(64) NOT NULL,
    action text NOT NULL,
    purpose text NOT NULL,
    locale text NOT NULL,
    country char(2) NOT NULL,
    surface text NOT NULL,
    control_text_id text NOT NULL,
    correlation_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    CHECK (action IN ('accepted', 'acknowledged', 'granted', 'rejected', 'withdrawn')),
    CHECK (document_digest_sha256 ~ '^[0-9a-f]{64}$'),
    UNIQUE (
        account_id,
        document_key,
        document_version,
        document_digest_sha256,
        action,
        purpose,
        correlation_id
    )
);

CREATE INDEX auth_consent_events_account_time_idx
    ON auth_consent_events (account_id, occurred_at);

CREATE TABLE auth_outbox_events (
    id text PRIMARY KEY,
    event_type text NOT NULL,
    event_version integer NOT NULL,
    aggregate_id text NOT NULL,
    correlation_id text NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    published_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    last_error_code text,
    CHECK (event_version > 0),
    CHECK (jsonb_typeof(payload) = 'object'),
    CHECK (attempts >= 0),
    UNIQUE (event_type, event_version, aggregate_id)
);

CREATE INDEX auth_outbox_events_pending_idx
    ON auth_outbox_events (occurred_at)
    WHERE published_at IS NULL;
