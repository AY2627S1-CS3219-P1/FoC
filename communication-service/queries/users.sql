-- name: SaveUserEmail :exec
-- Replays keep the first copy; User Service doesn't change emails.
INSERT INTO user_emails (user_id, email, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(email), sqlc.arg(created_at))
ON CONFLICT (user_id) DO NOTHING;

-- name: GetUserEmail :one
SELECT email FROM user_emails WHERE user_id = sqlc.arg(user_id);
