ALTER TABLE auth_accounts
    ADD COLUMN email_verified_at timestamptz;

UPDATE auth_accounts account
SET email_verified_at = account.created_at
WHERE EXISTS (
    SELECT 1
    FROM auth_provider_identities identity
    WHERE identity.account_id = account.id
      AND lower(btrim(identity.provider_email)) = account.normalized_email
);

CREATE TABLE auth_password_credentials (
    account_id text PRIMARY KEY REFERENCES auth_accounts(id) ON DELETE CASCADE,
    password_hash text NOT NULL,
    failed_attempts integer NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    locked_until timestamptz,
    changed_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (password_hash LIKE '$argon2id$v=%'),
    CHECK (updated_at >= created_at),
    CHECK (changed_at >= created_at)
);

CREATE TABLE auth_password_tokens (
    id text PRIMARY KEY,
    account_id text NOT NULL REFERENCES auth_accounts(id) ON DELETE CASCADE,
    purpose text NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    token_hash char(64) NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL,
    CHECK (expires_at > created_at),
    CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);

CREATE INDEX auth_password_tokens_account_purpose_idx
ON auth_password_tokens (account_id, purpose, expires_at)
WHERE consumed_at IS NULL;

CREATE TABLE auth_security_events (
    id text PRIMARY KEY,
    account_id text REFERENCES auth_accounts(id) ON DELETE SET NULL,
    event_type text NOT NULL CHECK (
        event_type IN (
            'password.bootstrap',
            'password.login_succeeded',
            'password.login_failed',
            'password.changed',
            'password.reset_requested',
            'password.reset_completed',
            'email.verification_requested',
            'email.verified'
        )
    ),
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'rejected')),
    occurred_at timestamptz NOT NULL
);

CREATE INDEX auth_security_events_account_time_idx
ON auth_security_events (account_id, occurred_at DESC);

CREATE FUNCTION auth_reject_security_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'auth_security_events is append-only'
        USING ERRCODE = '55000';
END
$$;

CREATE TRIGGER auth_security_events_append_only
BEFORE UPDATE OR DELETE ON auth_security_events
FOR EACH ROW
EXECUTE FUNCTION auth_reject_security_event_mutation();
