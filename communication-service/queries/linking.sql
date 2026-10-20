-- name: ReplaceCode :exec
-- The partial unique index on unused codes makes concurrent calls for one
-- user leave a single unused code.
INSERT INTO telegram_link_codes (code_hash, user_id, expires_at, created_at)
VALUES (sqlc.arg(code_hash), sqlc.arg(user_id), sqlc.arg(expires_at), sqlc.arg(created_at))
ON CONFLICT (user_id) WHERE used_at IS NULL DO UPDATE
SET code_hash = EXCLUDED.code_hash, expires_at = EXCLUDED.expires_at, created_at = EXCLUDED.created_at;

-- name: LockChat :exec
-- Serializes links for one chat until the transaction ends.
SELECT pg_advisory_xact_lock(sqlc.arg(chat_id)::bigint);

-- name: UseCode :one
-- Marks an unused, unexpired code as used, so each code is redeemable once.
UPDATE telegram_link_codes SET used_at = sqlc.arg(now)::timestamptz
WHERE code_hash = sqlc.arg(code_hash) AND used_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING user_id;

-- name: ReleaseChat :exec
-- Unlinks the chat from any other user.
DELETE FROM telegram_links WHERE chat_id = sqlc.arg(chat_id) AND user_id <> sqlc.arg(user_id);

-- name: SaveLink :exec
INSERT INTO telegram_links (user_id, chat_id, telegram_username, linked_at)
VALUES (sqlc.arg(user_id), sqlc.arg(chat_id), sqlc.narg(telegram_username), sqlc.arg(linked_at))
ON CONFLICT (user_id) DO UPDATE
SET chat_id = EXCLUDED.chat_id, telegram_username = EXCLUDED.telegram_username, linked_at = EXCLUDED.linked_at;

-- name: GetLink :one
SELECT user_id, chat_id, telegram_username, linked_at FROM telegram_links WHERE user_id = sqlc.arg(user_id);

-- name: DeleteLink :exec
DELETE FROM telegram_links WHERE user_id = sqlc.arg(user_id);

-- name: DeleteLinkByChat :exec
DELETE FROM telegram_links WHERE chat_id = sqlc.arg(chat_id);
