-- +goose Up
CREATE TABLE order_assignment_attempts (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY NOT NULL UNIQUE,
 order_id uuid NOT NULL REFERENCES orders(id),
 offer_id uuid REFERENCES offers(id) DEFERRABLE INITIALLY DEFERRED,
 caller_id uuid NOT NULL,
 courier_id uuid NOT NULL,
 method text NOT NULL CHECK (method IN ('/order.v1.OrderService/AcceptOrder','/order.v1.OrderService/AcceptOffer')),
 idempotency_key uuid,
 request_hash text NOT NULL,
 amount bigint NOT NULL CHECK (amount > 0),
 original_amount bigint NOT NULL CHECK (original_amount > 0),
 phase text NOT NULL CHECK (phase IN ('adjustment_pending','revert_pending','succeeded','rejected')),
 generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 lease_until timestamptz NOT NULL,
 next_attempt_at timestamptz NOT NULL,
 rejection text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK (updated_at >= created_at),
 CHECK ((method = '/order.v1.OrderService/AcceptOrder' AND idempotency_key IS NOT NULL AND length(request_hash) = 64)
     OR (method = '/order.v1.OrderService/AcceptOffer' AND idempotency_key IS NULL AND request_hash = '')),
 CHECK (offer_id IS NOT NULL OR phase = 'rejected')
);
CREATE UNIQUE INDEX order_assignment_active ON order_assignment_attempts(order_id)
 WHERE phase IN ('adjustment_pending','revert_pending');
CREATE UNIQUE INDEX order_assignment_retry ON order_assignment_attempts(caller_id,method,idempotency_key)
 WHERE idempotency_key IS NOT NULL;
CREATE INDEX order_assignment_offer ON order_assignment_attempts(offer_id,sequence DESC);
CREATE INDEX order_assignment_due ON order_assignment_attempts(next_attempt_at,lease_until)
 WHERE phase IN ('adjustment_pending','revert_pending');
CREATE UNIQUE INDEX offers_one_open_per_courier ON offers(order_id,courier_id) WHERE status = 'open';

-- +goose Down
DROP INDEX offers_one_open_per_courier;
DROP TABLE order_assignment_attempts;
