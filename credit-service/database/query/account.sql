-- name: InsertAccount :execrows
-- Opens an empty account. The starting credits are posted as a GRANT entry.
INSERT INTO accounts (user_id, balance, created_at, updated_at)
VALUES (sqlc.arg(user_id), 0, sqlc.arg(now_at), sqlc.arg(now_at))
ON CONFLICT (user_id) DO NOTHING;

-- name: GetAccount :one
SELECT * FROM accounts WHERE user_id = $1;

-- name: LockAccount :one
SELECT * FROM accounts WHERE user_id = $1 FOR UPDATE;

-- name: ApplyAccountDelta :one
UPDATE accounts
SET balance = balance + sqlc.arg(balance_delta),
    reserved = reserved + sqlc.arg(reserved_delta),
    revision = revision + 1,
    updated_at = sqlc.arg(now_at)
WHERE user_id = sqlc.arg(user_id)
RETURNING *;

-- name: InsertLedgerEntry :exec
-- IDs are UUIDv7, so entries posted at one timestamp list in posting order.
INSERT INTO ledger_entries (id, user_id, kind, balance_delta, reserved_delta, order_id, assignment_id, created_at)
VALUES (
    sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(balance_delta), sqlc.arg(reserved_delta),
    sqlc.narg(order_id), sqlc.narg(assignment_id), sqlc.arg(now_at)
);

-- name: ListLedgerEntries :many
SELECT * FROM ledger_entries
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size)::bigint OFFSET sqlc.arg(page_offset)::bigint;

-- name: CountLedgerEntries :one
SELECT count(*) FROM ledger_entries WHERE user_id = $1;
