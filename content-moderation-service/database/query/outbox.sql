-- name: InsertOutbox :exec
INSERT INTO moderation_outbox (
    event_id, job_id, run_generation, payload_version, payload, available_at
) VALUES (
    sqlc.arg(event_id), sqlc.arg(job_id), sqlc.arg(run_generation),
    sqlc.arg(payload_version), sqlc.arg(payload), sqlc.arg(available_at)
);

-- name: ClaimOutboxRow :one
SELECT * FROM moderation_outbox
WHERE published_at IS NULL
  AND available_at <= now()
ORDER BY created_at ASC, event_id ASC
FOR UPDATE SKIP LOCKED
LIMIT 1;

-- name: ClaimOutboxLease :one
-- Atomically leases the oldest unpublished, available outbox row so a single
-- relay owns it until it publishes or releases. The subquery's FOR UPDATE SKIP
-- LOCKED guarantees mutual exclusion between relays; the lease_token set here
-- fences MarkOutboxPublished so only the owning relay may publish.
UPDATE moderation_outbox
SET lease_token = gen_random_uuid(),
    lease_until = clock_timestamp() + sqlc.arg(lease_seconds)::double precision * interval '1 second'
WHERE event_id = (
    SELECT event_id
    FROM moderation_outbox
    WHERE published_at IS NULL
      AND available_at <= now()
      AND (lease_token IS NULL OR lease_until <= clock_timestamp())
    ORDER BY created_at ASC, event_id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: MarkOutboxPublished :execrows
UPDATE moderation_outbox
SET published_at = now(),
    lease_token = NULL,
    lease_until = NULL
WHERE event_id = sqlc.arg(event_id)
  AND lease_token = sqlc.arg(lease_token)
  AND published_at IS NULL
  AND lease_until > clock_timestamp();

-- name: ReleaseOutboxLease :execrows
UPDATE moderation_outbox
SET lease_token = NULL,
    lease_until = now() - interval '1 second'
WHERE event_id = sqlc.arg(event_id)
  AND lease_token = sqlc.arg(lease_token)
  AND published_at IS NULL
  AND lease_until > clock_timestamp();

-- name: LockOutbox :one
-- Lease expiry is checked in a separate write after this lock has been acquired.
SELECT event_id FROM moderation_outbox WHERE event_id = sqlc.arg(event_id) FOR UPDATE;
