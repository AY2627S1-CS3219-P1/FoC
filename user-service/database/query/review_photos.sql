-- name: InsertReviewPhoto :exec
INSERT INTO review_photos (
    id, owner_id, object_key, content_type, size_bytes, state, created_at, upload_expires_at
)
VALUES (
    @id::UUID, @owner_id::UUID, @object_key, @content_type, @size_bytes,
    'uploading', now(), @upload_expires_at::TIMESTAMPTZ
);

-- name: GetReviewPhoto :one
SELECT id, owner_id, object_key, content_type, size_bytes, state, review_id, ordinal, created_at, upload_expires_at
FROM review_photos
WHERE id = @id::UUID AND owner_id = @owner_id::UUID;

-- Zero rows means the photo was claimed, deleted or replaced since inspection.
-- name: AttachReviewPhoto :execrows
UPDATE review_photos
SET state = 'attached', review_id = @review_id::UUID, ordinal = @ordinal
WHERE id = @id::UUID AND owner_id = @owner_id::UUID AND state = 'uploading'
  AND object_key = @object_key AND content_type = @content_type AND size_bytes = @size_bytes;

-- name: ListReviewPhotosForReviews :many
SELECT id, review_id, object_key, content_type, ordinal
FROM review_photos
WHERE review_id = ANY (@review_ids::UUID[]) AND state = 'attached'
ORDER BY review_id, ordinal;

-- SKIP LOCKED lets concurrent replicas claim different rows.
-- name: ClaimOrphanReviewPhotos :many
UPDATE review_photos target
SET state = 'deleting'
WHERE target.id IN (
    SELECT candidate.id
    FROM review_photos candidate
    WHERE candidate.state <> 'attached'
      AND candidate.created_at <= @created_before::TIMESTAMPTZ
      AND candidate.upload_expires_at <= @now_at::TIMESTAMPTZ
    ORDER BY candidate.created_at, candidate.id
    LIMIT 50
    FOR UPDATE OF candidate SKIP LOCKED
)
RETURNING target.id, target.object_key;

-- name: DeleteOrphanReviewPhoto :execrows
DELETE FROM review_photos WHERE id = @id::UUID AND state = 'deleting';
