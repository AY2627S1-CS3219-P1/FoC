-- name: ReviewParticipantsExist :one
SELECT EXISTS (
           SELECT 1 FROM users WHERE id = @reviewer_id::UUID
       ) AS reviewer_exists,
       EXISTS (
           SELECT 1 FROM users WHERE id = @reviewee_id::UUID
       ) AS reviewee_exists;

-- A removed review still blocks re-review, matching the insert's unique rule.
-- name: ReviewExists :one
SELECT EXISTS (
    SELECT 1 FROM reviews
    WHERE reviewer_id = @reviewer_id::UUID AND order_id = @order_id::UUID
);

-- name: InsertReview :exec
INSERT INTO reviews (id, reviewer_id, reviewee_id, order_id, rating, comment, created_at)
VALUES (@id::UUID, @reviewer_id::UUID, @reviewee_id::UUID, @order_id::UUID, @rating, @comment, now());

-- Locks the row; a concurrent removal sees no live row and reports not found.
-- name: SoftDeleteReview :one
UPDATE reviews
SET deleted_at = now()
WHERE id = @id::UUID AND deleted_at IS NULL
RETURNING id, deleted_at, reviewer_id, reviewee_id, order_id, rating, comment, created_at;

-- name: RecordReviewRemoval :exec
INSERT INTO review_removals (id, review_id, reviewer_id, reviewee_id, order_id, reason, created_at, created_by)
VALUES (@id::UUID, @review_id::UUID, @reviewer_id::UUID, @reviewee_id::UUID, @order_id::UUID, @reason, now(), @created_by::UUID);

-- name: ListReviewsForUser :many
SELECT r.id, r.deleted_at, r.reviewer_id, r.reviewee_id, r.order_id, r.rating, r.comment, r.created_at,
       reviewer.display_name AS reviewer_name,
       reviewee.display_name AS reviewee_name
FROM reviews r
JOIN users reviewer ON reviewer.id = r.reviewer_id
JOIN users reviewee ON reviewee.id = r.reviewee_id
WHERE r.reviewee_id = @user_id::UUID AND r.deleted_at IS NULL
ORDER BY r.created_at DESC, r.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: ListReviewsFromUser :many
SELECT r.id, r.deleted_at, r.reviewer_id, r.reviewee_id, r.order_id, r.rating, r.comment, r.created_at,
       reviewer.display_name AS reviewer_name,
       reviewee.display_name AS reviewee_name
FROM reviews r
JOIN users reviewer ON reviewer.id = r.reviewer_id
JOIN users reviewee ON reviewee.id = r.reviewee_id
WHERE r.reviewer_id = @user_id::UUID AND r.deleted_at IS NULL
ORDER BY r.created_at DESC, r.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: CountReviewsForUser :one
SELECT COUNT(*)
FROM reviews
WHERE reviewee_id = @user_id::UUID AND deleted_at IS NULL;

-- name: CountReviewsFromUser :one
SELECT COUNT(*)
FROM reviews
WHERE reviewer_id = @user_id::UUID AND deleted_at IS NULL;

-- Which of these requests the reviewer has already reviewed.
-- name: ListReviewedOrderIds :many
SELECT order_id::TEXT
FROM reviews
WHERE reviewer_id = @reviewer_id::UUID AND order_id = ANY(@order_ids::UUID[])
  AND deleted_at IS NULL;

-- name: GetReviewByReviewerAndOrder :one
SELECT r.id, r.deleted_at, r.reviewer_id, r.reviewee_id, r.order_id, r.rating, r.comment, r.created_at,
       reviewer.display_name AS reviewer_name,
       reviewee.display_name AS reviewee_name
FROM reviews r
JOIN users reviewer ON reviewer.id = r.reviewer_id
JOIN users reviewee ON reviewee.id = r.reviewee_id
WHERE r.reviewer_id = @reviewer_id::UUID AND r.order_id = @order_id::UUID
  AND r.deleted_at IS NULL;

-- name: ListReviewRemovals :many
SELECT rr.id, rr.review_id, rr.reviewer_id, rr.reviewee_id, rr.order_id, rr.reason, rr.created_at, rr.created_by,
       creator.display_name AS created_by_name
FROM review_removals rr
JOIN users creator ON creator.id = rr.created_by
ORDER BY rr.created_at DESC, rr.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: CountReviewRemovals :one
SELECT COUNT(*)
FROM review_removals;

-- Lock the reviewee row first, then read the aggregate in a separate statement,
-- so a concurrent submit/remove about the same reviewee converges on the count
-- and mean. A locking query must not also read the aggregate (see LANDMINES).
-- FOR NO KEY UPDATE rather than FOR UPDATE so the lock does not conflict with
-- the FOR KEY SHARE locks InsertReview's foreign-key checks take on the same
-- users rows.
-- name: LockReviewee :exec
SELECT id FROM users WHERE id = @reviewee_id::UUID FOR NO KEY UPDATE;

-- name: RevieweeStatistics :one
SELECT COUNT(*) AS review_count, COALESCE(SUM(rating), 0)::bigint AS sum_rating
FROM reviews
WHERE reviewee_id = @reviewee_id::UUID AND deleted_at IS NULL;

-- Keep the input bigint so assigning to the integer column rejects overflow.
-- name: UpdateRevieweeStats :exec
UPDATE users
SET review_count = @count::bigint, avg_rating = @avg_rating, updated_at = now()
WHERE id = @reviewee_id::UUID;
