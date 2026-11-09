-- +goose Up

CREATE TABLE moderation_jobs (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    moderation_request_id uuid    NOT NULL UNIQUE,
    source_event_id   uuid        NOT NULL UNIQUE,
    review_id         uuid        NOT NULL,
    reviewer_id       uuid        NOT NULL,
    content_revision  integer     NOT NULL,
    comment           text        NOT NULL,
    config_version_id text,
    policy_version    text,
    run_generation    integer     NOT NULL DEFAULT 1,
    status            text        NOT NULL DEFAULT 'queued'
                          CHECK (status IN ('queued','processing','retry_wait','completed','errored')),
    attempt_count     integer     NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz,
    claim_token       uuid,
    worker_id         text,
    lease_until       timestamptz,
    heartbeat_at      timestamptz,
    outcome           text
                          CHECK (outcome IN ('approved','rejected')),
    reason_codes      text[],
    error_class       text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    finished_at       timestamptz,
    CONSTRAINT completed_decision_is_present
        CHECK (status <> 'completed' OR (outcome IS NOT NULL AND error_class IS NULL)),
    CONSTRAINT errored_has_no_verdict
        CHECK (status <> 'errored' OR outcome IS NULL),
    CONSTRAINT processing_is_claimed
        CHECK (status <> 'processing' OR claim_token IS NOT NULL)
);

CREATE INDEX moderation_jobs_eligible
    ON moderation_jobs (created_at, id)
    WHERE status IN ('queued','retry_wait');

CREATE INDEX moderation_jobs_expired_processing
    ON moderation_jobs (lease_until)
    WHERE status = 'processing';

CREATE INDEX moderation_jobs_reviewer
    ON moderation_jobs (reviewer_id, status DESC, created_at DESC, id);

-- +goose Down

DROP TABLE moderation_jobs;
