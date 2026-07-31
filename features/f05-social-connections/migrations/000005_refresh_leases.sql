ALTER TABLE f05_social_connections
    ADD COLUMN refresh_lock_id text;

UPDATE f05_social_connections
SET refresh_lock_id = id
WHERE refresh_locked_until IS NOT NULL;

CREATE INDEX f05_social_connections_refresh_lock_idx
    ON f05_social_connections (refresh_lock_id)
    WHERE refresh_lock_id IS NOT NULL;
