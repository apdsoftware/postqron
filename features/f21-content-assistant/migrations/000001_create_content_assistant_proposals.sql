CREATE TABLE f21_content_assistant_proposals (
    id text PRIMARY KEY CHECK (
        id LIKE 'proposal_%'
        AND length(id) BETWEEN 10 AND 96
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    draft_id text NOT NULL CHECK (length(btrim(draft_id)) > 0),
    draft_revision bigint NOT NULL CHECK (draft_revision > 0),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'confirmed', 'rejected')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    candidates jsonb NOT NULL CHECK (
        jsonb_typeof(candidates) = 'array'
        AND jsonb_array_length(candidates) > 0
    ),
    created_at timestamptz NOT NULL,
    decided_at timestamptz,
    CHECK (
        (status = 'pending' AND decided_at IS NULL)
        OR (status IN ('confirmed', 'rejected') AND decided_at IS NOT NULL)
    )
);

CREATE INDEX f21_content_assistant_workspace_draft_idx
    ON f21_content_assistant_proposals (
        workspace_id,
        draft_id,
        created_at DESC,
        id
    );

CREATE TABLE f21_content_assistant_trace (
    id text PRIMARY KEY CHECK (
        id LIKE 'trace_%'
        AND length(id) BETWEEN 7 AND 96
    ),
    proposal_id text NOT NULL
        REFERENCES f21_content_assistant_proposals (id) ON DELETE CASCADE,
    action text NOT NULL CHECK (
        action IN (
            'proposal.generated',
            'proposal.manual_submitted',
            'proposal.confirmed',
            'proposal.rejected'
        )
    ),
    actor_id text NOT NULL CHECK (length(btrim(actor_id)) > 0),
    correlation_id text NOT NULL CHECK (
        length(btrim(correlation_id)) BETWEEN 1 AND 128
    ),
    occurred_at timestamptz NOT NULL,
    candidate_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (
        jsonb_typeof(candidate_ids) = 'array'
    )
);

CREATE INDEX f21_content_assistant_trace_proposal_time_idx
    ON f21_content_assistant_trace (proposal_id, occurred_at, id);

COMMENT ON COLUMN f21_content_assistant_proposals.candidates IS
    'Immutable original/proposed/diff snapshots; never credentials, secrets, or provider prompts.';
COMMENT ON TABLE f21_content_assistant_trace IS
    'Append-only human-decision timeline using opaque identifiers and bounded metadata.';
