-- name: ClaimOrderFact :one
UPDATE order_outbox SET lease_token=sqlc.arg(lease_token),lease_until=sqlc.arg(lease_until)
WHERE event_id = (
 SELECT candidate.event_id FROM order_outbox candidate
 WHERE candidate.published_at IS NULL
 AND (candidate.lease_until IS NULL OR candidate.lease_until <= sqlc.arg(now_at))
 AND NOT EXISTS (SELECT 1 FROM order_outbox earlier WHERE earlier.order_id=candidate.order_id AND earlier.published_at IS NULL AND (earlier.revision,earlier.ordinal) < (candidate.revision,candidate.ordinal))
 ORDER BY candidate.created_at,candidate.order_id,candidate.revision,candidate.ordinal
 LIMIT 1 FOR UPDATE OF candidate SKIP LOCKED
)
RETURNING *;

-- name: MarkOrderFactPublished :execrows
UPDATE order_outbox SET published_at=sqlc.arg(now_at),lease_token=NULL,lease_until=NULL
WHERE event_id=sqlc.arg(event_id) AND lease_token=sqlc.arg(lease_token) AND lease_until>sqlc.arg(now_at) AND published_at IS NULL;

-- name: RetryOrderFact :exec
UPDATE order_outbox SET lease_until=$3 WHERE event_id=$1 AND lease_token=$2 AND published_at IS NULL;

-- name: FactActor :one
SELECT actor_kind,actor_id FROM order_audit WHERE order_id=$1 AND revision=$2;
