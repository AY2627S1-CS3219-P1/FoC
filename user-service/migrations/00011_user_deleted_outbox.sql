-- Committed hard-deletion facts for the users.user_deleted topic. Deliberately
-- independent of the users row (no FK) so publication survives the hard delete.
-- Exactly one row per user_id; uniqueness enforces a single deletion fact even
-- under concurrent deletion attempts.
-- +goose Up
CREATE TABLE user_deleted_outbox (
    event_id            UUID PRIMARY KEY,
    user_id             UUID NOT NULL UNIQUE,
    deletion_request_id UUID NOT NULL UNIQUE,
    deleted_at          TIMESTAMPTZ NOT NULL,
    payload_version     SMALLINT NOT NULL CHECK (payload_version = 1),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    available_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at        TIMESTAMPTZ,
    lease_token         UUID,
    lease_until         TIMESTAMPTZ,
    CONSTRAINT user_deleted_outbox_lease_pair_check
        CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);

CREATE INDEX idx_user_deleted_outbox_pending
    ON user_deleted_outbox (available_at, created_at, event_id)
    WHERE published_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS user_deleted_outbox;
