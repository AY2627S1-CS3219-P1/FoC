-- name: UserExists :one
SELECT EXISTS (
    SELECT 1 FROM users WHERE id = @user_id::UUID
) AS exists;

-- name: InsertReview :exec
INSERT INTO reviews (id, reviewer_id, reviewee_id, order_id, rating, comment, created_at)
VALUES (@id::UUID, @reviewer_id::UUID, @reviewee_id::UUID, @order_id::UUID, @rating, @comment, now());

-- name: FindReview :one
SELECT id, reviewer_id, reviewee_id, order_id, rating, comment, created_at
FROM reviews
WHERE id = @id::UUID;

-- name: DeleteReview :exec
DELETE FROM reviews
WHERE id = @id::UUID;

-- name: InsertReviewRemoval :exec
INSERT INTO review_removals (id, reviewer_id, reviewee_id, order_id, reason, created_at, created_by)
VALUES (@id::UUID, @reviewer_id::UUID, @reviewee_id::UUID, @order_id::UUID, @reason, now(), @created_by::UUID);

-- name: ListReviewsForUser :many
SELECT id, reviewer_id, reviewee_id, order_id, rating, comment, created_at
FROM reviews
WHERE reviewee_id = @user_id::UUID
ORDER BY created_at DESC, id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: ListReviewsFromUser :many
SELECT id, reviewer_id, reviewee_id, order_id, rating, comment, created_at
FROM reviews
WHERE reviewer_id = @user_id::UUID
ORDER BY created_at DESC, id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountReviewsForUser :one
SELECT COUNT(*)
FROM reviews
WHERE reviewee_id = @user_id::UUID;

-- name: CountReviewsFromUser :one
SELECT COUNT(*)
FROM reviews
WHERE reviewer_id = @user_id::UUID;

-- name: ListReviewRemovals :many
SELECT id, reviewer_id, reviewee_id, order_id, reason, created_at, created_by
FROM review_removals
ORDER BY created_at DESC, id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountReviewRemovals :one
SELECT COUNT(*)
FROM review_removals;

-- Lock the reviewee row first, then read the aggregate in a separate statement,
-- so a concurrent submit/remove about the same reviewee converges on the count
-- and mean. A locking query must not also read the aggregate (see LANDMINES).
-- name: LockReviewee :exec
SELECT id FROM users WHERE id = @reviewee_id::UUID FOR UPDATE;

-- name: RevieweeStatistics :one
SELECT COUNT(*) AS review_count, COALESCE(SUM(rating), 0)::bigint AS sum_rating
FROM reviews
WHERE reviewee_id = @reviewee_id::UUID;

-- name: UpdateRevieweeStats :exec
UPDATE users
SET review_count = @count, avg_rating = @avg_rating, updated_at = now()
WHERE id = @reviewee_id::UUID;
