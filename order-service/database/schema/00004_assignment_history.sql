-- +goose Up
CREATE TABLE order_assignment_history (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 order_id uuid NOT NULL REFERENCES orders(id),
 courier_id uuid NOT NULL,
 assigned_revision bigint CHECK (assigned_revision > 0),
 assigned_at timestamptz,
 ended_revision bigint CHECK (ended_revision > assigned_revision),
 ended_at timestamptz,
 end_reason text,
 is_current boolean NOT NULL,
 source text NOT NULL CHECK (source IN ('transition','assignment_fact','removal_fact','current_snapshot')),
 UNIQUE (order_id, assigned_revision),
 CHECK (NOT is_current OR (ended_revision IS NULL AND ended_at IS NULL AND end_reason IS NULL)),
 CHECK (ended_at IS NULL OR assigned_at IS NULL OR ended_at >= assigned_at)
);

-- Recover only identities and times supported by durable assignment facts.
INSERT INTO order_assignment_history (order_id,courier_id,assigned_revision,assigned_at,is_current,source)
SELECT order_id,(payload->>'courier_id')::uuid,revision,created_at,false,'assignment_fact'
FROM order_outbox
WHERE kind='order.status_changed' AND payload->>'previous_status'='awaiting_courier'
 AND payload->>'status'='awaiting_pickup' AND payload->>'courier_id' IS NOT NULL;

-- A removal fact can establish participation even without an older assignment fact.
INSERT INTO order_assignment_history (order_id,courier_id,ended_revision,ended_at,end_reason,is_current,source)
SELECT f.order_id,(f.payload->>'previous_courier_id')::uuid,f.revision,f.created_at,f.payload->>'action',false,'removal_fact'
FROM order_outbox f
WHERE f.kind='order.status_changed' AND f.payload->>'previous_courier_id' IS NOT NULL
 AND f.payload->>'courier_id' IS DISTINCT FROM f.payload->>'previous_courier_id'
 AND NOT EXISTS (SELECT 1 FROM order_assignment_history h WHERE h.order_id=f.order_id
  AND h.courier_id=(f.payload->>'previous_courier_id')::uuid AND h.assigned_revision<f.revision);

UPDATE order_assignment_history h SET
 ended_revision=ending.revision, ended_at=ending.created_at, end_reason=ending.reason
FROM (
 SELECT h2.id, f.revision,f.created_at,f.payload->>'action' AS reason,
 row_number() OVER (PARTITION BY h2.id ORDER BY f.revision) AS ordinal
 FROM order_assignment_history h2 JOIN order_outbox f ON f.order_id=h2.order_id
 AND f.revision>h2.assigned_revision AND f.kind='order.status_changed'
 AND f.payload->>'previous_courier_id'=h2.courier_id::text
 AND f.payload->>'courier_id' IS DISTINCT FROM f.payload->>'previous_courier_id'
) ending WHERE h.id=ending.id AND ending.ordinal=1;

-- Only the latest evidenced assignment can represent the current snapshot.
UPDATE order_assignment_history h SET is_current=true
FROM orders o WHERE h.order_id=o.id AND h.courier_id=o.courier_id AND h.ended_revision IS NULL
 AND h.assigned_revision=(SELECT max(h2.assigned_revision) FROM order_assignment_history h2 WHERE h2.order_id=o.id);

-- Existing current identity is evidence, but not evidence of an assignment revision.
INSERT INTO order_assignment_history (order_id,courier_id,assigned_at,is_current,source)
SELECT o.id,o.courier_id,o.assigned_at,true,'current_snapshot' FROM orders o
WHERE o.courier_id IS NOT NULL AND NOT EXISTS
 (SELECT 1 FROM order_assignment_history h WHERE h.order_id=o.id AND h.is_current);

CREATE UNIQUE INDEX assignment_history_current ON order_assignment_history(order_id) WHERE is_current;
CREATE INDEX assignment_history_courier ON order_assignment_history(courier_id,order_id);
CREATE INDEX orders_delivery_building ON orders ((delivery->>'building_name'));
-- +goose Down
DROP INDEX orders_delivery_building;
DROP TABLE order_assignment_history;
