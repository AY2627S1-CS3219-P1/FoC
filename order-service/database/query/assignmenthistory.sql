-- name: HasAssignmentHistory :one
SELECT EXISTS (SELECT 1 FROM order_assignment_history WHERE order_id=$1 AND courier_id=$2);

-- name: OpenAssignmentHistory :exec
INSERT INTO order_assignment_history(order_id,courier_id,assigned_revision,assigned_at,is_current,source)
VALUES ($1,$2,$3,$4,true,'transition');

-- name: CloseAssignmentHistory :execrows
UPDATE order_assignment_history SET ended_revision=sqlc.arg(ended_revision),ended_at=sqlc.arg(ended_at),
 end_reason=sqlc.arg(end_reason),is_current=false
WHERE order_id=sqlc.arg(order_id) AND courier_id=sqlc.arg(courier_id) AND is_current;
