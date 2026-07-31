ALTER TABLE f06_composer_duplicate_operations
    ADD COLUMN lease_generation bigint NOT NULL DEFAULT 1 CHECK (lease_generation > 0);
