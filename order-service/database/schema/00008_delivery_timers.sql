-- +goose Up

-- Restrict discovery to the relevant live populations rather than retained
-- terminal history. Expiry can first bound the original deadline; delivery
-- discovery uses an exact numeric duration comparison on a covered UUID scan.
CREATE INDEX orders_expiry_timers ON orders (expires_at, id)
WHERE status = 'awaiting_courier';

CREATE INDEX orders_delivery_timers ON orders (id)
INCLUDE (picked_up_at, delivery_duration_ns)
WHERE status = 'delivering';

-- +goose Down
DROP INDEX orders_delivery_timers;
DROP INDEX orders_expiry_timers;
