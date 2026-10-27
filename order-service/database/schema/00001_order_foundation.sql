-- +goose Up
CREATE TABLE orders (
 id uuid PRIMARY KEY,
 requester_id uuid NOT NULL,
 courier_id uuid,
 status text NOT NULL CHECK (status IN ('pending_reservation','awaiting_courier','awaiting_pickup','delivering','delivered','completed','cancelled','expired','failed','rejected')),
 pickup jsonb NOT NULL CHECK (jsonb_typeof(pickup) = 'object'),
 delivery jsonb NOT NULL CHECK (jsonb_typeof(delivery) = 'object'),
 delivery_instructions text CHECK (length(delivery_instructions) <= 500),
 item_description text NOT NULL CHECK (length(item_description) BETWEEN 1 AND 1000),
 reward_credits bigint NOT NULL CHECK (reward_credits > 0),
 reserved_credits bigint NOT NULL CHECK (reserved_credits >= 0),
 delivery_duration_ns bigint NOT NULL CHECK (delivery_duration_ns BETWEEN 300000000000 AND 86400000000000),
 expires_at timestamptz NOT NULL,
 assigned_at timestamptz,
 picked_up_at timestamptz,
 delivered_at timestamptz,
 completed_at timestamptz,
 closed_at timestamptz,
 close_reason text CHECK (length(close_reason) <= 500),
 item_location text CHECK (length(item_location) BETWEEN 1 AND 500),
 unattended_delivery boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL CHECK (revision > 0),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK (courier_id IS NULL OR courier_id <> requester_id),
 CHECK (status NOT IN ('awaiting_pickup','delivering','delivered','completed') OR courier_id IS NOT NULL),
 CHECK (updated_at >= created_at)
);
CREATE INDEX orders_open ON orders (created_at DESC,id DESC) WHERE status = 'awaiting_courier';
CREATE INDEX orders_requester ON orders (requester_id,created_at DESC,id DESC);
CREATE INDEX orders_courier ON orders (courier_id,created_at DESC,id DESC);
CREATE INDEX orders_pickup ON orders ((pickup->>'location_id'));
CREATE INDEX orders_delivery ON orders ((delivery->>'location_id'));
CREATE TABLE offers (
 id uuid PRIMARY KEY,
 order_id uuid NOT NULL REFERENCES orders(id),
 courier_id uuid NOT NULL,
 credits bigint NOT NULL CHECK (credits > 0),
 status text NOT NULL CHECK (status IN ('open','accepting','accepted','void')),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK (updated_at >= created_at)
);
CREATE INDEX offers_order ON offers (order_id,created_at DESC,id DESC);
CREATE INDEX offers_courier ON offers (order_id,courier_id);
CREATE TABLE order_audit (
 order_id uuid NOT NULL REFERENCES orders(id),
 revision bigint NOT NULL CHECK (revision > 0),
 action text NOT NULL CHECK (length(action) BETWEEN 1 AND 64),
 actor_kind text NOT NULL CHECK (actor_kind IN ('user','admin','system')),
 actor_id uuid,
 previous_status text NOT NULL,
 resulting_status text NOT NULL,
 occurred_at timestamptz NOT NULL,
 details jsonb NOT NULL CHECK (octet_length(details::text) <= 4096),
 PRIMARY KEY (order_id,revision),
 CHECK (actor_kind = 'system' OR actor_id IS NOT NULL)
);
CREATE TABLE order_outbox (
 event_id uuid PRIMARY KEY,
 order_id uuid NOT NULL REFERENCES orders(id),
 revision bigint NOT NULL CHECK (revision > 0),
 ordinal integer NOT NULL CHECK (ordinal >= 0),
 kind text NOT NULL CHECK (kind IN ('order.status_changed','order.outcome','order.courier_warning_requested')),
 payload_version integer NOT NULL CHECK (payload_version > 0),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
 created_at timestamptz NOT NULL,
 published_at timestamptz,
 UNIQUE (order_id,revision,ordinal),
 UNIQUE (order_id,revision,kind)
);
CREATE UNIQUE INDEX order_terminal_outcome ON order_outbox (order_id) WHERE kind = 'order.outcome';
CREATE INDEX order_outbox_pending ON order_outbox (created_at,order_id,revision,ordinal) WHERE published_at IS NULL;
-- +goose Down
DROP TABLE order_outbox;
DROP TABLE order_audit;
DROP TABLE offers;
DROP TABLE orders;
