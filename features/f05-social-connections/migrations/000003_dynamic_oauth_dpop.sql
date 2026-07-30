ALTER TABLE f05_oauth_attempts
    ADD COLUMN oauth_state_key_id text,
    ADD COLUMN oauth_state_ciphertext bytea,
    ADD COLUMN oauth_issuer text NOT NULL DEFAULT '',
    ADD COLUMN oauth_resource_server text NOT NULL DEFAULT '',
    ADD COLUMN oauth_subject text NOT NULL DEFAULT '',
    ADD CONSTRAINT f05_oauth_attempts_dynamic_state_check CHECK (
        (oauth_state_key_id IS NULL AND oauth_state_ciphertext IS NULL)
        OR
        (
            length(oauth_state_key_id) > 0
            AND octet_length(oauth_state_ciphertext) > 28
            AND length(oauth_issuer) > 0
            AND length(oauth_resource_server) > 0
        )
    );

ALTER TABLE f05_selection_resources
    ADD COLUMN oauth_session_key_id text,
    ADD COLUMN oauth_session_ciphertext bytea,
    ADD COLUMN oauth_issuer text NOT NULL DEFAULT '',
    ADD COLUMN oauth_resource_server text NOT NULL DEFAULT '',
    ADD COLUMN oauth_subject text NOT NULL DEFAULT '',
    ADD COLUMN refresh_token_mode text NOT NULL DEFAULT 'reusable'
        CHECK (refresh_token_mode IN ('reusable', 'single_use')),
    ADD CONSTRAINT f05_selection_resources_dynamic_session_check CHECK (
        (oauth_session_key_id IS NULL AND oauth_session_ciphertext IS NULL)
        OR
        (
            length(oauth_session_key_id) > 0
            AND octet_length(oauth_session_ciphertext) > 28
            AND length(oauth_issuer) > 0
            AND length(oauth_resource_server) > 0
        )
    );

ALTER TABLE f05_social_connections
    ADD COLUMN oauth_session_key_id text,
    ADD COLUMN oauth_session_ciphertext bytea,
    ADD COLUMN oauth_issuer text NOT NULL DEFAULT '',
    ADD COLUMN oauth_resource_server text NOT NULL DEFAULT '',
    ADD COLUMN oauth_subject text NOT NULL DEFAULT '',
    ADD COLUMN refresh_token_mode text NOT NULL DEFAULT 'reusable'
        CHECK (refresh_token_mode IN ('reusable', 'single_use')),
    ADD COLUMN session_locked_until timestamptz,
    ADD COLUMN session_lock_id text,
    ADD COLUMN session_refreshing boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT f05_social_connections_dynamic_session_check CHECK (
        (oauth_session_key_id IS NULL AND oauth_session_ciphertext IS NULL)
        OR
        (
            length(oauth_session_key_id) > 0
            AND octet_length(oauth_session_ciphertext) > 28
            AND length(oauth_issuer) > 0
            AND length(oauth_resource_server) > 0
        )
    ),
    ADD CONSTRAINT f05_social_connections_session_lock_check CHECK (
        (
            session_locked_until IS NULL
            AND session_lock_id IS NULL
            AND session_refreshing = false
        )
        OR
        (
            session_locked_until IS NOT NULL
            AND length(session_lock_id) > 0
        )
    );

CREATE INDEX f05_social_connections_session_lock_idx
    ON f05_social_connections (session_locked_until)
    WHERE session_locked_until IS NOT NULL;
