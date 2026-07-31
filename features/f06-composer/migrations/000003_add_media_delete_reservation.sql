ALTER TABLE f06_composer_media
    ADD COLUMN deleting_at timestamptz;

CREATE INDEX f06_composer_media_deleting_idx
    ON f06_composer_media (workspace_id, deleting_at)
    WHERE deleting_at IS NOT NULL;
