-- +goose Up
-- Content moderation service owned schema.
--
-- The moderation_job row is the durable state machine for one submitted
-- review's assessment. Ingestion deduplicates on moderation_request_id (the
-- row's uniqueness IS the inbox). The outbox table records the exactly-once
-- terminal event per job generation; the attempts table is append-only history.

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

CREATE TABLE moderation_attempts (
    job_id            uuid        NOT NULL REFERENCES moderation_jobs(id),
    run_generation    integer     NOT NULL,
    attempt_number    integer     NOT NULL,
    config_version_id text,
    model_id          text,
    outcome           text        NOT NULL DEFAULT '',
    error_class       text,
    detail            text,
    started_at        timestamptz NOT NULL DEFAULT now(),
    finished_at       timestamptz,
    PRIMARY KEY (job_id, run_generation, attempt_number)
);

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

CREATE TABLE moderation_quarantine (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    topic         text        NOT NULL,
    message_key   text        NOT NULL,
    event_id      text,
    reason        text        NOT NULL,
    payload_summary text      NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE moderation_config_versions (
    version_id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id            text        NOT NULL,
    parameters          jsonb       NOT NULL,
    output_mode         text        NOT NULL
                          CHECK (output_mode IN ('json_schema','json_object')),
    policy_version      text        NOT NULL,
    provider_fingerprint text       NOT NULL,
    created_by          text        NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE moderation_config_state (
    id                  bool        PRIMARY KEY DEFAULT true,
    active_version_id   uuid        REFERENCES moderation_config_versions(version_id),
    activated_by        text,
    activated_at        timestamptz,
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE moderation_admin_audit (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    action            text        NOT NULL,
    job_id            uuid,
    config_version_id uuid,
    actor             text        NOT NULL,
    detail            jsonb,
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE moderation_admin_audit;
DROP TABLE moderation_config_state;
DROP TABLE moderation_config_versions;
DROP TABLE moderation_quarantine;
DROP TABLE moderation_outbox;
DROP TABLE moderation_attempts;
DROP TABLE moderation_jobs;
