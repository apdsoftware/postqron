CREATE TABLE f05_linkedin_dms_grants (
    handle_hash text PRIMARY KEY CHECK (length(handle_hash) = 64),
    workspace_id text NOT NULL,
    connection_id text NOT NULL
        REFERENCES f05_social_connections(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider = 'linkedin'),
    evidence_key_id text NOT NULL CHECK (length(evidence_key_id) > 0),
    evidence_ciphertext bytea NOT NULL
        CHECK (octet_length(evidence_ciphertext) > 28),
    state text NOT NULL CHECK (
        state IN (
            'registered',
            'uploading',
            'upload_sending',
            'uploaded',
            'creating',
            'create_sending',
            'consumed',
            'failed'
        )
    ),
    lease_id text NOT NULL DEFAULT '',
    locked_until timestamptz,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    uploaded_at timestamptz,
    consumed_at timestamptz,
    CHECK (expires_at > created_at),
    CHECK (
        (
            state IN ('uploading', 'creating')
            AND length(lease_id) > 0
            AND locked_until IS NOT NULL
        )
        OR
        (
            state NOT IN ('uploading', 'creating')
            AND lease_id = ''
            AND locked_until IS NULL
        )
    ),
    CHECK (
        (
            state IN ('uploaded', 'creating', 'create_sending', 'consumed')
            AND uploaded_at IS NOT NULL
        )
        OR
        (
            state IN ('registered', 'uploading', 'upload_sending')
            AND uploaded_at IS NULL
        )
        OR
        state = 'failed'
    ),
    CHECK (
        (state IN ('consumed', 'failed') AND consumed_at IS NOT NULL)
        OR
        (state NOT IN ('consumed', 'failed') AND consumed_at IS NULL)
    )
);

CREATE INDEX f05_linkedin_dms_grants_binding_idx
    ON f05_linkedin_dms_grants (
        workspace_id,
        connection_id,
        state,
        expires_at
    );

CREATE INDEX f05_linkedin_dms_grants_expiry_idx
    ON f05_linkedin_dms_grants (expires_at)
    WHERE state NOT IN ('consumed', 'failed');

CREATE INDEX f05_linkedin_dms_grants_lease_idx
    ON f05_linkedin_dms_grants (locked_until)
    WHERE locked_until IS NOT NULL;
