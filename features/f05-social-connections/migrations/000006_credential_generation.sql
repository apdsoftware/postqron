ALTER TABLE f05_social_connections
    ADD COLUMN credential_generation bigint NOT NULL DEFAULT 1
        CHECK (credential_generation > 0);
