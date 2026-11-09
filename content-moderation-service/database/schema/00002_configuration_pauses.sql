-- +goose Up
-- A bad configuration blocks new claims across replicas until revalidated.
CREATE TABLE moderation_config_pauses (
    version_id uuid PRIMARY KEY REFERENCES moderation_config_versions(version_id),
    paused_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE moderation_config_pauses;
