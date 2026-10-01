-- name: LockIdempotency :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(scope)::text,0));

-- name: DeleteExpiredIdempotency :exec
DELETE FROM supplier_idempotency
WHERE caller_id=sqlc.arg(caller_id) AND method=sqlc.arg(method) AND key=sqlc.arg(key)::uuid
    AND expires_at<=sqlc.arg(now_at)::timestamptz;

-- name: GetIdempotency :one
SELECT * FROM supplier_idempotency WHERE caller_id=$1 AND method=$2 AND key=$3;

-- name: InsertIdempotency :exec
INSERT INTO supplier_idempotency (caller_id,method,key,request_hash,resource_id,expires_at) VALUES ($1,$2,$3,$4,$5,$6);
