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

-- report_id is set when a report's outcome removed the review.
-- name: RecordReviewRemoval :exec
INSERT INTO review_removals (id, review_id, reviewer_id, reviewee_id, order_id, reason, created_at, created_by, report_id)
VALUES (@id::UUID, @review_id::UUID, @reviewer_id::UUID, @reviewee_id::UUID, @order_id::UUID, @reason, now(), @created_by::UUID,
        sqlc.narg(report_id)::UUID);

-- Locks the row; a concurrent restoration sees no removed row and reports not found.
-- name: RestoreReview :one
UPDATE reviews
SET deleted_at = NULL
WHERE id = @id::UUID AND deleted_at IS NOT NULL
RETURNING id, deleted_at, reviewer_id, reviewee_id, order_id, rating, comment, created_at;

-- The removal a restoration answers: the latest one not yet restored.
-- name: LatestUnrestoredRemoval :one
SELECT rr.id
FROM review_removals rr
WHERE rr.review_id = @review_id::UUID
  AND NOT EXISTS (SELECT 1 FROM review_restorations x WHERE x.removal_id = rr.id)
ORDER BY rr.created_at DESC, rr.id DESC
LIMIT 1;

-- name: RecordReviewRestoration :exec
INSERT INTO review_restorations (id, removal_id, review_id, appeal_id, reason, created_at, created_by)
VALUES (@id::UUID, @removal_id::UUID, @review_id::UUID, sqlc.narg(appeal_id)::UUID, @reason, now(), @created_by::UUID);

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

-- Include removed rows so callers can distinguish blocked orders from unreviewed ones.
-- name: ListReviewOrderStatuses :many
SELECT order_id::TEXT, (deleted_at IS NOT NULL)::BOOLEAN AS removed
FROM reviews
WHERE reviewer_id = @reviewer_id::UUID AND order_id = ANY(@order_ids::UUID[]);

-- name: GetReviewByReviewerAndOrder :one
SELECT r.id, r.deleted_at, r.reviewer_id, r.reviewee_id, r.order_id, r.rating, r.comment, r.created_at,
       reviewer.display_name AS reviewer_name,
       reviewee.display_name AS reviewee_name
FROM reviews r
JOIN users reviewer ON reviewer.id = r.reviewer_id
JOIN users reviewee ON reviewee.id = r.reviewee_id
WHERE r.reviewer_id = @reviewer_id::UUID AND r.order_id = @order_id::UUID;

-- Includes a removed review; the service decides who may see it.
-- name: GetReviewByID :one
SELECT r.id, r.deleted_at, r.reviewer_id, r.reviewee_id, r.order_id, r.rating, r.comment, r.created_at,
       reviewer.display_name AS reviewer_name,
       reviewee.display_name AS reviewee_name
FROM reviews r
JOIN users reviewer ON reviewer.id = r.reviewer_id
JOIN users reviewee ON reviewee.id = r.reviewee_id
WHERE r.id = @id::UUID;

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

-- The caller's removed reviews, each with its latest unrestored removal. A
-- removal that a report outcome applied is excluded; it is appealed through
-- the report.
-- name: ListMyReviewRemovals :many
SELECT latest.id, r.id AS review_id, r.reviewer_id, r.reviewee_id, r.order_id, latest.reason, latest.created_at,
       latest.created_by, creator.display_name AS created_by_name
FROM reviews r
JOIN LATERAL (
    SELECT rr.id, rr.reason, rr.created_at, rr.created_by, rr.report_id
    FROM review_removals rr
    WHERE rr.review_id = r.id
      AND NOT EXISTS (SELECT 1 FROM review_restorations x WHERE x.removal_id = rr.id)
    ORDER BY rr.created_at DESC, rr.id DESC
    LIMIT 1
) latest ON true
JOIN users creator ON creator.id = latest.created_by
WHERE r.reviewer_id = @reviewer_id::UUID AND r.deleted_at IS NOT NULL
  AND latest.report_id IS NULL
ORDER BY latest.created_at DESC, latest.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: CountMyReviewRemovals :one
SELECT COUNT(*)
FROM reviews r
JOIN LATERAL (
    SELECT rr.created_at, rr.report_id
    FROM review_removals rr
    WHERE rr.review_id = r.id
      AND NOT EXISTS (SELECT 1 FROM review_restorations x WHERE x.removal_id = rr.id)
    ORDER BY rr.created_at DESC, rr.id DESC
    LIMIT 1
) latest ON true
WHERE r.reviewer_id = @reviewer_id::UUID AND r.deleted_at IS NOT NULL
  AND latest.report_id IS NULL;

-- Lock the row before the aggregate read; FOR NO KEY UPDATE avoids conflicting
-- with the key-share locks InsertReview's foreign keys take.
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

-- Facts Moderation Service needs to accept a report or appeal about a review.
-- removal_report_id is the report behind the latest unrestored removal, if a
-- report removed it.
-- name: GetReviewFacts :one
SELECT r.reviewer_id, r.reviewee_id, r.order_id, (r.deleted_at IS NOT NULL)::BOOLEAN AS removed,
       latest.report_id AS removal_report_id
FROM reviews r
LEFT JOIN LATERAL (
    SELECT rr.report_id
    FROM review_removals rr
    WHERE rr.review_id = r.id
      AND NOT EXISTS (SELECT 1 FROM review_restorations x WHERE x.removal_id = rr.id)
    ORDER BY rr.created_at DESC, rr.id DESC
    LIMIT 1
) latest ON r.deleted_at IS NOT NULL
WHERE r.id = @id::UUID;
