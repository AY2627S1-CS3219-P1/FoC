-- name: CountRecentCreations :one
SELECT count(*) FROM orders WHERE requester_id = sqlc.arg(requester_id) AND created_at > sqlc.arg(since_at)::timestamptz;

-- name: InsertCreationWorkflow :exec
INSERT INTO order_creation_workflows (id,order_id,requester_id,canonical_version,canonical_request,request_hash,reservation_amount,expires_at,phase,lease_until,next_attempt_at,created_at,updated_at)
VALUES (sqlc.arg(id),sqlc.arg(order_id),sqlc.arg(requester_id),1,sqlc.arg(canonical_request),sqlc.arg(request_hash),sqlc.arg(reservation_amount),sqlc.arg(expires_at),'reserve_pending',sqlc.arg(lease_until),sqlc.arg(next_attempt_at),sqlc.arg(created_at),sqlc.arg(updated_at));

-- name: GetCreationWorkflow :one
SELECT * FROM order_creation_workflows WHERE order_id = $1;

-- name: GetCreationWorkflowByID :one
SELECT * FROM order_creation_workflows WHERE id = $1;

-- name: LockCreationWorkflow :one
SELECT * FROM order_creation_workflows WHERE order_id = $1 FOR UPDATE;

-- name: TryLockCreationOrder :one
SELECT * FROM orders WHERE id = $1 FOR UPDATE SKIP LOCKED;

-- name: UpdateCreationWorkflow :execrows
UPDATE order_creation_workflows SET phase = sqlc.arg(phase), generation = sqlc.arg(generation), attempts = sqlc.arg(attempts), lease_until = sqlc.arg(lease_until), next_attempt_at = sqlc.arg(next_attempt_at), last_error = sqlc.arg(last_error), rejection = sqlc.arg(rejection), result = sqlc.narg(result), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(expected_generation);

-- name: DueCreations :many
SELECT order_id FROM order_creation_workflows
WHERE phase IN ('reserve_pending','abort_pending') AND lease_until <= sqlc.arg(now_at)::timestamptz
 AND (next_attempt_at <= sqlc.arg(now_at)::timestamptz OR (phase = 'reserve_pending' AND expires_at <= sqlc.arg(now_at)::timestamptz))
ORDER BY next_attempt_at,order_id LIMIT sqlc.arg(batch_size);

-- name: HasPendingCreations :one
SELECT EXISTS (SELECT 1 FROM order_creation_workflows WHERE phase IN ('reserve_pending','abort_pending'));
