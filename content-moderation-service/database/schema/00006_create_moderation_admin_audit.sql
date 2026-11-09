-- +goose Up

CREATE TABLE moderation_admin_audit (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    action            text        NOT NULL,
    job_id            uuid,
    config_version_id uuid,
    actor             text        NOT NULL,
    detail            jsonb,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT moderation_admin_audit_action_check
        CHECK (action IN ('ACTIVATE_CONFIG', 'RETRY_JOB', 'RESUME_CONFIGURATION_PAUSED_JOB'))
);

-- +goose Down

DROP TABLE moderation_admin_audit;
