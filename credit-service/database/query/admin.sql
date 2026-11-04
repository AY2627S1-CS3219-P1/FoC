-- name: GetCreditTotals :one
-- Credits currently reserved, and lifetime credits returned to requesters and
-- paid to couriers, across all users from one statement snapshot.
SELECT
    (SELECT COALESCE(SUM(reserved), 0) FROM accounts)::bigint AS reserved,
    COALESCE(SUM(returned_to_requester), 0)::bigint AS returned,
    COALESCE(SUM(paid_to_courier), 0)::bigint AS transferred
FROM reservations
WHERE status IN ('RELEASED', 'SETTLED');
