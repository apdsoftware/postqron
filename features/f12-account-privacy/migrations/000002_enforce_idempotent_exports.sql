CREATE UNIQUE INDEX account_privacy_active_export_idx
    ON account_privacy_export_requests (account_id, scope, COALESCE(workspace_id, ''))
    WHERE status IN ('queued', 'ready');

ALTER TABLE account_privacy_deletion_requests
    DROP COLUMN immediate;
