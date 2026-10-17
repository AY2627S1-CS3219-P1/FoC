-- +goose Up
ALTER TABLE order_creation_keys RENAME TO order_idempotency_keys;
ALTER TABLE order_idempotency_keys DROP CONSTRAINT order_creation_keys_method_check;
ALTER TABLE order_idempotency_keys DROP CONSTRAINT order_creation_keys_workflow_id_fkey;
ALTER TABLE order_idempotency_keys RENAME COLUMN workflow_id TO resource_id;
ALTER TABLE order_idempotency_keys ADD COLUMN request_hash text;

-- Existing keys still reference creation workflows. Backfill before requiring
-- the shared payload hash; unresolved and expired mappings are both preserved.
UPDATE order_idempotency_keys k SET request_hash = w.request_hash
FROM order_creation_workflows w WHERE w.id = k.resource_id;

ALTER TABLE order_idempotency_keys ALTER COLUMN request_hash SET NOT NULL;
ALTER TABLE order_idempotency_keys ADD CONSTRAINT order_idempotency_keys_request_hash_check CHECK (length(request_hash) = 64);
ALTER TABLE order_idempotency_keys RENAME CONSTRAINT order_creation_keys_pkey TO order_idempotency_keys_pkey;
ALTER INDEX creation_keys_workflow RENAME TO order_idempotency_keys_resource;

-- +goose Down
-- Refuse rollback if generic mappings cannot be represented by the old schema.
-- Never delete offer keys or dangling resources to make a downgrade succeed.
ALTER TABLE order_idempotency_keys ADD CONSTRAINT order_creation_keys_method_check CHECK (method = '/order.v1.OrderService/CreateOrder');
ALTER TABLE order_idempotency_keys ADD CONSTRAINT order_creation_keys_workflow_id_fkey FOREIGN KEY (resource_id) REFERENCES order_creation_workflows(id);
ALTER TABLE order_idempotency_keys DROP COLUMN request_hash;
ALTER TABLE order_idempotency_keys RENAME COLUMN resource_id TO workflow_id;
ALTER TABLE order_idempotency_keys RENAME CONSTRAINT order_idempotency_keys_pkey TO order_creation_keys_pkey;
ALTER INDEX order_idempotency_keys_resource RENAME TO creation_keys_workflow;
ALTER TABLE order_idempotency_keys RENAME TO order_creation_keys;
