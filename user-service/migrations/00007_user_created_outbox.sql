-- +goose Up
CREATE TABLE user_created_outbox (
    event_id        UUID        PRIMARY KEY,
    user_id         UUID        NOT NULL UNIQUE,
    email           TEXT        NOT NULL,
    display_name    TEXT        NOT NULL,
    user_created_at TIMESTAMPTZ NOT NULL,
    payload_version SMALLINT    NOT NULL CHECK (payload_version = 1),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    available_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at    TIMESTAMPTZ,
    lease_token     UUID,
    lease_until     TIMESTAMPTZ,
    CONSTRAINT user_created_outbox_lease_pair_check
        CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);

CREATE INDEX idx_user_created_outbox_pending
    ON user_created_outbox (available_at, created_at, event_id)
    WHERE published_at IS NULL;

-- +goose Down
DROP TABLE user_created_outbox;
