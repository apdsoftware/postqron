CREATE TABLE f17_collaboration_comments (
    id text PRIMARY KEY CHECK (id LIKE 'comment_%'),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    draft_id text NOT NULL CHECK (length(btrim(draft_id)) > 0),
    author_account_id text NOT NULL CHECK (length(btrim(author_account_id)) > 0),
    body text NOT NULL CHECK (
        length(btrim(body)) > 0
        AND char_length(body) <= 4000
    ),
    created_at timestamptz NOT NULL,
    resolved_by_account_id text,
    resolved_at timestamptz,
    CHECK (
        (resolved_by_account_id IS NULL AND resolved_at IS NULL)
        OR
        (
            length(btrim(resolved_by_account_id)) > 0
            AND resolved_at IS NOT NULL
            AND resolved_at >= created_at
        )
    )
);

CREATE INDEX f17_collaboration_comments_draft_idx
    ON f17_collaboration_comments (workspace_id, draft_id, created_at, id);

CREATE INDEX f17_collaboration_open_comments_idx
    ON f17_collaboration_comments (workspace_id, draft_id)
    WHERE resolved_at IS NULL;

CREATE TABLE f17_collaboration_reviews (
    id text PRIMARY KEY CHECK (id LIKE 'review_%'),
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    draft_id text NOT NULL CHECK (length(btrim(draft_id)) > 0),
    draft_revision bigint NOT NULL CHECK (draft_revision > 0),
    status text NOT NULL CHECK (
        status IN ('pending', 'changes_requested', 'approved')
    ),
    requested_by_account_id text NOT NULL
        CHECK (length(btrim(requested_by_account_id)) > 0),
    requested_at timestamptz NOT NULL,
    decided_by_account_id text,
    decided_at timestamptz,
    decision_note text NOT NULL DEFAULT ''
        CHECK (char_length(decision_note) <= 1000),
    CHECK (
        (status = 'pending' AND decided_by_account_id IS NULL AND decided_at IS NULL)
        OR
        (
            status <> 'pending'
            AND length(btrim(decided_by_account_id)) > 0
            AND decided_at IS NOT NULL
            AND decided_at >= requested_at
        )
    ),
    CHECK (
        status <> 'changes_requested' OR length(btrim(decision_note)) > 0
    ),
    CHECK (
        status <> 'approved'
        OR requested_by_account_id <> decided_by_account_id
    )
);

CREATE UNIQUE INDEX f17_collaboration_one_pending_review_idx
    ON f17_collaboration_reviews (workspace_id, draft_id)
    WHERE status = 'pending';

CREATE INDEX f17_collaboration_latest_review_idx
    ON f17_collaboration_reviews (
        workspace_id,
        draft_id,
        sequence DESC
    );

CREATE TABLE f17_collaboration_audit_events (
    id text PRIMARY KEY CHECK (id LIKE 'audit_%'),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    actor_account_id text,
    target_type text NOT NULL CHECK (target_type IN ('comment', 'review', 'draft')),
    target_id text NOT NULL CHECK (length(btrim(target_id)) > 0),
    action text NOT NULL CHECK (
        action IN (
            'comment.created',
            'comment.resolved',
            'review.requested',
            'review.changes_requested',
            'review.approved',
            'scheduling.blocked'
        )
    ),
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'denied')),
    occurred_at timestamptz NOT NULL
);

CREATE INDEX f17_collaboration_audit_workspace_idx
    ON f17_collaboration_audit_events (workspace_id, occurred_at DESC, id);

CREATE TABLE f17_collaboration_outbox (
    id text PRIMARY KEY CHECK (id LIKE 'event_%'),
    event_type text NOT NULL CHECK (event_type LIKE 'collaboration.%.v1'),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    actor_account_id text,
    draft_id text NOT NULL CHECK (length(btrim(draft_id)) > 0),
    correlation_id text NOT NULL CHECK (length(btrim(correlation_id)) > 0),
    occurred_at timestamptz NOT NULL,
    data jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(data) = 'object'),
    published_at timestamptz
);

CREATE INDEX f17_collaboration_pending_outbox_idx
    ON f17_collaboration_outbox (occurred_at, id)
    WHERE published_at IS NULL;

COMMENT ON COLUMN f17_collaboration_outbox.data IS
    'Opaque review metadata for F9; comment bodies and personal data are excluded.';
