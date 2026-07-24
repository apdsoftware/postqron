CREATE TABLE compliance_legal_documents (
    document_key text NOT NULL CHECK (
        document_key IN ('terms_it', 'privacy_it', 'cookies_it', 'dpa_it', 'subprocessors')
    ),
    jurisdiction text NOT NULL CHECK (jurisdiction = 'IT'),
    locale text NOT NULL CHECK (locale = 'it-IT'),
    version text NOT NULL CHECK (version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'),
    content_bytes bytea NOT NULL,
    digest_sha256 text NOT NULL CHECK (digest_sha256 ~ '^[a-f0-9]{64}$'),
    content_status text NOT NULL CHECK (content_status IN ('placeholder', 'approved')),
    legal_approval_id text,
    approved_at timestamptz,
    published_at timestamptz,
    effective_at timestamptz,
    superseded_at timestamptz,
    permanent_url text,
    current_url text,
    change_type text NOT NULL CHECK (change_type IN ('material', 'non_material')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (document_key, jurisdiction, locale, version),
    UNIQUE (
        document_key,
        jurisdiction,
        locale,
        version,
        content_status,
        digest_sha256
    ),
    CHECK (
        (
            content_status = 'placeholder'
            AND legal_approval_id IS NULL
            AND approved_at IS NULL
            AND published_at IS NULL
            AND effective_at IS NULL
            AND superseded_at IS NULL
            AND permanent_url IS NULL
            AND current_url IS NULL
        )
        OR
        (
            content_status = 'approved'
            AND legal_approval_id IS NOT NULL
            AND approved_at IS NOT NULL
        )
    ),
    CHECK (
        published_at IS NULL
        OR (
            content_status = 'approved'
            AND effective_at IS NOT NULL
            AND permanent_url IS NOT NULL
            AND current_url IS NOT NULL
        )
    ),
    CHECK (
        superseded_at IS NULL
        OR (published_at IS NOT NULL AND superseded_at > published_at)
    )
);

CREATE TABLE compliance_consent_events (
    event_id text PRIMARY KEY,
    subject_kind text NOT NULL CHECK (
        subject_kind IN ('authenticated_user', 'pseudonymous_browser')
    ),
    subject_id text NOT NULL,
    workspace_id text,
    document_key text NOT NULL CHECK (
        document_key IN ('terms_it', 'privacy_it', 'cookies_it', 'dpa_it', 'subprocessors')
    ),
    document_version text NOT NULL,
    document_digest_sha256 text NOT NULL CHECK (document_digest_sha256 ~ '^[a-f0-9]{64}$'),
    document_content_status text NOT NULL DEFAULT 'approved' CHECK (
        document_content_status = 'approved'
    ),
    ui_digest_sha256 text CHECK (
        ui_digest_sha256 IS NULL OR ui_digest_sha256 ~ '^[a-f0-9]{64}$'
    ),
    purpose text NOT NULL,
    action text NOT NULL CHECK (
        action IN ('accepted', 'acknowledged', 'granted', 'rejected', 'withdrawn')
    ),
    occurred_at timestamptz NOT NULL,
    locale text NOT NULL CHECK (locale = 'it-IT'),
    contractual_country text NOT NULL CHECK (contractual_country = 'IT'),
    surface text NOT NULL,
    correlation_id text NOT NULL,
    idempotency_key text NOT NULL UNIQUE,
    control_text_version text NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (
        document_key,
        contractual_country,
        locale,
        document_version,
        document_content_status,
        document_digest_sha256
    ) REFERENCES compliance_legal_documents (
        document_key,
        jurisdiction,
        locale,
        version,
        content_status,
        digest_sha256
    )
);

CREATE TABLE compliance_cookie_preferences (
    subject_kind text NOT NULL CHECK (
        subject_kind IN ('authenticated_user', 'pseudonymous_browser')
    ),
    subject_id text NOT NULL,
    necessary boolean NOT NULL DEFAULT true CHECK (necessary),
    preferences boolean NOT NULL DEFAULT false,
    analytics boolean NOT NULL DEFAULT false,
    marketing boolean NOT NULL DEFAULT false,
    selected_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subject_kind, subject_id),
    CHECK (expires_at > selected_at),
    CHECK (expires_at <= selected_at + interval '6 months')
);

CREATE FUNCTION compliance_reject_immutable_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$;

CREATE TRIGGER compliance_legal_documents_are_immutable
BEFORE UPDATE OR DELETE ON compliance_legal_documents
FOR EACH ROW EXECUTE FUNCTION compliance_reject_immutable_change();

CREATE TRIGGER compliance_consent_events_are_append_only
BEFORE UPDATE OR DELETE ON compliance_consent_events
FOR EACH ROW EXECUTE FUNCTION compliance_reject_immutable_change();

CREATE INDEX compliance_consent_events_subject_time_idx
ON compliance_consent_events (subject_kind, subject_id, occurred_at DESC);

CREATE INDEX compliance_consent_events_workspace_time_idx
ON compliance_consent_events (workspace_id, occurred_at DESC)
WHERE workspace_id IS NOT NULL;
