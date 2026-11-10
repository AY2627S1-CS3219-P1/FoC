-- Outcomes waiting to be published as moderation.outcome_requested. A closing
-- report or appeal inserts its consequences in the same transaction; the relay
-- leases, publishes and marks each one.
-- +goose Up
CREATE TABLE outcome_outbox (
    event_id     UUID PRIMARY KEY,
    -- The Kafka key: the affected user or review, so its outcomes stay ordered.
    message_key  TEXT NOT NULL CHECK (message_key <> ''),
    -- The serialized moderation.events.v1.OutcomeRequested.
    payload      BYTEA NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token  UUID,
    lease_until  TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    CONSTRAINT outcome_outbox_lease CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);
CREATE INDEX outcome_outbox_pending ON outcome_outbox (occurred_at, event_id) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE outcome_outbox;
