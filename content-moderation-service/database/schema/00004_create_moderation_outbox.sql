-- +goose Up

CREATE TABLE moderation_outbox (
    event_id        uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id          uuid        NOT NULL REFERENCES moderation_jobs(id),
    run_generation  integer     NOT NULL,
    payload_version integer     NOT NULL DEFAULT 1,
    payload         jsonb       NOT NULL,
    available_at    timestamptz NOT NULL DEFAULT now(),
    published_at    timestamptz,
    lease_token     uuid,
    lease_until     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (job_id, run_generation)
);

CREATE INDEX moderation_outbox_unpublished
    ON moderation_outbox (available_at)
    WHERE published_at IS NULL;

-- +goose Down

DROP TABLE moderation_outbox;
