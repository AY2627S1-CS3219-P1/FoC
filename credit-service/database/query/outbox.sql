-- name: InsertAccountEvent :exec
INSERT INTO account_outbox(event_id,user_id,revision,occurred_at) VALUES ($1,$2,$3,$4);

-- name: ClaimAccountEvent :one
UPDATE account_outbox SET lease_token=sqlc.arg(lease_token),lease_until=sqlc.arg(lease_until)
WHERE event_id=(SELECT candidate.event_id FROM account_outbox candidate
 WHERE candidate.published_at IS NULL AND (candidate.lease_until IS NULL OR candidate.lease_until<=sqlc.arg(now_at))
 ORDER BY candidate.occurred_at,candidate.event_id LIMIT 1 FOR UPDATE OF candidate SKIP LOCKED)
RETURNING *;

-- name: PublishAccountEvent :execrows
UPDATE account_outbox SET published_at=sqlc.arg(now_at),lease_until=NULL,lease_token=NULL
WHERE event_id=sqlc.arg(event_id) AND lease_token=sqlc.arg(lease_token)
 AND lease_until>sqlc.arg(now_at) AND published_at IS NULL;

-- name: RetryAccountEvent :exec
UPDATE account_outbox SET lease_until=$3 WHERE event_id=$1 AND lease_token=$2 AND published_at IS NULL;
