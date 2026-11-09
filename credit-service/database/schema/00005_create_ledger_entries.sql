-- +goose Up
-- Every change to an account. A user's balance_delta values sum to
-- accounts.balance and their reserved_delta values sum to accounts.reserved.
-- Writers record no entry for a change of zero.
CREATE TABLE ledger_entries (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID        NOT NULL REFERENCES accounts (user_id),
    kind           TEXT        NOT NULL,
    balance_delta  BIGINT      NOT NULL,
    reserved_delta BIGINT      NOT NULL,
    order_id       UUID        REFERENCES reservations (order_id),
    assignment_id  UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ledger_entries_kind_check CHECK (
        kind IN ('GRANT', 'RESERVE', 'ADJUST', 'REVERT', 'RELEASE', 'PAYOUT', 'REFUND')
    ),
    CONSTRAINT ledger_entries_nonzero_check CHECK (balance_delta <> 0 OR reserved_delta <> 0),
    CONSTRAINT ledger_entries_order_check CHECK ((order_id IS NULL) = (kind = 'GRANT')),
    CONSTRAINT ledger_entries_assignment_check CHECK ((assignment_id IS NOT NULL) = (kind IN ('ADJUST', 'REVERT'))),
    CONSTRAINT ledger_entries_assignment_fkey
        FOREIGN KEY (order_id, assignment_id) REFERENCES assignments (order_id, assignment_id),
    CONSTRAINT ledger_entries_deltas_check CHECK (CASE kind
        WHEN 'GRANT' THEN balance_delta > 0 AND reserved_delta = 0
        WHEN 'RESERVE' THEN balance_delta = 0 AND reserved_delta > 0
        WHEN 'ADJUST' THEN balance_delta = 0
        WHEN 'REVERT' THEN balance_delta = 0
        WHEN 'RELEASE' THEN balance_delta = 0 AND reserved_delta < 0
        -- The requester spends from the hold; the courier receives credits.
        WHEN 'PAYOUT' THEN (balance_delta < 0 AND reserved_delta = balance_delta)
            OR (balance_delta > 0 AND reserved_delta = 0)
        WHEN 'REFUND' THEN balance_delta = 0 AND reserved_delta < 0
    END)
);

-- Starting credits are granted at most once per user (C1.3.1).
CREATE UNIQUE INDEX ledger_entries_one_grant_idx ON ledger_entries (user_id) WHERE kind = 'GRANT';
-- ListMyLedgerEntries: newest first.
CREATE INDEX ledger_entries_user_idx ON ledger_entries (user_id, created_at DESC, id DESC);
CREATE INDEX ledger_entries_order_idx ON ledger_entries (order_id) WHERE order_id IS NOT NULL;

CREATE TRIGGER ledger_entries_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION reject_append_only_change();

-- +goose Down
DROP TABLE ledger_entries;
