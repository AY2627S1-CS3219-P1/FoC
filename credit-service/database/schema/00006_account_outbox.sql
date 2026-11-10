-- +goose Up
CREATE TABLE account_outbox (
 event_id uuid PRIMARY KEY REFERENCES ledger_entries(id),
 user_id uuid NOT NULL REFERENCES accounts(user_id),
 revision bigint NOT NULL CHECK (revision > 0),
 occurred_at timestamptz NOT NULL,
 lease_token uuid,
 lease_until timestamptz,
 published_at timestamptz
);
CREATE INDEX account_outbox_pending ON account_outbox(occurred_at,event_id) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE account_outbox;
