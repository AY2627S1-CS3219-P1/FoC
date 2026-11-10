-- Submission outbox writes share the review transaction; completion dedup and verdicts commit together.

-- name: InsertReviewSubmittedOutbox :exec
INSERT INTO review_submitted_outbox (event_id, review_id, payload_version, payload)
VALUES (@event_id::UUID, @review_id::UUID, @payload_version, @payload::jsonb);

-- The lease permits recovery if a relay dies before acknowledging publication.
-- name: ClaimReviewSubmittedOutbox :one
WITH candidate AS (
    SELECT event_id
    FROM review_submitted_outbox
    WHERE published_at IS NULL
      AND available_at <= @eligible_at::timestamptz
      AND (lease_until IS NULL OR lease_until <= @eligible_at::timestamptz)
    ORDER BY created_at, event_id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE review_submitted_outbox AS event
SET lease_token = @lease_token::UUID, lease_until = @lease_until::timestamptz
FROM candidate
WHERE event.event_id = candidate.event_id
RETURNING event.event_id, event.review_id, event.payload_version, event.payload, event.lease_token;

-- name: MarkReviewSubmittedOutboxPublished :execrows
UPDATE review_submitted_outbox
SET published_at = @published_at::timestamptz, lease_token = NULL, lease_until = NULL
WHERE event_id = @event_id::UUID AND lease_token = @lease_token::UUID
  AND lease_until > @published_at::timestamptz AND published_at IS NULL;

-- name: RescheduleReviewSubmittedOutbox :execrows
UPDATE review_submitted_outbox
SET available_at = @available_at::timestamptz, lease_token = NULL, lease_until = NULL
WHERE event_id = @event_id::UUID AND lease_token = @lease_token::UUID AND published_at IS NULL;

-- The insert is the dedup gate: zero rows means the completion event was
-- already processed and the handler must no-op.
-- name: RecordReviewModerationProcessedEvent :execrows
INSERT INTO review_moderation_processed_events (event_id)
VALUES (@event_id::UUID)
ON CONFLICT (event_id) DO NOTHING;

-- Lock the review row first; the completion handler reads it in a separate
-- statement afterwards, so the read sees concurrent soft-deletes that committed
-- while the lock was waited on (see LANDMINES/locked-row-relationship-snapshots.md).
-- FOR NO KEY UPDATE rather than FOR UPDATE so the lock does not conflict with
-- the FOR KEY SHARE lock review_removals' foreign key takes on the same row.
-- name: LockReviewForModeration :exec
SELECT id FROM reviews WHERE id = @review_id::UUID FOR NO KEY UPDATE;

-- name: GetReviewForModeration :one
SELECT id, deleted_at, reviewer_id, reviewee_id, moderation_status, content_revision,
       moderation_request_id, applied_run_generation
FROM reviews
WHERE id = @review_id::UUID;

-- Never clears deleted_at; approval cannot undo administrative removal.
-- name: ApplyReviewModerationOutcome :exec
UPDATE reviews
SET moderation_status = @moderation_status,
    reason_codes = @reason_codes::text[],
    applied_run_generation = @applied_run_generation,
    policy_version = @policy_version,
    moderated_at = @moderated_at
WHERE id = @review_id::UUID;
