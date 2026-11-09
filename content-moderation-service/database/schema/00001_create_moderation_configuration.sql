-- +goose Up

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

-- +goose Down

DROP TABLE moderation_config_state;
DROP TABLE moderation_config_versions;
