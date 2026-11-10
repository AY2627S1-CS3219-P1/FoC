-- name: ChangeRecipients :many
SELECT o.requester_id AS user_id FROM orders o WHERE o.id=$1
UNION SELECT o.courier_id FROM orders o WHERE o.id=$1 AND o.courier_id IS NOT NULL
UNION SELECT courier_id FROM offers WHERE order_id=$1
UNION SELECT courier_id FROM order_assignment_history WHERE order_id=$1
UNION SELECT caller_id FROM order_assignment_attempts WHERE order_id=$1
UNION SELECT caller_id FROM order_cancellations WHERE order_id=$1;

-- name: InsertOrderChange :exec
INSERT INTO order_outbox(event_id,order_id,revision,ordinal,kind,payload_version,payload,created_at)
SELECT sqlc.arg(event_id),sqlc.arg(order_id),sqlc.arg(revision),
 COALESCE(MAX(ordinal)+1,0),'order.changed',1,sqlc.arg(payload),sqlc.arg(created_at)
FROM order_outbox WHERE order_id=sqlc.arg(order_id) AND revision=sqlc.arg(revision);
