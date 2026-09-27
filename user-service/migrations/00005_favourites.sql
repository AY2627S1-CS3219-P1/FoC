-- +goose Up
-- U3.4: supplier_id lives in the supplier service, so no FK.
-- Validate on add; delete rows on SupplierDeleted events.
CREATE TABLE favourite_suppliers (
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    supplier_id UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, supplier_id)
);
-- For fan-out on supplier deletion.
CREATE INDEX idx_favourite_suppliers_supplier ON favourite_suppliers (supplier_id);

-- +goose Down
DROP TABLE IF EXISTS favourite_suppliers;
