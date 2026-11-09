-- name: LockOrderIdempotencyCaller :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key)::text, 0));

-- name: GetOrderIdempotencyKey :one
SELECT * FROM order_idempotency_keys
WHERE caller_id = $1 AND method = $2 AND idempotency_key = $3;

-- name: InsertOrderIdempotencyKey :exec
INSERT INTO order_idempotency_keys (caller_id,method,idempotency_key,resource_id,request_hash,expires_at)
VALUES ($1,$2,$3,$4,$5,$6);

-- name: DeleteOrderIdempotencyKey :exec
DELETE FROM order_idempotency_keys WHERE caller_id = $1 AND method = $2 AND idempotency_key = $3;
