-- name: GetCancellation :one
SELECT * FROM order_cancellations WHERE id = $1;

-- name: InsertCancellation :exec
INSERT INTO order_cancellations (id,order_id,caller_id,reason,status,assignment_id,courier_id,assigned_at,original_amount,generation,attempts,lease_until,next_attempt_at,created_at,updated_at,suspension_decision_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16);

-- name: UpdateCancellation :execrows
UPDATE order_cancellations SET status=sqlc.arg(status), generation=sqlc.arg(generation), attempts=sqlc.arg(attempts), lease_until=sqlc.arg(lease_until), next_attempt_at=sqlc.arg(next_attempt_at), updated_at=sqlc.arg(updated_at)
WHERE id=sqlc.arg(id) AND generation=sqlc.arg(expected_generation) AND status='pending';

-- name: DueCancellations :many
SELECT id FROM order_cancellations WHERE status='pending' AND lease_until <= sqlc.arg(now_at) AND next_attempt_at <= sqlc.arg(now_at) ORDER BY next_attempt_at,id LIMIT sqlc.arg(batch_size);

-- name: HasPendingCancellations :one
SELECT EXISTS(SELECT 1 FROM order_cancellations WHERE status='pending');

-- name: CurrentSuccessfulAssignment :one
SELECT a.* FROM order_assignment_attempts a JOIN orders o ON o.id=a.order_id
WHERE a.order_id=$1 AND a.phase='succeeded' AND a.courier_id=o.courier_id AND a.updated_at=o.assigned_at
ORDER BY a.sequence DESC LIMIT 1;
