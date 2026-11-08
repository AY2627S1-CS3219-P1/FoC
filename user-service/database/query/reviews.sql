-- name: ReviewParticipantsExist :one
SELECT EXISTS (
            SELECT 1 FROM users WHERE id = @reviewer_id::UUID
        ) AS reviewer_exists,
        EXISTS (
            SELECT 1 FROM users WHERE id = @reviewee_id::UUID
        ) AS reviewee_exists;

-- Live eligibility for a NEW submission. A participant is eligible only when its
-- row exists, is not soft-deleted and is not permanently deleting. Checked
-- transactionally after the rows are locked (see LockSubmissionParticipants).

-- InsertReview inserts the review and recomputes the reviewee's stats in the
-- same transaction, so the stats are current when it returns.
-- name: InsertReview :exec
INSERT INTO reviews (id, reviewer_id, reviewee_id, order_id, rating, comment, created_at)
VALUES (@id::UUID, @reviewer_id::UUID, @reviewee_id::UUID, @order_id::UUID, @rating, @comment, now());

-- Lock the two submission participants in a deterministic id order so two
-- concurrent submissions about the same pair never deadlock, and so a final
-- hard delete cannot slip in between the eligibility check and the insert.
-- Returns each row that exists; callers treat a missing row as not-live.
-- name: LockSubmissionParticipants :many
SELECT id, deleted_at, is_deleting
FROM users
WHERE id = @a::UUID OR id = @b::UUID
ORDER BY id
FOR UPDATE;

-- The update locks the row and returns the data needed for the removal audit.
-- A concurrent removal waits, then finds no live row and reports not found.
-- The row is retained so review_removals can keep referencing it.
-- name: SoftDeleteReview :one
UPDATE reviews
SET deleted_at = now()
WHERE id = @id::UUID AND deleted_at IS NULL
RETURNING id, deleted_at, reviewer_id, reviewee_id, order_id, rating, comment, created_at;

-- name: RecordReviewRemoval :exec
INSERT INTO review_removals (id, review_id, reviewer_id, reviewee_id, order_id, reason, created_at, created_by)
VALUES (@id::UUID, @review_id::UUID, @reviewer_id::UUID, @reviewee_id::UUID, @order_id::UUID, @reason, now(), @created_by::UUID);

-- Retained reviews still page, count and paginate consistently whether or not
-- the participants survive. The LEFT JOINs clear the display name and flag the
-- participant as deleted once its row is gone. A participant is live only if
-- the row exists AND deleted_at IS NULL AND NOT is_deleting.
-- name: ListReviewsForUser :many
SELECT r.id, r.deleted_at, r.reviewer_id, r.reviewee_id, r.order_id, r.rating, r.comment, r.created_at,
       reviewer.display_name AS reviewer_name,
       reviewee.display_name AS reviewee_name,
       CASE WHEN reviewer.id IS NULL THEN true ELSE false END AS reviewer_deleted,
       CASE WHEN reviewee.id IS NULL THEN true ELSE false END AS reviewee_deleted
FROM reviews r
LEFT JOIN users reviewer
    ON reviewer.id = r.reviewer_id AND reviewer.deleted_at IS NULL AND NOT reviewer.is_deleting
LEFT JOIN users reviewee
    ON reviewee.id = r.reviewee_id AND reviewee.deleted_at IS NULL AND NOT reviewee.is_deleting
WHERE r.reviewee_id = @user_id::UUID AND r.deleted_at IS NULL
ORDER BY r.created_at DESC, r.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: ListReviewsFromUser :many
SELECT r.id, r.deleted_at, r.reviewer_id, r.reviewee_id, r.order_id, r.rating, r.comment, r.created_at,
       reviewer.display_name AS reviewer_name,
       reviewee.display_name AS reviewee_name,
       CASE WHEN reviewer.id IS NULL THEN true ELSE false END AS reviewer_deleted,
       CASE WHEN reviewee.id IS NULL THEN true ELSE false END AS reviewee_deleted
FROM reviews r
LEFT JOIN users reviewer
    ON reviewer.id = r.reviewer_id AND reviewer.deleted_at IS NULL AND NOT reviewer.is_deleting
LEFT JOIN users reviewee
    ON reviewee.id = r.reviewee_id AND reviewee.deleted_at IS NULL AND NOT reviewee.is_deleting
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

-- Retained removals page with their cleared author flagged when gone.
-- name: ListReviewRemovals :many
SELECT rr.id, rr.review_id, rr.reviewer_id, rr.reviewee_id, rr.order_id, rr.reason, rr.created_at, rr.created_by,
       creator.display_name AS created_by_name,
       CASE WHEN creator.id IS NULL THEN true ELSE false END AS created_by_deleted
FROM review_removals rr
LEFT JOIN users creator
    ON creator.id = rr.created_by AND creator.deleted_at IS NULL AND NOT creator.is_deleting
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

-- Ratings recalc from retained, non-removed reviews regardless of whether the
-- reviewer still has an account; the LEFT-LESS count only needs the review rows.
-- name: RevieweeStatistics :one
SELECT COUNT(*) AS review_count, COALESCE(SUM(rating), 0)::bigint AS sum_rating
FROM reviews
WHERE reviewee_id = @reviewee_id::UUID AND deleted_at IS NULL;

-- Keep the input bigint so assigning to the integer column rejects overflow.
-- name: UpdateRevieweeStats :exec
UPDATE users
SET review_count = @count::bigint, avg_rating = @avg_rating, updated_at = now()
WHERE id = @reviewee_id::UUID;
