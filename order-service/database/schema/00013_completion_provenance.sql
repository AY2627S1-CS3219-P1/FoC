-- +goose Up
ALTER TABLE orders ADD COLUMN automatically_completed boolean NOT NULL DEFAULT false;

-- Preserve provenance if receipt timers ran before this migration.
UPDATE orders SET automatically_completed = true
WHERE status = 'completed' AND EXISTS (
 SELECT 1 FROM order_audit a
 WHERE a.order_id = orders.id AND a.action = 'auto_complete' AND a.actor_kind = 'system'
);

ALTER TABLE orders ADD CONSTRAINT orders_automatic_completion
 CHECK (NOT automatically_completed OR (status = 'completed' AND completed_at IS NOT NULL));

-- +goose Down
ALTER TABLE orders DROP COLUMN automatically_completed;
