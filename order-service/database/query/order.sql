-- name: GetOrder :one
SELECT * FROM orders WHERE id = $1;

-- name: LockOrder :one
SELECT * FROM orders WHERE id = $1 FOR UPDATE;

-- name: InsertOrder :exec
INSERT INTO orders (id, requester_id, courier_id, status, pickup, delivery, delivery_instructions, item_description, reward_credits, reserved_credits, delivery_duration_ns, expires_at, assigned_at, picked_up_at, delivered_at, completed_at, closed_at, close_reason, item_location, unattended_delivery, revision, created_at, updated_at, pending_cancellation_id) VALUES (sqlc.arg(id), sqlc.arg(requester_id), sqlc.narg(courier_id), sqlc.arg(status), sqlc.arg(pickup), sqlc.arg(delivery), sqlc.narg(delivery_instructions), sqlc.arg(item_description), sqlc.arg(reward_credits), sqlc.arg(reserved_credits), sqlc.arg(delivery_duration_ns), sqlc.arg(expires_at), sqlc.narg(assigned_at), sqlc.narg(picked_up_at), sqlc.narg(delivered_at), sqlc.narg(completed_at), sqlc.narg(closed_at), sqlc.narg(close_reason), sqlc.narg(item_location), sqlc.arg(unattended_delivery), sqlc.arg(revision), sqlc.arg(created_at), sqlc.arg(updated_at), sqlc.narg(pending_cancellation_id));

-- name: UpdateOrder :execrows
UPDATE orders SET pending_cancellation_id = sqlc.narg(pending_cancellation_id), courier_id = sqlc.narg(courier_id), status = sqlc.arg(status), pickup = sqlc.arg(pickup), delivery = sqlc.arg(delivery), delivery_instructions = sqlc.narg(delivery_instructions), item_description = sqlc.arg(item_description), reward_credits = sqlc.arg(reward_credits), reserved_credits = sqlc.arg(reserved_credits), delivery_duration_ns = sqlc.arg(delivery_duration_ns), expires_at = sqlc.arg(expires_at), assigned_at = sqlc.narg(assigned_at), picked_up_at = sqlc.narg(picked_up_at), delivered_at = sqlc.narg(delivered_at), completed_at = sqlc.narg(completed_at), closed_at = sqlc.narg(closed_at), close_reason = sqlc.narg(close_reason), item_location = sqlc.narg(item_location), unattended_delivery = sqlc.arg(unattended_delivery), revision = sqlc.arg(revision), updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision);

-- name: ListOrders :many
SELECT * FROM orders WHERE
 (NOT sqlc.arg(open_only)::boolean OR (status = 'awaiting_courier' AND requester_id <> sqlc.arg(caller_id)::uuid AND expires_at > sqlc.arg(now_at)::timestamptz))
 AND (sqlc.arg(user_id)::text = '' OR
   ((sqlc.arg(role)::text IN ('','requester') AND requester_id = NULLIF(sqlc.arg(user_id)::text,'')::uuid) OR
    (sqlc.arg(role)::text IN ('','courier') AND (courier_id = NULLIF(sqlc.arg(user_id)::text,'')::uuid OR EXISTS (SELECT 1 FROM order_assignment_history h WHERE h.order_id=orders.id AND h.courier_id=NULLIF(sqlc.arg(user_id)::text,'')::uuid)))))
 AND (sqlc.arg(status_filter)::text = '' OR status = sqlc.arg(status_filter))
 AND (sqlc.arg(pickup_id)::text = '' OR pickup->>'location_id' = sqlc.arg(pickup_id)::text)
 AND (sqlc.arg(delivery_building_name)::text = '' OR delivery->>'building_name' = sqlc.arg(delivery_building_name)::text) ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset)::bigint;

-- name: CountOrders :one
SELECT count(*) FROM orders WHERE
 (NOT sqlc.arg(open_only)::boolean OR (status = 'awaiting_courier' AND requester_id <> sqlc.arg(caller_id)::uuid AND expires_at > sqlc.arg(now_at)::timestamptz))
 AND (sqlc.arg(user_id)::text = '' OR
   ((sqlc.arg(role)::text IN ('','requester') AND requester_id = NULLIF(sqlc.arg(user_id)::text,'')::uuid) OR
    (sqlc.arg(role)::text IN ('','courier') AND (courier_id = NULLIF(sqlc.arg(user_id)::text,'')::uuid OR EXISTS (SELECT 1 FROM order_assignment_history h WHERE h.order_id=orders.id AND h.courier_id=NULLIF(sqlc.arg(user_id)::text,'')::uuid)))))
 AND (sqlc.arg(status_filter)::text = '' OR status = sqlc.arg(status_filter))
 AND (sqlc.arg(pickup_id)::text = '' OR pickup->>'location_id' = sqlc.arg(pickup_id)::text)
 AND (sqlc.arg(delivery_building_name)::text = '' OR delivery->>'building_name' = sqlc.arg(delivery_building_name)::text);

-- name: OrderOffers :many
SELECT * FROM offers WHERE order_id = $1 ORDER BY created_at DESC,id DESC;

-- name: InsertOffer :exec
INSERT INTO offers (id,order_id,courier_id,credits,status,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: UpdateOffer :execrows
UPDATE offers SET credits = sqlc.arg(credits), status = sqlc.arg(status), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND order_id = sqlc.arg(order_id) AND courier_id = sqlc.arg(courier_id) AND status = sqlc.arg(expected_status);

-- name: InsertAudit :exec
INSERT INTO order_audit (order_id,revision,action,actor_kind,actor_id,previous_status,resulting_status,occurred_at,details) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: InsertFact :exec
INSERT INTO order_outbox (event_id,order_id,revision,ordinal,kind,payload_version,payload,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8);
