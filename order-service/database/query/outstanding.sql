-- name: GetUserOutstandingWork :one
-- Read-only account-deletion drain check. Every EXISTS runs in this single
-- statement, so all checks share one database snapshot and an offer that
-- transitions into an assignment between checks cannot hide both.
--
-- Nonterminal orders involve the user as requester or current courier.
-- Open and accepting offers stay outstanding until they are withdrawn, voided
-- or selected into an assignment; a former courier's resolved history is not
-- listed. Pending admissions are durable records that can still create a new
-- obligation (a creation reserving credit, an assignment applying an offer);
-- pending workflows are recovery records that resolve or compensate existing
-- work (creation aborts, assignment reverts, pending cancellations).
SELECT
    EXISTS (
        SELECT 1 FROM orders
        WHERE status NOT IN ('completed', 'cancelled', 'expired', 'failed', 'rejected')
            AND (requester_id = sqlc.arg(user_id)::uuid OR courier_id = sqlc.arg(user_id)::uuid)
    ) AS nonterminal_order,
    EXISTS (
        SELECT 1 FROM offers
        WHERE courier_id = sqlc.arg(user_id)::uuid AND status IN ('open', 'accepting')
    ) AS open_offer,
    bool(EXISTS (
        SELECT 1 FROM order_creation_workflows
        WHERE requester_id = sqlc.arg(user_id)::uuid AND phase = 'reserve_pending'
    ) OR EXISTS (
        SELECT 1 FROM order_assignment_attempts
        WHERE (caller_id = sqlc.arg(user_id)::uuid OR courier_id = sqlc.arg(user_id)::uuid)
            AND phase = 'adjustment_pending'
    )) AS pending_admission,
    bool(EXISTS (
        SELECT 1 FROM order_creation_workflows
        WHERE requester_id = sqlc.arg(user_id)::uuid AND phase = 'abort_pending'
    ) OR EXISTS (
        SELECT 1 FROM order_assignment_attempts
        WHERE (caller_id = sqlc.arg(user_id)::uuid OR courier_id = sqlc.arg(user_id)::uuid)
            AND phase = 'revert_pending'
    ) OR EXISTS (
        SELECT 1 FROM order_cancellations
        WHERE caller_id = sqlc.arg(user_id)::uuid AND status = 'pending'
    )) AS pending_workflow;

