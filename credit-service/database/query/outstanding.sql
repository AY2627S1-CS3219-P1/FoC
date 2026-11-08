-- name: GetUserOutstandingWork :one
-- Read-only account-deletion drain check. Every EXISTS runs in this single
-- statement, so all checks share one database snapshot. A nonexistent account
-- is not outstanding, and this read never creates one.
--
-- RESERVED_BALANCE: the account holds a nonzero reserved amount.
-- ACTIVE_RESERVATION: HELD or ASSIGNED reservations with the user as requester
-- or as the courier of the applied assignment. UNSETTLED_ASSIGNMENT: applied
-- courier assignments whose hold has not settled; reserved = 0 alone does not
-- establish courier safety. Completed immutable ledger rows are not listed.
SELECT
    bool(EXISTS (
        SELECT 1 FROM accounts
        WHERE user_id = sqlc.arg(user_id)::uuid AND reserved <> 0
    )) AS reserved_balance,
    bool(EXISTS (
        SELECT 1 FROM reservations
        WHERE requester_id = sqlc.arg(user_id)::uuid AND status IN ('HELD', 'ASSIGNED')
    ) OR EXISTS (
        SELECT 1 FROM reservations
        JOIN assignments
            ON assignments.order_id = reservations.order_id
            AND assignments.assignment_id = reservations.assignment_id
        WHERE reservations.status = 'ASSIGNED' AND assignments.courier_id = sqlc.arg(user_id)::uuid
    )) AS active_reservation,
    bool(EXISTS (
        SELECT 1 FROM assignments
        JOIN reservations
            ON reservations.order_id = assignments.order_id
            AND reservations.assignment_id = assignments.assignment_id
        WHERE assignments.courier_id = sqlc.arg(user_id)::uuid
            AND assignments.status = 'APPLIED'
            AND reservations.status = 'ASSIGNED'
    )) AS unsettled_assignment;
