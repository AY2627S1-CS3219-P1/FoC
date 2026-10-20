-- Every ordinary local mutation holds the shared gate. Installing a decision
-- takes the exclusive gate before any Order parent locks. Ordinary mutations
-- remain concurrent; a rare suspension transaction briefly fences all of them.
-- name: LockSuspensionGateShared :exec
SELECT pg_advisory_xact_lock_shared(63021920261020::bigint);

-- name: LockSuspensionGateExclusive :exec
SELECT pg_advisory_xact_lock(63021920261020::bigint);

-- name: IsAccountSuspended :one
SELECT EXISTS(SELECT 1 FROM order_account_suspensions WHERE user_id=$1 AND suspended);

-- name: AccountSuspension :one
SELECT * FROM order_account_suspensions WHERE user_id=$1;

-- name: SaveAccountSuspension :exec
INSERT INTO order_account_suspensions(user_id,decision_id,suspension_revision,suspended,admin_id,reason)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT(user_id) DO UPDATE SET decision_id=EXCLUDED.decision_id,
 suspension_revision=EXCLUDED.suspension_revision,suspended=EXCLUDED.suspended,
 admin_id=EXCLUDED.admin_id,reason=EXCLUDED.reason;

-- name: AccountSuspensionOrders :many
SELECT id FROM orders o WHERE o.courier_id=$1 OR EXISTS (
 SELECT 1 FROM order_assignment_attempts a WHERE a.order_id=o.id
 AND a.phase IN ('adjustment_pending','revert_pending') AND (a.courier_id=$1 OR a.caller_id=$1)
) ORDER BY id;
