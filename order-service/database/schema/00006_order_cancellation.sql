-- +goose Up
CREATE TABLE order_cancellations (
 id uuid PRIMARY KEY,
 order_id uuid NOT NULL REFERENCES orders(id),
 caller_id uuid NOT NULL,
 reason text NOT NULL CHECK (length(reason) <= 500),
 status text NOT NULL CHECK (status IN ('pending','succeeded','superseded')),
 assignment_id uuid REFERENCES order_assignment_attempts(id),
 courier_id uuid,
 assigned_at timestamptz,
 original_amount bigint NOT NULL CHECK (original_amount >= 0),
 generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 lease_until timestamptz NOT NULL,
 next_attempt_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
 UNIQUE (order_id,id)
);
CREATE UNIQUE INDEX order_cancellation_pending ON order_cancellations(order_id) WHERE status = 'pending';
CREATE INDEX order_cancellation_due ON order_cancellations(next_attempt_at,lease_until) WHERE status = 'pending';
ALTER TABLE orders ADD COLUMN pending_cancellation_id uuid;
ALTER TABLE orders ADD CONSTRAINT orders_pending_cancellation FOREIGN KEY (id,pending_cancellation_id)
 REFERENCES order_cancellations(order_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE orders ADD CONSTRAINT orders_pending_cancellation_state CHECK (pending_cancellation_id IS NULL OR status = 'awaiting_pickup');

-- +goose Down
-- Pending withdrawals still need their durable reversal identity and retry state.
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM order_cancellations WHERE status = 'pending') THEN
  RAISE EXCEPTION 'cannot downgrade while courier withdrawals are pending';
 END IF;
END $$;
-- +goose StatementEnd
DELETE FROM order_idempotency_keys WHERE method = '/order.v1.OrderService/CancelOrder';
ALTER TABLE orders DROP CONSTRAINT orders_pending_cancellation_state;
ALTER TABLE orders DROP CONSTRAINT orders_pending_cancellation;
ALTER TABLE orders DROP COLUMN pending_cancellation_id;
DROP TABLE order_cancellations;
