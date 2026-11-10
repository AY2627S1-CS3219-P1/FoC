-- name: GetCreditTotals :one
-- Credits currently reserved, and lifetime credits returned to requesters and
-- paid to couriers, across all users from one statement snapshot.
SELECT
    (SELECT COALESCE(SUM(reserved), 0) FROM accounts)::bigint AS reserved,
    COALESCE(SUM(returned_to_requester), 0)::bigint AS returned,
    COALESCE(SUM(paid_to_courier), 0)::bigint AS transferred
FROM reservations
WHERE status IN ('RELEASED', 'SETTLED');

-- name: DailyPayouts :many
-- Credits paid to couriers in [from_at, to_at), and the number of those
-- payments, by Asia/Singapore calendar day. Requester-side PAYOUT entries
-- record the same payment from the hold, so only courier credits count.
SELECT
    (created_at AT TIME ZONE 'Asia/Singapore')::date AS day,
    SUM(balance_delta)::bigint AS credits,
    count(*) AS payments
FROM ledger_entries
WHERE kind = 'PAYOUT' AND balance_delta > 0
    AND created_at >= sqlc.arg(from_at)::timestamptz AND created_at < sqlc.arg(to_at)::timestamptz
GROUP BY day
ORDER BY day;
