-- name: UserExists :one
SELECT EXISTS (
    SELECT 1 FROM users WHERE id = @user_id::UUID
) AS exists;

-- name: InsertReview :exec
INSERT INTO reviews (id, reviewer_id, reviewee_id, order_id, rating, comment, created_at)
VALUES (@id::UUID, @reviewer_id::UUID, @reviewee_id::UUID, @order_id::UUID, @rating, @comment, now());

-- Find the live review and lock it: removal serialises on this lock, so a
-- concurrent removal of the same review sees the row already soft-deleted and
-- reports not found instead of writing a second audit row.
-- name: FindReviewLocked :one
SELECT id, deleted_at, reviewer_id, reviewee_id, order_id, rating, comment, created_at
FROM reviews
WHERE id = @id::UUID AND deleted_at IS NULL
FOR UPDATE;

-- Mark the review removed; the row is retained so review_removals can keep
-- referencing it. A no-op when the row is already soft-deleted.
-- name: SoftDeleteReview :exec
UPDATE reviews
SET deleted_at = now()
WHERE id = @id::UUID AND deleted_at IS NULL;

-- name: InsertReviewRemoval :exec
INSERT INTO review_removals (id, review_id, reviewer_id, reviewee_id, order_id, reason, created_at, created_by)
VALUES (@id::UUID, @review_id::UUID, @reviewer_id::UUID, @reviewee_id::UUID, @order_id::UUID, @reason, now(), @created_by::UUID);

-- name: ListReviewsForUser :many
SELECT id, deleted_at, reviewer_id, reviewee_id, order_id, rating, comment, created_at
FROM reviews
WHERE reviewee_id = @user_id::UUID AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: ListReviewsFromUser :many
SELECT id, deleted_at, reviewer_id, reviewee_id, order_id, rating, comment, created_at
FROM reviews
WHERE reviewer_id = @user_id::UUID AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountReviewsForUser :one
SELECT COUNT(*)
FROM reviews
WHERE reviewee_id = @user_id::UUID AND deleted_at IS NULL;

-- name: CountReviewsFromUser :one
SELECT COUNT(*)
FROM reviews
WHERE reviewer_id = @user_id::UUID AND deleted_at IS NULL;

-- name: ListReviewRemovals :many
SELECT id, review_id, reviewer_id, reviewee_id, order_id, reason, created_at, created_by
FROM review_removals
ORDER BY created_at DESC, id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountReviewRemovals :one
SELECT COUNT(*)
FROM review_removals;

-- Lock the reviewee row first, then read the aggregate in a separate statement,
-- so a concurrent submit/remove about the same reviewee converges on the count
-- and mean. A locking query must not also read the aggregate (see LANDMINES).
-- FOR NO KEY UPDATE (not FOR UPDATE): the mode still serialises concurrent
-- recalculations, but does not conflict with the FOR KEY SHARE locks that
-- InsertReview's foreign-key checks take on the same users rows, so mutual
-- submissions (A reviews B while B reviews A) cannot deadlock.
-- name: LockReviewee :exec
SELECT id FROM users WHERE id = @reviewee_id::UUID FOR NO KEY UPDATE;

-- name: RevieweeStatistics :one
SELECT COUNT(*) AS review_count, COALESCE(SUM(rating), 0)::bigint AS sum_rating
FROM reviews
WHERE reviewee_id = @reviewee_id::UUID AND deleted_at IS NULL;

-- name: UpdateRevieweeStats :exec
UPDATE users
SET review_count = @count, avg_rating = @avg_rating, updated_at = now()
WHERE id = @reviewee_id::UUID;
