-- The relay checks for an earlier unpublished outcome with the same key before
-- claiming one, so per-key publication order survives publish retries.
-- +goose Up
CREATE INDEX outcome_outbox_pending_key ON outcome_outbox (message_key, occurred_at, event_id)
WHERE published_at IS NULL;

-- +goose Down
DROP INDEX outcome_outbox_pending_key;
