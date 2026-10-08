-- +goose Up
-- A Location is disabled while an uncancelled, unended interval covers now.
-- Cancelling stops a future schedule; ending stops an interval already in use.
-- Disablement warns callers but does not make the Location unselectable.
CREATE TABLE location_disablements (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    location_id  UUID        NOT NULL REFERENCES locations (id) ON DELETE CASCADE,
    starts_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ends_at      TIMESTAMPTZ CHECK (ends_at IS NULL OR ends_at > starts_at),
    cancelled_at TIMESTAMPTZ,
    ended_at     TIMESTAMPTZ,
    reason       TEXT NOT NULL,
    created_by   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revision     BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT disablements_revision_check CHECK (revision > 0),
    CONSTRAINT disablements_reason_check CHECK (char_length(btrim(reason)) BETWEEN 1 AND 500),
    CONSTRAINT disablements_terminal_check CHECK (
        NOT (ended_at IS NOT NULL AND cancelled_at IS NOT NULL)
        AND (ended_at IS NULL OR (ended_at >= starts_at AND (ends_at IS NULL OR ended_at <= ends_at)))
        AND (cancelled_at IS NULL OR cancelled_at < starts_at)
    )
);

CREATE INDEX location_disablements_location_idx ON location_disablements (location_id, starts_at);
CREATE INDEX location_disablements_active_idx ON location_disablements (location_id) WHERE cancelled_at IS NULL;
CREATE INDEX disablements_list_idx ON location_disablements (location_id, starts_at DESC, id);

-- +goose Down
DROP TABLE IF EXISTS location_disablements;
