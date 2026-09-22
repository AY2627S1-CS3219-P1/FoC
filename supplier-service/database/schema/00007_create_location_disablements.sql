-- +goose Up
-- S2.3: disablement intervals. A location is effectively disabled when an
-- uncancelled interval covers now (half-open: starts_at <= now < ends_at).
-- Immediate disable (S2.3.1) is a row starting now with no end;
-- scheduled disablement (S2.3.3) is a row with start and end.
-- Enabling (S2.3.2) or cancelling a future schedule sets cancelled_at.
-- Disabled only warns (S2.3.5); unlike archiving it stays selectable.
CREATE TABLE location_disablements (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    location_id  UUID        NOT NULL REFERENCES locations (id) ON DELETE CASCADE,
    starts_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ends_at      TIMESTAMPTZ CHECK (ends_at IS NULL OR ends_at > starts_at),
    cancelled_at TIMESTAMPTZ,
    reason       TEXT,
    created_by   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX location_disablements_location_idx ON location_disablements (location_id, starts_at);
CREATE INDEX location_disablements_active_idx ON location_disablements (location_id) WHERE cancelled_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS location_disablements;
