-- +goose Up
-- When this environment first ran notifications. Earlier order events update
-- chat participants but aren't sent, so a new environment's backlog is skipped.
CREATE TABLE notifications_start (
    singleton  boolean     PRIMARY KEY DEFAULT true CHECK (singleton),
    started_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS notifications_start;
