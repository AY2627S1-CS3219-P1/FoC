-- name: DueReceiptTimers :many
SELECT id FROM orders
WHERE status = 'delivered'
  AND delivered_at <= sqlc.arg(delivered_before)::timestamptz
  AND id > sqlc.arg(after_id)::uuid
ORDER BY id
LIMIT sqlc.arg(batch_size);
