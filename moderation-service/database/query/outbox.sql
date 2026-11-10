-- name: InsertOutcome :exec
INSERT INTO outcome_outbox (event_id, message_key, payload)
VALUES (@event_id::UUID, @message_key::TEXT, @payload::BYTEA);

-- Leases the oldest unpublished outcome that no live lease holds.
-- name: ClaimOutcome :one
UPDATE outcome_outbox
SET lease_token = @lease_token::UUID, lease_until = @lease_until::TIMESTAMPTZ
WHERE event_id = (
    SELECT candidate.event_id
    FROM outcome_outbox candidate
    WHERE candidate.published_at IS NULL
      AND (candidate.lease_until IS NULL OR candidate.lease_until <= @now_at::TIMESTAMPTZ)
    ORDER BY candidate.occurred_at, candidate.event_id
    LIMIT 1
    FOR UPDATE OF candidate SKIP LOCKED
)
RETURNING event_id, message_key, payload;

-- name: PublishedOutcome :execrows
UPDATE outcome_outbox
SET published_at = @now_at::TIMESTAMPTZ, lease_token = NULL, lease_until = NULL
WHERE event_id = @event_id::UUID AND lease_token = @lease_token::UUID
  AND lease_until > @now_at::TIMESTAMPTZ AND published_at IS NULL;

-- name: RetryOutcome :exec
UPDATE outcome_outbox
SET lease_until = @retry_at::TIMESTAMPTZ
WHERE event_id = @event_id::UUID AND lease_token = @lease_token::UUID AND published_at IS NULL;
