-- name: LockOrder :exec
-- Serializes every operation on one order, including orders Credit has not seen.
SELECT pg_advisory_xact_lock(hashtextextended('credit:order:' || sqlc.arg(order_id)::uuid::text, 0));

-- name: GetReservation :one
SELECT * FROM reservations WHERE order_id = $1;

-- name: LockReservation :one
SELECT * FROM reservations WHERE order_id = $1 FOR UPDATE;

-- name: GetReservationRevision :one
SELECT * FROM reservation_revisions WHERE order_id = $1 AND revision = $2;

-- name: InsertReservation :exec
INSERT INTO reservations (
    order_id, requester_id, status, original_amount, reserved_amount, assignment_id,
    release_reason, settlement_outcome, paid_to_courier, returned_to_requester,
    picked_up_at, delivered_at, delivery_duration, settled_at, revision, created_at, updated_at
) VALUES (
    sqlc.arg(order_id), sqlc.narg(requester_id), sqlc.arg(status), sqlc.arg(original_amount),
    sqlc.arg(reserved_amount), sqlc.narg(assignment_id), sqlc.narg(release_reason),
    sqlc.narg(settlement_outcome), sqlc.narg(paid_to_courier), sqlc.narg(returned_to_requester),
    sqlc.narg(picked_up_at), sqlc.narg(delivered_at), sqlc.narg(delivery_duration),
    sqlc.narg(settled_at), sqlc.arg(revision), sqlc.arg(now_at), sqlc.arg(now_at)
);

-- name: UpdateReservation :execrows
UPDATE reservations
SET status = sqlc.arg(status),
    reserved_amount = sqlc.arg(reserved_amount),
    assignment_id = sqlc.narg(assignment_id),
    release_reason = sqlc.narg(release_reason),
    settlement_outcome = sqlc.narg(settlement_outcome),
    paid_to_courier = sqlc.narg(paid_to_courier),
    returned_to_requester = sqlc.narg(returned_to_requester),
    picked_up_at = sqlc.narg(picked_up_at),
    delivered_at = sqlc.narg(delivered_at),
    delivery_duration = sqlc.narg(delivery_duration),
    settled_at = sqlc.narg(settled_at),
    revision = sqlc.arg(revision),
    updated_at = sqlc.arg(now_at)
WHERE order_id = sqlc.arg(order_id) AND revision = sqlc.arg(expected_revision);

-- name: GetAssignment :one
SELECT * FROM assignments WHERE order_id = $1 AND assignment_id = $2;

-- name: InsertAssignment :exec
INSERT INTO assignments (order_id, assignment_id, courier_id, offer_amount, status, created_at, reverted_at)
VALUES (
    sqlc.arg(order_id), sqlc.arg(assignment_id), sqlc.narg(courier_id), sqlc.narg(offer_amount),
    sqlc.arg(status), sqlc.arg(now_at), sqlc.narg(reverted_at)
);

-- name: RevertAssignment :execrows
UPDATE assignments
SET status = 'REVERTED', reverted_at = sqlc.arg(now_at)::timestamptz
WHERE order_id = sqlc.arg(order_id) AND assignment_id = sqlc.arg(assignment_id) AND status = 'APPLIED';

-- name: GetOperation :one
SELECT * FROM reservation_operations
WHERE operation = sqlc.arg(operation) AND order_id = sqlc.arg(order_id)
    AND assignment_id IS NOT DISTINCT FROM sqlc.narg(assignment_id);

-- name: InsertOperation :exec
INSERT INTO reservation_operations (operation, order_id, assignment_id, request_hash, revision, rejection_reason, created_at)
VALUES (
    sqlc.arg(operation), sqlc.arg(order_id), sqlc.narg(assignment_id), sqlc.arg(request_hash),
    sqlc.narg(revision), sqlc.narg(rejection_reason), sqlc.arg(now_at)
);
