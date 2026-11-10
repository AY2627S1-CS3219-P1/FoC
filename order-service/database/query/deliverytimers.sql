-- name: DueDeliveryTimers :many
SELECT id FROM orders
WHERE status = 'delivering'
  AND picked_up_at IS NOT NULL
  AND (EXTRACT(EPOCH FROM sqlc.arg(now_at)::timestamptz) - EXTRACT(EPOCH FROM picked_up_at)) * 1000000000 >= delivery_duration_ns::numeric * 2
  AND id > sqlc.arg(after_id)::uuid
ORDER BY id
LIMIT sqlc.arg(batch_size);
