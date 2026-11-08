-- +goose Up
-- The account-deletion drain probes assignments by courier; the only existing
-- index is order-leading, so add a courier-leading index for the per-user EXISTS.
CREATE INDEX assignments_courier_status ON assignments (courier_id, status);

-- +goose Down
DROP INDEX IF EXISTS assignments_courier_status;
