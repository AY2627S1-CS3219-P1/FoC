-- +goose Up
CREATE TABLE order_creation_workflows (
 id uuid PRIMARY KEY,
 order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
 requester_id uuid NOT NULL,
 canonical_version integer NOT NULL CHECK (canonical_version = 1),
 canonical_request jsonb NOT NULL CHECK (jsonb_typeof(canonical_request) = 'object'),
 request_hash text NOT NULL CHECK (length(request_hash) = 64),
 reservation_amount bigint NOT NULL CHECK (reservation_amount > 0),
 expires_at timestamptz NOT NULL,
 phase text NOT NULL CHECK (phase IN ('reserve_pending','abort_pending','opened','rejected')),
 generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 lease_until timestamptz NOT NULL,
 next_attempt_at timestamptz NOT NULL,
 last_error text NOT NULL DEFAULT '' CHECK (length(last_error) <= 128),
 rejection text NOT NULL DEFAULT '' CHECK (rejection IN ('','insufficient_credits','creation_expired','reservation_released')),
 result jsonb CHECK (result IS NULL OR jsonb_typeof(result) = 'object'),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK ((phase = 'opened') = (result IS NOT NULL)),
 CHECK ((phase = 'rejected') = (rejection <> ''))
);
CREATE INDEX creation_due ON order_creation_workflows (next_attempt_at,order_id)
 WHERE phase IN ('reserve_pending','abort_pending');
CREATE TABLE order_idempotency_keys (
 caller_id uuid NOT NULL,
 method text NOT NULL,
 idempotency_key uuid NOT NULL,
 resource_id uuid NOT NULL,
 expires_at timestamptz NOT NULL,
 request_hash text NOT NULL CHECK (length(request_hash) = 64),
 PRIMARY KEY (caller_id,method,idempotency_key)
);
CREATE INDEX order_idempotency_keys_resource ON order_idempotency_keys (resource_id);
CREATE INDEX orders_creation_rate ON orders (requester_id,created_at);
-- +goose Down
DROP INDEX orders_creation_rate;
DROP TABLE order_idempotency_keys;
DROP TABLE order_creation_workflows;
