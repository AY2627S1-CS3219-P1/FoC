-- +goose Up
-- Discover delivered orders whose requester hasn't confirmed receipt, so they
-- can auto-complete. Only orders currently in Delivered are indexed.
CREATE INDEX orders_receipt_timers ON orders (delivered_at, id)
WHERE status = 'delivered';

-- +goose Down
DROP INDEX orders_receipt_timers;
