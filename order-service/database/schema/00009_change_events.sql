-- +goose Up
ALTER TABLE order_outbox DROP CONSTRAINT order_outbox_kind_check;
ALTER TABLE order_outbox ADD CONSTRAINT order_outbox_kind_check
 CHECK (kind IN ('order.status_changed','order.outcome','order.courier_warning_requested','order.changed'));
ALTER TABLE order_outbox DROP CONSTRAINT order_outbox_order_id_revision_kind_key;
CREATE UNIQUE INDEX order_outbox_fact_revision ON order_outbox(order_id,revision,kind) WHERE kind<>'order.changed';

-- +goose Down
DELETE FROM order_outbox WHERE kind='order.changed';
DROP INDEX order_outbox_fact_revision;
ALTER TABLE order_outbox ADD CONSTRAINT order_outbox_order_id_revision_kind_key UNIQUE(order_id,revision,kind);
ALTER TABLE order_outbox DROP CONSTRAINT order_outbox_kind_check;
ALTER TABLE order_outbox ADD CONSTRAINT order_outbox_kind_check
 CHECK (kind IN ('order.status_changed','order.outcome','order.courier_warning_requested'));
