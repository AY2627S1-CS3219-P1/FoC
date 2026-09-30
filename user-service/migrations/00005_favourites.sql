-- Favourite suppliers. supplier_id belongs to the supplier service, so it has no FK.
-- +goose Up
CREATE TABLE favourite_suppliers (
    user_id     BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    supplier_id UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, supplier_id)
);
CREATE INDEX idx_favourite_suppliers_supplier ON favourite_suppliers (supplier_id);

-- +goose Down
DROP TABLE IF EXISTS favourite_suppliers;
