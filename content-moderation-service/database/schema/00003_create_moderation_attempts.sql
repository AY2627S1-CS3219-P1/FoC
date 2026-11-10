-- +goose Up

CREATE TABLE moderation_attempts (
    job_id            uuid        NOT NULL REFERENCES moderation_jobs(id),
    run_generation    integer     NOT NULL,
    attempt_number    integer     NOT NULL,
    model_id          text,
    outcome           text        NOT NULL DEFAULT '',
    error_class       text,
    detail            text,
    started_at        timestamptz NOT NULL DEFAULT now(),
    finished_at       timestamptz,
    PRIMARY KEY (job_id, run_generation, attempt_number)
);

-- +goose Down

DROP TABLE moderation_attempts;
