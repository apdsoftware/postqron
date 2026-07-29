CREATE TABLE f04_workspace_selections (
    account_id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES f04_workspaces(id) ON DELETE CASCADE,
    selected_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (updated_at >= selected_at)
);

CREATE INDEX f04_workspace_selections_workspace_idx
    ON f04_workspace_selections (workspace_id, updated_at DESC);
