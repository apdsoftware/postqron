CREATE TYPE f04_workspace_status AS ENUM ('active', 'deletion_pending');
CREATE TYPE f04_workspace_role AS ENUM ('owner', 'member');
CREATE TYPE f04_membership_status AS ENUM ('active', 'removed');
CREATE TYPE f04_invitation_status AS ENUM ('pending', 'accepted', 'revoked', 'expired');

CREATE TABLE f04_workspaces (
    id text PRIMARY KEY,
    personal_account_id text NOT NULL UNIQUE,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    status f04_workspace_status NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (updated_at >= created_at)
);

CREATE TABLE f04_memberships (
    workspace_id text NOT NULL REFERENCES f04_workspaces(id) ON DELETE CASCADE,
    account_id text NOT NULL,
    role f04_workspace_role NOT NULL,
    status f04_membership_status NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, account_id),
    CHECK (updated_at >= created_at)
);

CREATE INDEX f04_memberships_active_account_idx
    ON f04_memberships (account_id, workspace_id)
    WHERE status = 'active';

CREATE INDEX f04_memberships_active_owner_idx
    ON f04_memberships (workspace_id)
    WHERE status = 'active' AND role = 'owner';

CREATE TABLE f04_invitations (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES f04_workspaces(id) ON DELETE CASCADE,
    email_digest bytea NOT NULL CHECK (octet_length(email_digest) = 32),
    token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    status f04_invitation_status NOT NULL DEFAULT 'pending',
    expires_at timestamptz NOT NULL,
    accepted_by_account_id text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (expires_at > created_at),
    CHECK (updated_at >= created_at),
    CHECK (
        (status = 'accepted' AND accepted_by_account_id IS NOT NULL)
        OR (status <> 'accepted')
    )
);

CREATE UNIQUE INDEX f04_invitations_one_pending_email_idx
    ON f04_invitations (workspace_id, email_digest)
    WHERE status = 'pending';

CREATE INDEX f04_invitations_pending_capacity_idx
    ON f04_invitations (workspace_id, expires_at)
    WHERE status = 'pending';

CREATE TABLE f04_workspace_audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id text NOT NULL,
    actor_account_id text,
    subject_id text NOT NULL,
    event_type text NOT NULL CHECK (length(btrim(event_type)) > 0),
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'rejected')),
    occurred_at timestamptz NOT NULL
);

CREATE INDEX f04_workspace_audit_events_workspace_time_idx
    ON f04_workspace_audit_events (workspace_id, occurred_at DESC);

CREATE FUNCTION f04_assert_workspace_has_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    checked_workspace_id text;
BEGIN
    checked_workspace_id := COALESCE(NEW.workspace_id, OLD.workspace_id);

    PERFORM 1
    FROM f04_workspaces
    WHERE id = checked_workspace_id
    FOR UPDATE;

    IF FOUND AND NOT EXISTS (
        SELECT 1
        FROM f04_memberships
        WHERE workspace_id = checked_workspace_id
          AND status = 'active'
          AND role = 'owner'
    ) THEN
        RAISE EXCEPTION 'workspace % must retain an active Owner', checked_workspace_id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER f04_memberships_require_owner
AFTER INSERT OR UPDATE OR DELETE ON f04_memberships
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION f04_assert_workspace_has_owner();

CREATE FUNCTION f04_prevent_audit_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'workspace audit events are immutable'
        USING ERRCODE = '55000';
END
$$;

CREATE TRIGGER f04_workspace_audit_events_immutable
BEFORE UPDATE OR DELETE ON f04_workspace_audit_events
FOR EACH ROW
EXECUTE FUNCTION f04_prevent_audit_mutation();
