-- +goose Up
CREATE TABLE supplier_idempotency (
    caller_id TEXT NOT NULL,
    method TEXT NOT NULL,
    key UUID NOT NULL,
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    resource_id UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (caller_id, method, key)
);

CREATE INDEX supplier_idempotency_expiry_idx ON supplier_idempotency (expires_at);

-- +goose Down
DROP TABLE supplier_idempotency;
