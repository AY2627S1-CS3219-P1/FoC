-- +goose Up

-- Admin daily order counts scan a creation-time range.
CREATE INDEX orders_created ON orders (created_at);

-- +goose Down
DROP INDEX orders_created;
