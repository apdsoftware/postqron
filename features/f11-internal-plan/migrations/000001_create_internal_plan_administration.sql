CREATE TABLE f11_internal_plan_allowlist (
    account_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    active boolean NOT NULL,
    allowed_at timestamptz NOT NULL,
    allowed_by_account_id uuid NOT NULL,
    revoked_at timestamptz,
    revoked_by_account_id uuid,
    PRIMARY KEY (account_id, workspace_id),
    CHECK (
        (active AND revoked_at IS NULL AND revoked_by_account_id IS NULL)
        OR (
            NOT active
            AND revoked_at IS NOT NULL
            AND revoked_by_account_id IS NOT NULL
        )
    )
);

COMMENT ON TABLE f11_internal_plan_allowlist IS
    'Server-managed allowlist; never expose through public API contracts.';

CREATE TABLE f11_internal_plan_bindings (
    workspace_id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    active boolean NOT NULL,
    assigned_at timestamptz NOT NULL,
    assigned_by_account_id uuid NOT NULL,
    revoked_at timestamptz,
    revoked_by_account_id uuid,
    FOREIGN KEY (account_id, workspace_id)
        REFERENCES f11_internal_plan_allowlist (account_id, workspace_id),
    CHECK (
        (active AND revoked_at IS NULL AND revoked_by_account_id IS NULL)
        OR (
            NOT active
            AND revoked_at IS NOT NULL
            AND revoked_by_account_id IS NOT NULL
        )
    )
);

COMMENT ON TABLE f11_internal_plan_bindings IS
    'Private account-to-workspace binding for the F10 enforcement override.';

CREATE INDEX f11_internal_plan_allowlist_active_idx
    ON f11_internal_plan_allowlist (workspace_id, account_id)
    WHERE active;

REVOKE SELECT, INSERT, UPDATE, DELETE, TRUNCATE
    ON f11_internal_plan_allowlist, f11_internal_plan_bindings
    FROM PUBLIC;
