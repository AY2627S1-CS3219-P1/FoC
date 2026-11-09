-- name: GetAssignmentAttempt :one
SELECT * FROM order_assignment_attempts WHERE id = $1;

-- name: ActiveAssignmentAttempt :one
SELECT * FROM order_assignment_attempts WHERE order_id = $1
 AND phase IN ('adjustment_pending','revert_pending');

-- name: LatestOfferAssignment :one
SELECT * FROM order_assignment_attempts WHERE offer_id = $1
 ORDER BY sequence DESC LIMIT 1;

-- name: AssignmentOfferOrder :one
SELECT order_id FROM offers WHERE id = $1;

-- name: DueAssignmentAttempts :many
SELECT a.id FROM order_assignment_attempts a JOIN orders o ON o.id = a.order_id
 WHERE a.phase IN ('adjustment_pending','revert_pending') AND a.lease_until <= sqlc.arg(now_at)
 AND (a.next_attempt_at <= sqlc.arg(now_at) OR
      (a.phase = 'adjustment_pending' AND (o.status <> 'awaiting_courier' OR o.expires_at <= sqlc.arg(now_at))))
 ORDER BY a.next_attempt_at,a.id LIMIT sqlc.arg(batch_size);

-- name: InsertAssignmentAttempt :exec
INSERT INTO order_assignment_attempts
 (id,order_id,offer_id,caller_id,courier_id,method,idempotency_key,request_hash,amount,original_amount,
 phase,generation,attempts,lease_until,next_attempt_at,rejection,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18);

-- name: UpdateAssignmentAttempt :execrows
UPDATE order_assignment_attempts SET phase=sqlc.arg(phase), generation=sqlc.arg(generation),
 attempts=sqlc.arg(attempts),lease_until=sqlc.arg(lease_until),next_attempt_at=sqlc.arg(next_attempt_at),
 rejection=sqlc.arg(rejection),updated_at=sqlc.arg(updated_at)
 WHERE id=sqlc.arg(id) AND generation=sqlc.arg(expected_generation)
 AND phase IN ('adjustment_pending','revert_pending');

-- name: HasPendingAssignments :one
SELECT EXISTS(SELECT 1 FROM order_assignment_attempts WHERE phase IN ('adjustment_pending','revert_pending'));

-- name: HasRejectedAcceptance :one
SELECT EXISTS(SELECT 1 FROM order_assignment_attempts
 WHERE order_id = $1 AND caller_id = $2 AND method = '/order.v1.OrderService/AcceptOrder'
 AND phase = 'rejected' AND offer_id IS NULL);
