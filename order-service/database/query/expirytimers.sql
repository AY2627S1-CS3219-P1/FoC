-- name: DueExpiryTimers :many
SELECT id FROM orders
WHERE status = 'awaiting_courier'
  AND expires_at <= sqlc.arg(now_at)::timestamptz
  AND id > sqlc.arg(after_id)::uuid
ORDER BY id
LIMIT sqlc.arg(batch_size);
