-- +goose Up
-- Admin daily transaction volume scans courier-side payouts by time.
CREATE INDEX ledger_entries_payouts_idx ON ledger_entries (created_at)
WHERE kind = 'PAYOUT' AND balance_delta > 0;

-- +goose Down
DROP INDEX ledger_entries_payouts_idx;
