CREATE TABLE f08_bluesky_tid_namespaces (
    repository text PRIMARY KEY CHECK (
        length(btrim(repository)) BETWEEN 1 AND 1024
    ),
    last_tid bigint NOT NULL CHECK (
        last_tid >= -1
    ),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE f08_bluesky_tid_allocations (
    repository text NOT NULL
        REFERENCES f08_bluesky_tid_namespaces (repository),
    idempotency_key text NOT NULL CHECK (
        length(btrim(idempotency_key)) BETWEEN 1 AND 256
    ),
    tid bigint NOT NULL CHECK (
        tid >= 0
    ),
    allocated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository, idempotency_key),
    UNIQUE (repository, tid)
);

COMMENT ON TABLE f08_bluesky_tid_allocations IS
    'Durable idempotent Bluesky TID allocations written before createRecord.';
