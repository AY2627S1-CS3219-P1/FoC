-- +goose Up

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
