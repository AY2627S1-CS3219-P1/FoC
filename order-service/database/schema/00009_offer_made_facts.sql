-- +goose Up
-- Courier offers are published so the requester can be told.
ALTER TABLE order_outbox DROP CONSTRAINT order_outbox_kind_check;
ALTER TABLE order_outbox ADD CONSTRAINT order_outbox_kind_check CHECK (
    kind IN ('order.status_changed', 'order.outcome', 'order.courier_warning_requested', 'order.offer_made')
);

-- +goose Down
DELETE FROM order_outbox WHERE kind = 'order.offer_made';
ALTER TABLE order_outbox DROP CONSTRAINT order_outbox_kind_check;
ALTER TABLE order_outbox ADD CONSTRAINT order_outbox_kind_check CHECK (
    kind IN ('order.status_changed', 'order.outcome', 'order.courier_warning_requested')
);
