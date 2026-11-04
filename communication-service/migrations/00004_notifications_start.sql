-- +goose Up
-- When this environment first ran notifications. Earlier order events update
-- chat participants but aren't sent, so a new environment's backlog is skipped.
CREATE TABLE notifications_start (
    singleton  boolean     PRIMARY KEY DEFAULT true CHECK (singleton),
    started_at timestamptz NOT NULL
);

-- An installation that already handled order events skips nothing, so events
-- that arrived while it was down for this upgrade are still sent. A new one
-- records its first run instead.
INSERT INTO notifications_start (started_at)
SELECT '1970-01-01 00:00:00+00'
WHERE EXISTS (SELECT 1 FROM order_participants) OR EXISTS (SELECT 1 FROM order_notifications_sent);

-- +goose Down
DROP TABLE IF EXISTS notifications_start;
