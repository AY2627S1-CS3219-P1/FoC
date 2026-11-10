-- +goose Up

-- Commit invalid decoded submissions before acknowledging them; retain bounded diagnostics.
CREATE TABLE moderation_quarantine (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    topic         text        NOT NULL,
    message_key   text        NOT NULL,
    event_id      text,
    reason        text        NOT NULL,
    payload_summary text      NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE moderation_quarantine IS 'Size-limited diagnostics for submissions that cannot safely map to jobs, persisted before acknowledgement so poison deliveries do not block progress and conflicting duplicates remain inspectable.';

-- +goose Down

DROP TABLE moderation_quarantine;
