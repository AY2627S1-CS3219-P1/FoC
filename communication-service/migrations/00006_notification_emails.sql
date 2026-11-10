-- +goose Up
-- Email fallback is accepted durably before acknowledging the order event.
-- Rows without a user_emails projection remain pending until it catches up.
CREATE TABLE notification_emails (
    event_id     uuid        NOT NULL,
    user_id      uuid        NOT NULL,
    body         text        NOT NULL,
    created_at   timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    status       text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'rejected')),
    attempts     integer     NOT NULL DEFAULT 0,
    attempted_at timestamptz,
    last_error   text        NOT NULL DEFAULT '',
    PRIMARY KEY (event_id, user_id)
);

CREATE INDEX notification_emails_ready ON notification_emails (available_at, created_at)
WHERE status = 'pending';

-- +goose Down
DROP TABLE notification_emails;
