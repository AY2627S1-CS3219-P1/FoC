-- +goose Up
-- The deduplication record of each mutating reservation RPC. The identity is
-- (operation, order_id), or (operation, order_id, assignment_id) for ADJUST
-- and REVERT. A repeat with the same request_hash replays the stored outcome,
-- and a different hash is IDEMPOTENCY_CONFLICT. Rows never expire.
--
-- A success stores the revision it produced, so a replay returns that
-- snapshot from reservation_revisions. The only stored rejection is
-- INSUFFICIENT_CREDITS from RESERVE or ADJUST. Other refusals are recomputed
-- from current state and leave no row.
--
-- Settlement has no row here: it comes only from the order.outcome consumer
-- (#136). A settled or released reservation never changes, so a repeated
-- outcome changes nothing; see docs/adr/credit-reservation-holds.md.
CREATE TABLE reservation_operations (
    operation        TEXT        NOT NULL,
    order_id         UUID        NOT NULL,
    assignment_id    UUID,
    request_hash     TEXT        NOT NULL,
    revision         BIGINT,
    rejection_reason TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reservation_operations_identity_key UNIQUE NULLS NOT DISTINCT (operation, order_id, assignment_id),
    CONSTRAINT reservation_operations_operation_check CHECK (
        operation IN ('RESERVE', 'ADJUST', 'REVERT', 'RELEASE')
    ),
    CONSTRAINT reservation_operations_assignment_check CHECK (
        (assignment_id IS NOT NULL) = (operation IN ('ADJUST', 'REVERT'))
    ),
    CONSTRAINT reservation_operations_hash_check CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT reservation_operations_outcome_check CHECK (
        (revision IS NOT NULL AND rejection_reason IS NULL)
        OR (revision IS NULL AND rejection_reason IS NOT DISTINCT FROM 'INSUFFICIENT_CREDITS'
            AND operation IN ('RESERVE', 'ADJUST'))
    ),
    CONSTRAINT reservation_operations_revision_fkey
        FOREIGN KEY (order_id, revision) REFERENCES reservation_revisions (order_id, revision)
);

CREATE TRIGGER reservation_operations_append_only
    BEFORE UPDATE OR DELETE ON reservation_operations
    FOR EACH ROW EXECUTE FUNCTION reject_append_only_change();

-- +goose Down
DROP TABLE reservation_operations;
