-- name: LockUser :exec
-- Serializes account opening and deletion marking for one user. Both paths
-- take this advisory lock first, so a delayed users.create or lazy open can
-- never resurrect an account that a concurrent deletion marker retires.
SELECT pg_advisory_xact_lock(hashtextextended('credit:user:' || sqlc.arg(user_id)::uuid::text, 0));

-- name: GetDeletedUser :one
SELECT user_id FROM deleted_users WHERE user_id = $1;

-- name: InsertDeletedUser :execrows
-- A redelivered event, or a second fact for the same user, changes nothing.
INSERT INTO deleted_users (user_id, event_id, deleted_at)
VALUES (sqlc.arg(user_id), sqlc.arg(event_id), sqlc.arg(deleted_at))
ON CONFLICT DO NOTHING;
