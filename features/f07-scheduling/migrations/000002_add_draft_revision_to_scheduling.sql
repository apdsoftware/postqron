ALTER TABLE f07_scheduled_posts
    ADD COLUMN draft_revision bigint NOT NULL DEFAULT 1
        CHECK (draft_revision > 0);

ALTER TABLE f07_publication_commands
    ADD COLUMN draft_revision bigint NOT NULL DEFAULT 1
        CHECK (draft_revision > 0);

COMMENT ON COLUMN f07_scheduled_posts.draft_revision IS
    'Immutable F6 draft revision validated for this scheduled post.';
COMMENT ON COLUMN f07_publication_commands.draft_revision IS
    'Immutable F6 draft revision handed to downstream publishing.';
